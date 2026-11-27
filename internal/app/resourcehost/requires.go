package resourcehost

import (
	"context"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/prusa3d/prusa-connect/internal/app/config/install"
	"github.com/prusa3d/prusa-connect/internal/app/resource"
	"github.com/prusa3d/prusa-connect/internal/extension/supervisor"
	"github.com/prusa3d/prusa-connect/internal/kernel/condition"
	"github.com/prusa3d/prusa-connect/internal/kernel/ref"
	"github.com/prusa3d/prusa-connect/sdk/accessapi"
	"github.com/prusa3d/prusa-connect/sdk/busapi"
	"github.com/prusa3d/prusa-connect/sdk/cameraapi"
	"github.com/prusa3d/prusa-connect/sdk/deviceapi"
	"github.com/prusa3d/prusa-connect/sdk/driverwire"
	"github.com/prusa3d/prusa-connect/sdk/fault"
	"github.com/prusa3d/prusa-connect/sdk/linkapi"
	"github.com/prusa3d/prusa-connect/sdk/manifest"
	"github.com/prusa3d/prusa-connect/sdk/routesapi"
	"github.com/prusa3d/prusa-connect/sdk/stateapi"
	"github.com/prusa3d/prusa-connect/sdk/storageapi"
	"github.com/prusa3d/prusa-connect/sdk/uiapi"
	"github.com/prusa3d/prusa-connect/sdk/wirerpc"
)

// A module declares its requirements in the `requires:` list of its manifest,
// and the host does not start the module while one of them is not met. The
// install copies the list into the `module` block of the instance declaration,
// because the manifest of a module that runs a process is not kept on disk.
//
// The host checks each requirement before it launches the process. A
// requirement names one of these:
//
//   - a capability: a wire method that servesTo installs for this instance.
//     The methods a build can install are fixed when it is compiled.
//   - a range of core versions, compared with the version of the running build.
//   - a contract another module provides. Over api it is
//     met when a module that speaks it is serving, and over direct when one is
//     declared. An operator can install such a module, so the host retries
//     only this kind of requirement.
//   - a request header the module marked required in routes.headers, met
//     when the service block grants it (ADR-20261118-nnm).

// reasonDependencyMissing is the reason of the DependenciesSatisfied condition
// while an instance waits for a contract that no module serves.
//
// It is a separate reason from the two that the state derivation reads. Those
// two mean the instance has failed. This one means the host still retries the
// start.
const reasonDependencyMissing = "DependencyMissing"

// Capabilities returns every wire method a module may name in a requirement.
// It is the union of the method sets that servesTo installs.
//
// It is a fixed list and does not need a running host, because the install
// command has no host and checks the same requirement before it writes
// anything. Add a method here when you add it to one of those sets. Otherwise
// the install refuses a module that requires a method the host serves.
func Capabilities() []string {
	methods := []wirerpc.Method{
		stateapi.MethodGet, stateapi.MethodPut, stateapi.MethodCompareAndPut,
		stateapi.MethodDelete, stateapi.MethodList, stateapi.MethodVersion,
		stateapi.MethodUsage,
		busapi.MethodEmit, busapi.MethodSubscribe,
		deviceapi.MethodList, deviceapi.MethodGet, deviceapi.MethodCommand,
		deviceapi.MethodApprove, deviceapi.MethodJobs, deviceapi.MethodFiles,
		deviceapi.MethodPrint, deviceapi.MethodSend, deviceapi.MethodJobHistory,
		storageapi.MethodStat, storageapi.MethodRead, storageapi.MethodList,
		storageapi.MethodFetch, storageapi.MethodWrite, storageapi.MethodMove,
		cameraapi.MethodAccept,
		accessapi.MethodCheck, accessapi.MethodPeople,
		linkapi.MethodChanged,
		uiapi.MethodHide,
		routesapi.MethodCall,
	}
	methods = append(methods, driverwire.HostMethods()...)

	out := make([]string, 0, len(methods))
	for _, method := range methods {
		out = append(out, string(method))
	}

	slices.Sort(out)

	return out
}

// dependencyError is the error for a requirement that is not met. The
// supervisor reads two things from it: the fault, which says whether a retry
// can help, and DependencyReason, which becomes the reason of the
// DependenciesSatisfied condition.
type dependencyError struct {
	raised *fault.Fault
	reason string
}

