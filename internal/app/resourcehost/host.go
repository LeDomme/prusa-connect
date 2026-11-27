// Package resourcehost carries out a reconcile plan by driving the supervision
// plane. It owns no lifecycle of its own: an instance is a supervisor.Instance,
// a restart is that instance's Restart, and a replacement is one instance
// cancelled and another started, so only the supervisor holds a state machine.
package resourcehost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"

	"github.com/prusa3d/prusa-connect/internal/app/config/install"
	"github.com/prusa3d/prusa-connect/internal/app/resource"
	"github.com/prusa3d/prusa-connect/internal/authz"
	"github.com/prusa3d/prusa-connect/internal/extension/bus"
	"github.com/prusa3d/prusa-connect/internal/extension/registry"
	"github.com/prusa3d/prusa-connect/internal/extension/supervised"
	"github.com/prusa3d/prusa-connect/internal/extension/supervisor"
	"github.com/prusa3d/prusa-connect/internal/kernel/procgroup"
	"github.com/prusa3d/prusa-connect/internal/kernel/ref"
	"github.com/prusa3d/prusa-connect/internal/transport/http/httpapi"
	"github.com/prusa3d/prusa-connect/sdk/accessapi"
	"github.com/prusa3d/prusa-connect/sdk/busapi"
	"github.com/prusa3d/prusa-connect/sdk/cameraapi"
	"github.com/prusa3d/prusa-connect/sdk/deviceapi"
	"github.com/prusa3d/prusa-connect/sdk/driverwire"
	"github.com/prusa3d/prusa-connect/sdk/extension"
	"github.com/prusa3d/prusa-connect/sdk/stateapi"
	"github.com/prusa3d/prusa-connect/sdk/storageapi"
)

// ErrNotRunning reports a host that has stopped driving: Run has returned, so
// nothing will carry a plan out and nothing is supervising what it started. A
// host whose Run has not started yet queues the plan, and the caller's context
// bounds the wait.
var ErrNotRunning = errors.New("resourcehost: the host is not running")

// requestDepth is the reconcile mailbox. One is enough: the store serialises
// its own writes, so there is never a second plan waiting.
const requestDepth = 1