func (d dependencyError) Error() string { return d.raised.Error() }

func (d dependencyError) Unwrap() error { return d.raised }

// DependencyReason implements supervisor.DependencyRefusal.
func (d dependencyError) DependencyReason() string { return d.reason }

// requirementsOf returns the requirements in the declaration of an instance.
// It returns none for an instance that is not a module, and for a module whose
// manifest declared none.
func requirementsOf(declared *serviceSpec) []install.Requirement {
	if declared == nil || declared.module == nil {
		return nil
	}

	return declared.module.Requires
}

// dependenciesMet returns an error when this host does not meet a requirement
// of the instance at this moment. The start calls it before it launches the
// process of the module, so a module with an unmet requirement never runs.
//
// serves is the set of wire methods the host built for this instance.
// dependenciesMet looks up a capability requirement in it.
//
// ctx bounds how long a start waits for a provider that is still starting, so
// it is the context of the start, with the start budget.
func (h *Host) dependenciesMet(
	ctx context.Context, declaration resource.Declaration, declared *serviceSpec, serves wirerpc.Methods,
) error {
	instance := declaration.Ref

	if ring := h.cycleThrough(instance); ring != "" {
		return dependencyError{
			reason: condition.ReasonDependencyCycle,
			raised: dependencyCycle.New("the instance is in a ring of modules that require each other").
				With(
					fault.StringParam("resource", instance.String()),
					fault.StringParam("cycle", ring),
				),
		}
	}

	for _, required := range requirementsOf(declared) {
		if err := h.meets(ctx, declaration, required, serves); err != nil {
			return err
		}
	}

	return nil
}

// meets checks one requirement. The field that is set, Capability, Core,
// Module or Header, selects the check and the error code.
func (h *Host) meets(
	ctx context.Context, declaration resource.Declaration, required install.Requirement, serves wirerpc.Methods,
) error {
	instance := declaration.Ref

	switch {
	case recordCapability(required.Capability):
		return h.meetsRecords(declaration, required)
	case required.Capability != "":
		if _, installed := serves[wirerpc.Method(required.Capability)]; installed {
			return nil
		}

		return unservedBy(instance, required,
			"the module requires the host capability "+required.Capability+", which this build does not serve",
			strings.Join(Capabilities(), ", "))
	case required.Core != "":
		return h.meetsCore(instance, required)
	case required.Module != "":
		return h.meetsModule(ctx, instance, required)
	case required.Header != "":
		return meetsHeader(declaration, required)
	}

	return nil
}

// meetsHeader checks that the service block grants a request header the
// module marked required (ADR-20261118-nnm). A grant the operator took away,
// or never wrote, keeps the instance from starting, and the error names the
// header.
func meetsHeader(declaration resource.Declaration, required install.Requirement) error {
	var granted []string
	if spec := serviceOf(declaration); spec != nil {
		granted = spec.service.RequestHeaders
	}

	if slices.Contains(granted, http.CanonicalHeaderKey(required.Header)) {
		return nil
	}

	return dependencyError{
		reason: condition.ReasonDependencyFailed,
		raised: headerNotGranted.New(
			"the module requires the request header "+required.Header+
				", and service.request_headers does not grant it").
			With(
				fault.StringParam("resource", declaration.Ref.String()),
				fault.StringParam("header", required.Header),
			),
	}
}

// meetsCore checks that the version of the running build is in the range the
// module declared.
//
// Go records no version for a binary built from a source directory. meetsCore
// skips the check for such a build, so a development build does not refuse
// every module. The install command skips it in the same case.
func (h *Host) meetsCore(instance ref.Ref, required install.Requirement) error {
	if _, err := manifest.ParseConstraint(required.Core); err != nil {
		return unservedBy(instance, required,
			"the module requires the core constraint "+required.Core+", which this server cannot read", "")
	}

	running := h.cfg.CoreVersion
	if running == "" {
		running = install.CoreVersion()
	}

	if running == "" {
		return nil
	}

	in, readable := install.CoreHolds(required.Core, running)
	if !readable || in {
		return nil
	}

	return unservedBy(instance, required,
		"the module requires a core version in "+required.Core+", and this server is "+running, running)
}