// Config is what a Host is built from.
type Config struct {
	// Registry is the type table instances are constructed from.
	Registry *registry.Registry
	// Logger is the base logger each instance binds its own name to.
	Logger *slog.Logger
	// Metrics binds metric handles to an instance. Nil records nothing.
	Metrics supervisor.Binder
	// OnStatus receives every status change. The caller publishes it on the
	// event bus.
	OnStatus func(supervisor.Status)
	// Policy chooses the host policy for one instance. Nil takes the default
	// for the kind.
	Policy func(ref.Kind, resource.Declaration) supervisor.Policy
	// Events binds one instance to the local event stream it may publish on.
	// It is a function of the instance because what a script emits is an event
	// of the instance that emitted it. The host sets the origin before the
	// script runs, and the script cannot change it.
	//
	// Nil is a host with no bus, and an instance that declares an event:
	// endpoint against one is refused at start.
	Events func(ref.Ref) bus.Emitter

	// Fleet binds one module instance to the fleet it may read.
	//
	// A call the module makes for a person it is serving carries that person's
	// principal, and the host has already checked it is one it handed the
	// module in a request still open. A call naming nobody is the module's own
	// work: it is refused unless the action it takes is one the operator
	// listed for the instance at install, and is then resolved against the
	// instance's own principal in the authorization graph, so one granted
	// nothing lists nothing.
	//
	// Nil is a host that serves no fleet. A module calling device.list against
	// one reads CodeMethodNotFound. That is an error on its first call and not
	// a failure to start, because a module may not need devices at all.
	Fleet func(Module) deviceapi.Fleet

	// Storage binds one module instance to the files this deployment's storage
	// resources hold: one file described, and one range of one file read.
	//
	// It resolves a call the way Fleet does, and the permission it takes is
	// read_storage on the storage kind, the same permission the HTTP storage
	// routes take. A path that leads outside the storage is refused before
	// anything is opened.
	//
	// Nil is a host that serves no storage. A module calling storage.read
	// against one reads CodeMethodNotFound.
	Storage func(Module) storageapi.Files

	// Cameras binds one module instance to the push cameras it may hand a
	// frame to. The permission it takes is operate on the camera, which is
	// what the HTTP push route takes.
	//
	// Nil is a host that serves no camera. A module calling camera.accept
	// against one reads CodeMethodNotFound.
	Cameras func(Module) cameraapi.Frames

	// Access binds one module instance to the one question it may ask the
	// authorization graph about somebody else: may this subject perform this
	// action on this object.
	//
	// It is bounded on two sides. The action is one the instance's permissions
	// list names, which this side holds, and the subject is one the host handed
	// the instance on routes.serve since it started, which the host holds.
	//
	// Nil is a host that answers no such question. A module calling
	// access.check against one reads CodeMethodNotFound.
	Access func(Module) accessapi.Access

	// RouteAccess decides a module's call to another module's routes made in
	// a person's name, as the HTTP mount decides the person's own request.
	// Nil is a host that refuses every such call. A call a module makes as
	// itself does not read it.
	RouteAccess RouteAccess

	// People is the list of people this server keeps. A module the operator
	// approved with check_absent_people asks about a person on it while it is
	// not serving them, and a module is told yes about a person only when the
	// person is on it. A module the operator approved with list_people reads
	// the whole list.
	//
	// Nil is a server that keeps no list of people. A question about a person
	// the module is not serving is then refused as one about a subject it has
	// not served.
	People People

	// FreeSpace answers the bytes free on the filesystem that holds path. The
	// host refuses a staged body the data directory has no room for. Nil
	// checks no room.
	FreeSpace func(path string) (int64, error)

	// Feed binds one module instance to the event stream it may observe, as
	// the person it is serving or as itself, on the rule Fleet states. It is
	// the subscription half of sdk/busapi, and it is a hub subscription like
	// any other reader's.
	//
	// Nil is a host that serves no stream. A module calling bus.subscribe
	// against one reads CodeMethodNotFound.
	Feed func(Module) busapi.Feed

	// State binds one module's namespace to the key/value store the host
	// serves it. A module gets this store and no table in the host's schema.
	//
	// It is a function of the namespace and never of anything the module says.
	// The host derives the namespace from the type an operator installed, calls
	// this before the process is launched, and installs the answer as the
	// method set the child may call. There is no argument on that wire for a
	// namespace, so a module cannot ask for another module's records.
	//
	// The limit is the byte limit the namespace is held to: what the operator
	// granted the instance, or stateapi.DefaultNamespaceBytes.
	//
	// Nil is a host that serves no state. A module calling state.get against
	// one reads CodeMethodNotFound. That is an error on its first call and not
	// a failure to start, because a module may have nothing to keep.
	State func(namespace string, limit int64) (stateapi.Records, error)

	// CoreVersion is the version of the running server. A module's declared
	// core range is checked against it before the module is started. Empty
	// reads the version from the build, as a deployment does. A test sets it
	// to check the refusal.
	CoreVersion string

	// HostKeys is the settings of one kind that this host reads and the type
	// does not, such as the printer a camera watches. A block may carry them
	// and no type declares them, so the check against a type's declared
	// options gets them here. Nil is a host that reads none.
	HostKeys func(ref.Kind) []extension.Option

	// Beside is the patterns a deployment mounted next to the API, as
	// httpapi.PatternsOf renders them: a frozen device wire, a connector
	// prefix. A module's root mount may not overlap any of them, so a module
	// cannot take a path a printer dials. It is a function because the
	// connectors are mounted after this host exists and before any module
	// starts. Nil is a deployment that mounted nothing beside the API.
	Beside func() []string

	// ModuleData is the directory a module's data directory is made in, one
	// folder per instance. Empty is a host that grants no directory, and a
	// module the operator granted one is then refused at start.
	ModuleData string
	// ModulesOff is the operator's core.modules_enabled: false. The store
	// already reconciles every installed module as switched off, and this is
	// the second check: start skips one whatever the plan says. A restart or
	// an in-place change of a running instance is not
	// checked, because the store never lets an installed module run. For an
	// instance that is not running, both call start, which checks. The zero
	// value keeps modules on.
	ModulesOff bool
	// SafeMode widens ModulesOff to every module, including the ones the
	// operator declared in the configuration file. It has no effect without
	// ModulesOff.
	SafeMode bool
	// SwitchOff is the server's core.modules_switch_off: when a failing
	// service module is switched off, unless its operator set otherwise. The
	// zero value takes the install defaults.
	SwitchOff install.SwitchOff
	// Reaper is the process's Linux subreaper, which every service child is
	// started through. Nil starts children directly, which a process that
	// runs no reaper must do: a reaper collects every child it did not start.
	Reaper *procgroup.Reaper

	// hang is when a service module is taken for hung. Only tests set it; the
	// zero value is defaultHangPolicy.
	hang hangPolicy
}

// Module is one module instance as the host's inbound methods see it: which
// instance is calling, and what the operator wrote it may do as itself.
type Module struct {
	Instance ref.Ref
	// Permissions is the most the instance may do as itself, from the
	// operator's install block. It bounds a call naming no person and never a
	// call the module makes for one.
	Permissions []authz.Action
	// Checks is the actions the operator let the instance ask access.check
	// about for a person, without the instance holding them itself.
	Checks []authz.Action
	// CheckAbsentPeople is whether the operator let the instance ask
	// access.check about a person it is not serving.
	CheckAbsentPeople bool
	// Data is the instance's data directory when the operator granted one,
	// and empty otherwise. It is the path the module is sent, and the core
	// prints from its print folder.
	Data string
	// ListPeople is whether the operator let the instance list the people
	// this server holds with access.people.
	ListPeople bool
	// Calls is the namespaces of the modules the operator let the instance
	// call the routes of with routes.call.
	Calls []string
	// Dependencies is the namespaces the module named under requires and
	// suggests. A call reaches only one of them.
	Dependencies []string
}

// Host is the supervision plane behind the configuration store.
//
// Reconcile hands its plan to the goroutine Run owns. Every supervised
// instance then gets a context that lasts as long as the process, and not the
// context of the HTTP request that caused the write. The host keeps no context
// in a field.
type Host struct {
	cfg      Config
	requests chan request
	done     chan struct{}

	mu      sync.Mutex
	running map[ref.Ref]*instance
	// drivers is the host's side of each driver module, by instance. It
	// outlives the module's processes, so a restarted module meets the same
	// one (ADR-20261123-sat).
	drivers map[ref.Ref]*driverwire.Client
	// printing names the printers of a driver module that hold a print, and
	// holds is the planned step of each module that waits for them to finish.
	printing func(instance ref.Ref) []string
	holds    map[ref.Ref]*resource.Step
	// cameraTypes is the camera types the installed modules offer, as the
	// last reconcile declared them.
	cameraTypes map[string]resource.ModuleCameraType
	// mounts is every root prefix a running module holds, by prefix. It is a
	// map of its own because the router asks it on every unrouted request,
	// and a claim is checked against it at start. One prefix belongs to one
	// instance, and the first instance to claim it keeps it.
	mounts map[string]ref.Ref
	// cycles is every instance that is in a cycle of modules requiring each
	// other, mapped to that cycle. It is computed over the whole document
	// each reconcile converges on, because one instance cannot see a cycle:
	// it sees only a requirement that is not ready yet.
	cycles map[ref.Ref]string
	// serving is whether each provider of a contract served at its last
	// status change, so its dependants move only when that changes.
	serving map[ref.Ref]bool
	// moved wakes every start that waits for a provider that is still
	// starting.
	moved statusSignal

	// flights is every request the host is forwarding into a module. A
	// principal the module names in a call back to the host is checked
	// against it.
	flights flights

	// staging counts the staged bodies each instance holds on disk now.
	staging stagingSlots
	// swept is each instance whose staged/ this process has emptied, under
	// mu. It is nil until the first sweep.
	swept map[ref.Ref]struct{}

	// absent counts each instance's questions about people it is not serving.
	absent absentBudgets
	// quiet holds the warnings a module can make the host repeat.
	quiet quietLog
	// hidden is what each running module hides of its own interface.
	hidden uiHidden

	// memory is each service instance's memory watch. It is kept across a
	// stop, so the history survives a switch off and on, and dropped when
	// the declaration is removed.
	memory map[ref.Ref]*memoryWatch

	// children is every module process launched and not yet reaped.
	children liveChildren
}