// meetsModule checks the module the requirement names: it serves, and its
// release is in the range.
//
// While that module is pending or starting, the start waits for it inside its
// own budget, so a dependant launches after the module it needs. The start
// does not wait for a module that is down for another reason. The instance is
// then retried with backoff, and the host retries it at once when the module
// serves again.
func (h *Host) meetsModule(ctx context.Context, instance ref.Ref, required install.Requirement) error {
	dependency := required.Dependency()

	for {
		moved := h.moved.next()

		held, found := h.moduleUnder(dependency.Module, instance)
		if !found || !inRange(held.declaration, dependency) {
			return dependencyMissingFor(instance, required)
		}

		state := held.runner.State()
		if state.Serving() {
			return nil
		}

		if state != condition.StatePending() && state != condition.StateStarting() {
			return dependencyMissingFor(instance, required)
		}

		select {
		case <-moved:
		case <-ctx.Done():
			return dependencyMissingFor(instance, required)
		}
	}
}

// dependencyMissingFor is the refusal of a start whose required module does
// not serve, or serves a release out of range.
func dependencyMissingFor(instance ref.Ref, required install.Requirement) error {
	return dependencyError{
		reason: reasonDependencyMissing,
		raised: dependencyMissing.New("this module requires "+required.Dependency().String()+
			", and no release in that range is serving").
			With(
				fault.StringParam("resource", instance.String()),
				fault.StringParam("requires", required.Describe()),
			),
	}
}

// heldModule is the instance the host holds of one module.
type heldModule struct {
	instance    ref.Ref
	declaration resource.Declaration
	runner      *supervisor.Instance[any]
}

// moduleUnder is the instance the host holds under a namespace, other than
// the one asking. A namespace is claimed by one instance.
func (h *Host) moduleUnder(namespace string, asking ref.Ref) (heldModule, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for instance, running := range h.running {
		if held, isModule := running.declaration.ModuleNamespace(); isModule && held == namespace &&
			instance != asking {
			return heldModule{instance: instance, declaration: running.declaration, runner: running.runner}, true
		}
	}

	return heldModule{}, false
}

// inRange reports whether a declaration's release is in a dependency's range.
func inRange(declared resource.Declaration, dependency install.Dependency) bool {
	return declared.Spec.Module != nil && dependency.Holds(declared.Spec.Module.Version)
}

// meets reports whether a declaration is the module a dependency names, with
// a release in range.
func meets(declared resource.Declaration, dependency install.Dependency) bool {
	namespace, isModule := declared.ModuleNamespace()

	return isModule && namespace == dependency.Module && inRange(declared, dependency)
}

// unservedBy returns the error for a requirement that a retry cannot meet: a
// capability this build does not install, or a core version range that does
// not include this build.
//
// detail is the message of the condition of the instance. It names the
// requirement first, and then the value this server has. serves lists every
// capability or version this server offers. The API publishes it as a
// parameter of the error, because it is too long for the message.
func unservedBy(instance ref.Ref, required install.Requirement, detail, serves string) error {
	return dependencyError{
		reason: condition.ReasonDependencyFailed,
		raised: dependencyUnserved.New(detail).
			With(
				fault.StringParam("resource", instance.String()),
				fault.StringParam("requires", required.Describe()),
				fault.StringParam("serves", serves),
			),
	}
}

// cycleThrough returns the cycle of requirements that contains one instance,
// as text for an operator. It returns an empty string for an instance that is
// in no cycle.
func (h *Host) cycleThrough(instance ref.Ref) string {
	h.mu.Lock()
	defer h.mu.Unlock()

	return h.cycles[instance]
}

// converging returns the declarations this host will have after it applies
// the plan: the declarations of the running instances, with each step of the
// plan applied.
//
// A plan lists only the instances that change. The running instances that do
// not change come from the host.
func (h *Host) converging(plan resource.Plan) []resource.Declaration {
	h.mu.Lock()

	held := make(map[ref.Ref]resource.Declaration, len(h.running))
	for instance, running := range h.running {
		held[instance] = running.declaration
	}

	h.mu.Unlock()

	for _, step := range plan.Steps {
		if step.Outcome == resource.OutcomeStop {
			delete(held, step.Ref)

			continue
		}

		if !step.Desired.Ref.IsZero() {
			held[step.Desired.Ref] = step.Desired
		}
	}

	return slices.Collect(maps.Values(held))
}