// KillChildren kills every module process the host launched that has not
// ended, and waits for none of them. A shutdown that ran out of its budget
// calls it, so no module outlives the server, whatever stop timeout each
// module was given: a restart that replaces this process would otherwise
// start a second copy beside one still running.
func (h *Host) KillChildren() {
	for _, err := range h.children.kill() {
		h.cfg.Logger.Warn("a module process could not be killed", "error", err.Error())
	}
}

// request is one plan and the channel its result comes back on.
type request struct {
	plan resource.Plan
	err  chan error
}

var _ resource.Plane = (*Host)(nil)

// New returns a host that supervises nothing until Run drives it.
func New(cfg Config) (*Host, error) {
	if cfg.Registry == nil {
		return nil, fmt.Errorf("%w: no registry", registry.ErrUnknownType)
	}

	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}

	if cfg.hang == (hangPolicy{}) {
		cfg.hang = defaultHangPolicy()
	}

	return &Host{
		cfg:      cfg,
		requests: make(chan request, requestDepth),
		done:     make(chan struct{}),
		running:  map[ref.Ref]*instance{},
		mounts:   map[string]ref.Ref{},
		cycles:   map[ref.Ref]string{},
		serving:  map[ref.Ref]bool{},
		memory:   map[ref.Ref]*memoryWatch{},
		drivers:  map[ref.Ref]*driverwire.Client{},
	}, nil
}

// Run drives the host until ctx is cancelled, then stops every instance it
// started and waits for them. It returns nil on cancellation, because
// cancelling the context is the normal way to end supervision.
func (h *Host) Run(ctx context.Context) error {
	defer close(h.done)

	for {
		select {
		case <-ctx.Done():
			h.stopAll()

			return nil
		case req := <-h.requests:
			req.err <- h.carryOut(ctx, req.plan)
		}
	}
}

// Reconcile hands the plan to Run and waits for it. It blocks so that the
// store's lock is still held when the plan has been carried out: a reader that
// arrived in between would otherwise see a document whose instances are not
// running yet.
func (h *Host) Reconcile(ctx context.Context, plan resource.Plan) error {
	errc := make(chan error, 1)

	select {
	case h.requests <- request{plan: plan, err: errc}:
	case <-h.done:
		return ErrNotRunning
	case <-ctx.Done():
		return ctx.Err()
	}

	select {
	case err := <-errc:
		return err
	case <-h.done:
		return ErrNotRunning
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Status returns the supervision plane's view of one instance, and false when
// it is supervising nothing under that name.
//
// The generation is read under the lock and the supervisor is asked outside it.
// The host keeps the generation, and the goroutine Run drives writes it while
// another goroutine may be reading here. The supervisor keeps its status under
// its own lock and must not be called with this one held.
func (h *Host) Status(instance ref.Ref) (httpapi.ResourceStatus, bool) {
	h.mu.Lock()
	running, ok := h.running[instance]
	watch := h.memory[instance]

	var generation int64
	if ok {
		generation = running.generation
	}

	h.mu.Unlock()

	if !ok {
		return httpapi.ResourceStatus{}, false
	}

	status := projectStatus(running.runner.Status(), generation)

	if watch != nil {
		watch.fill(&status)
	}

	return status, true
}

// carryOut applies every step and collects the failures. It does not stop at
// the first one: an instance that cannot be constructed must not leave the
// rest of the plan unapplied.
func (h *Host) carryOut(ctx context.Context, plan resource.Plan) error {
	var failures []error

	// Before the first step, because a cycle is a property of the document and
	// not of one instance. Every instance in a cycle is refused at its own
	// start, and no instance outside the cycle is affected.
	desired := h.converging(plan)
	h.refuseCycles(desired)

	// Before any instance starts, so a camera of a module's type is built
	// once its module's offer is known (ADR-20261106-km2).
	h.mu.Lock()
	h.cameraTypes = resource.ModuleCameraTypes(desired)
	h.mu.Unlock()

	// A provider is launched before the modules that need it, so a reconcile
	// starts the modules in layers.
	for _, step := range ordered(plan, desired) {
		if err := h.step(ctx, step); err != nil {
			failures = append(failures, fmt.Errorf("%s %s: %w", step.Outcome, step.Ref, err))
		}
	}

	return errors.Join(failures...)
}

// step carries out one outcome. In place changes host policy on the running
// instance. Restart asks the supervisor for a restart. Replace is a stop and a
// start, because a different type cannot inherit the old instance's identity.
func (h *Host) step(ctx context.Context, step resource.Step) error {
	if h.held(step) {
		return nil
	}

	switch step.Outcome {
	case resource.OutcomeStart:
		return h.start(ctx, step.Desired)
	case resource.OutcomeStop:
		h.stop(step.Ref)

		if step.Desired.Ref.IsZero() {
			h.mu.Lock()
			delete(h.memory, step.Ref)
			h.mu.Unlock()
		}

		return nil
	case resource.OutcomeRestart:
		return h.restart(ctx, step)
	case resource.OutcomeReplace:
		h.stop(step.Ref)

		return h.start(ctx, step.Desired)
	case resource.OutcomeInPlace:
		return h.inPlace(ctx, step)
	case resource.OutcomeNone:
		return nil
	}

	return fmt.Errorf("%w: %s", errUnknownOutcome, step.Outcome)
}

// restart applies the new declaration and asks the supervisor to rebuild. The
// resource is reconstructed from its type on the next start, so it never sees a
// declaration it was not built from.
//
// An instance the host is not supervising is started. A reconcile converges
// on the declaration from the state the host is really in. If it refused
// because the previous plan left the instance unsupervised, the instance
// would stay down and nothing would retry it.
func (h *Host) restart(ctx context.Context, step resource.Step) error {
	h.mu.Lock()

	running, ok := h.running[step.Ref]
	if !ok {
		h.mu.Unlock()

		return h.start(ctx, step.Desired)
	}

	// The mount follows the declaration. The old claim is released and the
	// new one taken before the instance is rebuilt. A refused claim leaves
	// the instance running with the prefix it held.
	h.releaseMount(step.Ref)

	if err := h.claimMount(step.Desired); err != nil {
		_ = h.claimMount(running.declaration) // the prefix it held a moment ago, still free
		h.mu.Unlock()

		return err
	}

	running.declaration = step.Desired
	running.generation = step.Desired.Generation
	running.adopt(step.Desired)
	h.budgetMemory(step.Desired)
	running.runner.Restart()
	h.mu.Unlock()

	return nil
}

// inPlace changes host policy without reconstructing anything, so an operator
// can change required on a running instance.
func (h *Host) inPlace(ctx context.Context, step resource.Step) error {
	h.mu.Lock()

	running, ok := h.running[step.Ref]
	if !ok {
		h.mu.Unlock()

		return h.start(ctx, step.Desired)
	}

	defer h.mu.Unlock()

	policy := running.runner.Policy()
	policy.Required = step.Desired.Spec.Required
	h.withSwitchOff(&policy, step.Desired)

	if err := running.runner.SetPolicy(policy); err != nil {
		return err
	}

	running.declaration = step.Desired
	running.generation = step.Desired.Generation

	// The watch takes a new on_over_memory at its next sample. The
	// declaration is adopted so the next start launches under it too.
	running.adopt(step.Desired)
	h.budgetMemory(step.Desired)

	return nil
}

// budgetMemory gives an instance's memory watch the budget and the
// on_over_memory of the declaration it runs under. The start of a restart does
// not set them, so an edit made while a start is running is not overwritten.
// The caller holds h.mu.
func (h *Host) budgetMemory(declared resource.Declaration) {
	watch := h.memory[declared.Ref]
	if budget, isService := resource.BudgetOf(declared.Spec); isService && watch != nil {
		watch.setBudget(budget.Bytes)
		watch.setMode(declared.Spec.OnOverMemory())
	}
}

// stopAll ends supervision of everything this host started.
func (h *Host) stopAll() {
	h.mu.Lock()
	refs := make([]ref.Ref, 0, len(h.running))

	for instance := range h.running {
		refs = append(refs, instance)
	}

	h.mu.Unlock()

	for _, instance := range refs {
		h.stop(instance)
	}
}

// Stop ends supervision of one instance and waits for it, and reports false
// when this host is supervising nothing under that name.
//
// An uninstall calls it before it deletes a module's files. A running process
// holds its own executable open, and Windows refuses to delete a file while
// any process holds it, so the child has to be gone first.
//
// It changes nothing about what the instance is declared to be. A caller that
// stops an instance and leaves the declaration in place has an instance that
// starts again at the next start of this core.
func (h *Host) Stop(instance ref.Ref) bool { return h.stop(instance) }

// stop cancels one instance's supervision loop and waits for it, and reports
// whether there was one. A replace depends on the wait: the new instance must
// not be constructed while the old one still holds what the resource opened.
func (h *Host) stop(instance ref.Ref) bool {
	h.mu.Lock()
	running, ok := h.running[instance]
	delete(h.running, instance)
	h.releaseMount(instance)
	h.mu.Unlock()

	h.hidden.forget(instance)

	if !ok {
		return false
	}

	running.cancel()
	<-running.stopped

	h.mu.Lock()
	delete(h.serving, instance)
	h.mu.Unlock()

	return true
}

// closeTarget is the Stop for a kind with no dedicated wrapper. A resource that
// holds nothing to release satisfies no Closer and is left alone.
func closeTarget(_ context.Context, target any) error {
	closer, ok := target.(io.Closer)
	if !ok {
		return nil
	}

	if err := closer.Close(); err != nil {
		return fmt.Errorf("close: %w", err)
	}

	return nil
}

// defaultPolicy is the host policy for a kind. A driver and a camera get no
// breaker. Their device is often off or disconnected, and a breaker would stop
// the host from ever dialling a device that was only switched off.
func defaultPolicy(kind ref.Kind) supervisor.Policy {
	switch kind {
	case ref.KindStorage:
		return supervised.DefaultStoragePolicy()
	case ref.KindSink:
		return supervised.DefaultSinkPolicy()
	case ref.KindDriver, ref.KindCamera:
		policy := supervisor.DefaultPolicy()
		policy.Breaker = nil

		return policy
	case ref.KindAuth, ref.KindDB, ref.KindHistory, ref.KindLink, ref.KindAsset:
		return supervisor.DefaultPolicy()
	case ref.KindGCodePreset, ref.KindDashboard, ref.KindPrinter, ref.KindNode:
		// Nothing here is supervised: the intrinsic kinds are not declared at
		// all, and a gcode preset and a dashboard are stored rows with no
		// implementation behind them. ref.Kind.Supervised reports all four
		// false, so no reconcile plan names one and this arm is unreachable.
		return supervisor.DefaultPolicy()
	}

	return supervisor.DefaultPolicy()
}