// refuseCycles records every instance that is in a cycle of modules that
// require each other. The start of such an instance then fails, where it would
// otherwise wait for the other module without an end.
//
// It reads all the declarations that converging returns. One instance alone
// cannot detect a cycle: it only finds that its requirement is not ready.
func (h *Host) refuseCycles(desired []resource.Declaration) {
	rings := cyclesAmong(desired)

	h.mu.Lock()
	defer h.mu.Unlock()

	h.cycles = rings
}

// cyclesAmong finds every cycle in the graph of hard dependencies. For each
// instance in a cycle it returns that cycle as text.
//
// The graph has an edge from an instance to the enabled instance of each
// module it requires. An instance that is in no cycle gets no
// entry, so a cycle fails only its own instances.
func cyclesAmong(desired []resource.Declaration) map[ref.Ref]string {
	edges := requirementEdges(desired)
	rings := map[ref.Ref]string{}

	for instance := range edges {
		if path, found := ringFrom(instance, edges); found {
			rings[instance] = path
		}
	}

	return rings
}

// requirementEdges builds the graph. It maps each instance to the instances
// of the modules it requires. A suggestion adds no edge.
func requirementEdges(desired []resource.Declaration) map[ref.Ref][]ref.Ref {
	edges := map[ref.Ref][]ref.Ref{}

	for _, declared := range desired {
		if declared.Spec.Module == nil || !declared.Spec.Enabled {
			continue
		}

		for _, required := range declared.Spec.Module.Requires {
			dependency := required.Dependency()
			if dependency.Module == "" {
				continue
			}

			for _, candidate := range desired {
				if candidate.Ref != declared.Ref && candidate.Spec.Enabled && meets(candidate, dependency) {
					edges[declared.Ref] = append(edges[declared.Ref], candidate.Ref)
				}
			}
		}
	}

	return edges
}

// layersOf is how deep each instance sits in the graph of hard dependencies:
// zero for one that requires no module, and one more than its deepest
// dependency otherwise. A reconcile starts the instances in this order, so a
// module is launched before its dependants. An instance on a cycle is
// refused at its start whatever its depth.
func layersOf(desired []resource.Declaration) map[ref.Ref]int {
	edges := requirementEdges(desired)
	depth := map[ref.Ref]int{}
	visiting := map[ref.Ref]bool{}

	var walk func(instance ref.Ref) int

	walk = func(instance ref.Ref) int {
		if held, done := depth[instance]; done {
			return held
		}

		if visiting[instance] {
			return 0
		}

		visiting[instance] = true
		deepest := 0

		for _, provider := range edges[instance] {
			deepest = max(deepest, walk(provider)+1)
		}

		visiting[instance] = false
		depth[instance] = deepest

		return deepest
	}

	for _, declared := range desired {
		walk(declared.Ref)
	}

	return depth
}

// ringFrom searches the graph from one instance. It returns the cycle that
// leads back to that instance, as the list of instances on it.
func ringFrom(start ref.Ref, edges map[ref.Ref][]ref.Ref) (string, bool) {
	type step struct {
		at   ref.Ref
		path []ref.Ref
	}

	queue := []step{{at: start, path: []ref.Ref{start}}}
	seen := map[ref.Ref]bool{}

	for len(queue) > 0 {
		held := queue[0]
		queue = queue[1:]

		for _, next := range edges[held.at] {
			if next == start {
				return renderRing(append(slices.Clone(held.path), start)), true
			}

			if seen[next] {
				continue
			}

			seen[next] = true
			queue = append(queue, step{at: next, path: append(slices.Clone(held.path), next)})
		}
	}

	return "", false
}

// renderRing writes the instances of a cycle in the order in which each waits
// for the next, so an operator can see which requirement to remove.
func renderRing(path []ref.Ref) string {
	names := make([]string, 0, len(path))
	for _, instance := range path {
		names = append(names, instance.String())
	}

	return strings.Join(names, " requires ")
}
