package resource

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/prusa3d/prusa-connect/internal/app/config/install"
	"github.com/prusa3d/prusa-connect/internal/kernel/ref"
	"github.com/prusa3d/prusa-connect/internal/transport/http/httpapi"
	"github.com/prusa3d/prusa-connect/internal/transport/http/httpfault"
	"github.com/prusa3d/prusa-connect/sdk/extension"
	"github.com/prusa3d/prusa-connect/sdk/fault"
	"github.com/prusa3d/prusa-connect/sdk/manifest"
)

const (
	// maxNameLen is the DNS-1123 label limit. It applies to a bare name and to
	// the name part of a qualified one. The registry validates a registered
	// name against the same limit.
	maxNameLen = extension.MaxNameLen
	// maxNamespaceLen is the limit of the namespace part. A namespace is
	// several labels joined by dots, so it may be longer than one label.
	maxNamespaceLen = extension.MaxNamespaceLen
	// typeParts is the most parts a type name has: a namespace and a label.
	typeParts = 2
)

// maxHiddenEntries and maxHiddenLen bound the hidden contributions list and
// the length of one entry.
const (
	maxHiddenEntries = 64
	maxHiddenLen     = 200
)

// hiddenSlotKeys are the slot keys an operator may hide an entry of: every
// SlotName.Key in sdk/manifest except cards, settings and device_info_section.
// A person places a card on a dashboard and removes it there, so the operator
// has nothing to hide. A settings tab and a card on a printer's Printer tab are
// where the module is set up, so they are never hidden: an operator who does
// not want them removes the module.
//
//nolint:gochecknoglobals // derived once from the closed slot table, read and never written.
var hiddenSlotKeys = func() []string {
	var keys []string
	for _, slot := range manifest.Slots() {
		if slot.Name != manifest.SlotDashboardCard && slot.Name != manifest.SlotSettings &&
			slot.Name != manifest.SlotDeviceInfoSection {
			keys = append(keys, slot.Name.Key())
		}
	}
	return keys
}()

// shownSlotKeys are the slot keys whose entries are always shown. An entry of
// them in hidden contributions is dropped, not refused, so a declaration that
// names one still saves and the entry hides nothing.
//
//nolint:gochecknoglobals // a fixed list, read and never written.
var shownSlotKeys = []string{manifest.SlotSettings.Key(), manifest.SlotDeviceInfoSection.Key()}

// keptHidden is hidden without the entries of shownSlotKeys.
func keptHidden(hidden []string) []string {
	if hidden == nil {
		return nil
	}

	kept := make([]string, 0, len(hidden))
	for _, entry := range hidden {
		key, _, _ := strings.Cut(entry, "/")
		if !slices.Contains(shownSlotKeys, key) {
			kept = append(kept, entry)
		}
	}

	return kept
}

// hiddenEntry is a slot key, a slash, and an identity with no whitespace. The
// manifest of the module is not read, so an entry can be hidden before a
// release of the module declares it.
var hiddenEntry = regexp.MustCompile(`^(` + strings.Join(hiddenSlotKeys, "|") + `)/\S+$`)

// validHidden checks the hidden contributions against their wire format.
func validHidden(hidden []string) error {
	if len(hidden) > maxHiddenEntries {
		return httpfault.RequestInvalid("body.hidden_contributions",
			"expected at most "+strconv.Itoa(maxHiddenEntries)+" entries",
			"got "+strconv.Itoa(len(hidden)))
	}

	for i, entry := range hidden {
		if len(entry) > maxHiddenLen || !hiddenEntry.MatchString(entry) {
			return httpfault.RequestInvalid("body.hidden_contributions["+strconv.Itoa(i)+"]",
				"expected <slot key>/<identity>, at most "+strconv.Itoa(maxHiddenLen)+" characters, "+
					"the slot key one of "+strings.Join(hiddenSlotKeys, ", "),
				"got "+strconv.Quote(entry))
		}

		if slices.Contains(hidden[:i], entry) {
			return httpfault.RequestInvalid("body.hidden_contributions["+strconv.Itoa(i)+"]",
				"expected each entry once", strconv.Quote(entry)+" is listed twice")
		}
	}

	return nil
}

// emptyObject is the settings block of an instance that declares none. A read
// of such an instance returns it.
var emptyObject = []byte("{}")

// Validator is the implementation's own check of a declaration, called before
// anything is written. The core cannot check a settings block: the
// implementation owns its schema, and only it can say whether a block is
// valid.
//
// An error carrying a registered fault code is returned to the client as it is.
// Any other error becomes a 422 naming the settings block.
type Validator interface {
	Validate(ctx context.Context, instance ref.Ref, typeName string, config json.RawMessage) error
}

// refuseSecondInstance refuses a second instance of a kind that allows one, on
// the API path, as the loader does for the file. A second link would be a
// second parent, and a node has one parent. A write to the instance that
// exists is allowed.
func refuseSecondInstance(instance ref.Ref, doc Document, exists bool) error {
	if exists || !instance.Kind().SoleInstance() {
		return nil
	}

	for _, declared := range doc.Instances {
		if declared.Ref.Kind() == instance.Kind() {
			return httpfault.RequestInvalid("path.name",
				"kind "+instance.Kind().String()+" allows one instance",
				declared.Ref.String()+" is already declared, so "+instance.String()+" would be a second")
		}
	}

	return nil
}

// validate turns a wire spec into the stored one. It makes every refusal that
// can be made before the file is written: an unusable type name, a settings
// block that is not an object, a change to a field fixed at creation, and
// whatever the implementation itself rejects.
func (s *Store) validate(
	ctx context.Context, instance ref.Ref, desired httpapi.ResourceSpec, current Declaration, exists bool,
) (Spec, error) {
	if !instance.Kind().Supervised() {
		return Spec{}, httpfault.RequestInvalid("path.kind",
			"expected one of "+kindNames(ref.SupervisedKinds()),
			instance.Kind().String()+" is configuration with no implementation behind it")
	}

	// Every instance this API creates is compiled into this binary, so only
	// an install declares an instance of a kind with no builtin runtime. An
	// asset is such a kind: a directory and its digest, which only an install
	// writes.
	//
	// The check applies to a create and not to a rewrite. This API switches a
	// module the install command declared on and off, so a write to an
	// existing instance must pass.
	if !exists && !slices.Contains(ref.KindRuntimes(instance.Kind()), install.RuntimeBuiltin) {
		return Spec{}, httpfault.RequestInvalid("path.kind",
			"expected a kind this API creates, one of "+kindNames(builtinKinds()),
			"an instance of kind "+instance.Kind().String()+" is written by an install, "+
				"and this API creates only what this binary compiles in")
	}

	// An instance of a kind that no code uses would be constructed, started
	// and reported ready, and no caller would ever call it. See the doc on
	// ref.Kind.Read.
	if !instance.Kind().Read() {
		return Spec{}, httpfault.RequestInvalid("path.kind",
			"expected one of "+kindNames(ref.ReadKinds()),
			"nothing in this build reads back an instance of kind "+instance.Kind().String()+
				", so one would report ready and answer nothing")
	}

	// This API creates builtin instances, and nothing reads a builtin driver
	// back (ADR-20261123-sat). A driver module an install declared is
	// rewritten under the runtime it has.
	if err := readBackAs(instance, runtimeInForce(current, exists)); err != nil {
		return Spec{}, httpfault.RequestInvalid("path.kind",
			"expected one of "+kindNames(ref.ReadKinds())+", and a driver installed as a module",
			err.Error())
	}

	if exists && desired.Type != current.Spec.Type {
		return Spec{}, httpfault.FieldImmutable("body.type")
	}

	if err := validTypeName(desired.Type); err != nil {
		return Spec{}, err
	}

	runtime := runtimeInForce(current, exists)

	if err := validRequired(desired, runtime); err != nil {
		return Spec{}, err
	}

	if err := validModule(desired, runtime); err != nil {
		return Spec{}, err
	}

	if err := s.knownType(instance, desired.Type, exists); err != nil {
		return Spec{}, err
	}

	desired.HiddenContributions = keptHidden(desired.HiddenContributions)

	if err := validHidden(desired.HiddenContributions); err != nil {
		return Spec{}, err
	}

	config, err := validConfig(desired.Config)
	if err != nil {
		return Spec{}, err
	}

	if config, err = s.keepSecrets(instance, desired.Type, config, current); err != nil {
		return Spec{}, err
	}

	// The declaration has more fields than the body. The install command
	// wrote the others, such as a module's runtime and artefact. The spec
	// therefore starts from the declaration and takes only the body's fields
	// from the request. A spec built from the body alone would lose the
	// artefact, and the module would not start again.
	spec := current.Spec
	spec.Type = desired.Type
	spec.Enabled = desired.Enabled
	spec.Required = desired.Required
	spec.Config = config
	spec.HiddenContributions = slices.Clone(desired.HiddenContributions)

	// The body has no field for the host's SwitchedOff record, so a write
	// keeps it. Switching the module on clears it.
	if spec.Enabled {
		spec.SwitchedOff = nil
	}

	if spec.Service, err = withMemory(spec, desired); err != nil {
		return Spec{}, err
	}

	if spec.Service, err = withStaged(spec, desired.StagedRoutes); err != nil {
		return Spec{}, err
	}

	if err = s.refuseSwitchingOn(current, exists, spec); err != nil {
		return Spec{}, err
	}

	if s.validator == nil {
		return spec, nil
	}

	if err = s.validator.Validate(ctx, instance, spec.Type, spec.Config); err != nil {
		return Spec{}, asClientFault(err)
	}

	return spec, nil
}

// keepSecrets puts the stored credentials back where the caller sent the
// marker RedactedValue. A read answers every credential with that marker, so
// a write of what was read keeps the stored credentials. A caller who wants to
// change one writes the new value in place of the marker.
func (s *Store) keepSecrets(
	instance ref.Ref, typeName string, config json.RawMessage, current Declaration,
) (json.RawMessage, error) {
	judged := current
	judged.Ref = instance
	judged.Spec.Type = typeName

	options, known := s.declaredOptions(judged)

	kept, err := Restore(options, known, config, current.Spec.Config)
	if err != nil {
		return nil, httpfault.RequestInvalid("body.config",
			"expected the credential itself. "+RedactedValue+" is what a read answers in place of one, "+
				"and writing it back means keep the stored value. This instance has none stored to keep",
			err.Error())
	}

	return kept, nil
}

// validTypeName accepts only a type name the registry can resolve: a DNS-1123
// label, or a namespace and a label separated by one slash. An unusable name
// is refused here, so it is never written to the file.
func validTypeName(name string) error {
	if name == "" {
		return httpfault.RequestInvalid("body.type", "expected a registered type name", "the type is empty")
	}

	parts := strings.Split(name, extension.QualifiedSeparator)
	if len(parts) > typeParts {
		return httpfault.RequestInvalid("body.type",
			"expected a name or a namespace and a name separated by one /",
			"the type name has more than one namespace separator")
	}

	// The two parts have different bounds. The name is one DNS-1123 label and
	// the namespace is several labels joined by dots. One bound for both would
	// refuse a namespace the host derived for a module it installed.
	bounds := []int{maxNameLen}
	if len(parts) == typeParts {
		bounds = []int{maxNamespaceLen, maxNameLen}
	}

	for i, part := range parts {
		if part == "" || len(part) > bounds[i] {
			return httpfault.RequestInvalid("body.type",
				"expected a name of 1 to "+strconv.Itoa(maxNameLen)+" bytes, "+
					"after an optional namespace of 1 to "+strconv.Itoa(maxNamespaceLen)+" bytes",
				"the type name has an empty or oversized part")
		}
	}

	return nil
}

// knownType refuses a new instance whose type this build has no
// implementation for, before the file is written. If it were written, it
// would fail at its first start.
//
// An instance that already exists is not refused, so an operator can still
// switch off an instance whose type this build does not have.
func (s *Store) knownType(instance ref.Ref, typeName string, exists bool) error {
	if exists || s.declared == nil {
		return nil
	}

	if _, known := s.declared.DeclaredOptions(instance.Kind(), typeName); known {
		return nil
	}

	return httpfault.RequestInvalid("body.type",
		"expected a type this build has for kind "+instance.Kind().String(),
		"this build has no "+instance.Kind().String()+" type named "+strconv.Quote(typeName))
}

// runtimeInForce is the runtime the declaration already has, and builtin for an
// instance that is being created. Only the install command and the operator's
// file write a runtime, so a create is builtin and a rewrite keeps the
// installed runtime.
//
// validRequired and validModule check against this runtime and not against
// builtin, because this API switches an instance the install command declared
// on and off. A check against builtin would refuse every write to a module.
func runtimeInForce(current Declaration, exists bool) install.Runtime {
	if !exists || current.Spec.Runtime == "" {
		return install.RuntimeBuiltin
	}

	return current.Spec.Runtime
}

// validRequired allows required only for a type of the core.
// internal/app/config/install applies the same rule to the operator's file.
//
// It is refused at the write and not at the next load. A caller who got 201
// and then found the instance missing would not know which field to change.
func validRequired(desired httpapi.ResourceSpec, runtime install.Runtime) error {
	if !desired.Required || install.IsCore(runtime, desired.Type) {
		return nil
	}

	return httpfault.RequestInvalid("body.required",
		"expected false for a type outside the core",
		"an instance the core did not write may not hold the whole server out of readiness")
}

// validModule refuses a module that would run inside this binary. A module
// runs in a process of its own. An instance this API creates is builtin, so a
// type outside the core namespace would be a module in the host's own address
// space, where the host cannot confine it.
//
// An instance the install command declared is checked against the runtime it
// was declared with, so a module that runs in its own process is not refused.
//
// It is called after validRequired, so a request that sets both is told about
// required first. A caller who removes required is then told about the type.
//
// This API cannot fix the refusal. A module is declared with runtime: service
// and a service block, and this API writes neither, so the refusal names the
// operator's file.
func validModule(desired httpapi.ResourceSpec, runtime install.Runtime) error {
	if !install.ModuleAsBuiltin(runtime, desired.Type) {
		return nil
	}

	return httpfault.RequestInvalid("body.type",
		"expected a type this binary ships, because every instance this API writes runs inside it",
		"a module runs as a supervised child process and is declared in the operator's file, "+
			"with runtime: "+install.RuntimeService.String()+" and the artefact it ships")
}

// withMemory is the service block with the operator's memory budget,
// on_over_memory and switch_off. The block is copied, because the original
// belongs to the declaration in force.
func withMemory(spec Spec, desired httpapi.ResourceSpec) (*install.Service, error) {
	memoryMiB, onOverMemory := desired.MemoryMiB, desired.OnOverMemory

	var switchOff install.SwitchOff
	if desired.SwitchOff != nil {
		switchOff = install.SwitchOff{
			Failures: desired.SwitchOff.Failures,
			Window:   time.Duration(desired.SwitchOff.WindowSeconds) * time.Second,
		}
	}

	if spec.Runtime != install.RuntimeService || spec.Service == nil {
		if memoryMiB != 0 {
			return nil, httpfault.RequestInvalid("body.memory_mib",
				"expected 0 or no key, because only a service module has a memory budget",
				"this instance runs no process of its own")
		}

		if onOverMemory != "" {
			return nil, httpfault.RequestInvalid("body.on_over_memory",
				"no key, because only a service module has a memory budget",
				"this instance runs no process of its own")
		}

		if !switchOff.IsZero() {
			return nil, httpfault.RequestInvalid("body.switch_off",
				"no key, because only a service module is switched off by the host",
				"this instance runs no process of its own")
		}

		return spec.Service, nil
	}

	if err := install.CheckMemoryMiB("body.memory_mib", memoryMiB); err != nil {
		return nil, httpfault.RequestInvalid("body.memory_mib",
			"expected 0 to follow the module, or a budget in MiB within the bounds", err.Error())
	}

	if err := install.CheckOnOverMemory("body.on_over_memory", onOverMemory); err != nil {
		return nil, httpfault.RequestInvalid("body.on_over_memory",
			"expected stop, warn or no key", err.Error())
	}

	if desired.SwitchOff != nil && desired.SwitchOff.WindowSeconds != 0 &&
		(switchOff.Window < install.MinSwitchOffWindow || switchOff.Window > install.MaxSwitchOffWindow) {
		return nil, httpfault.RequestInvalid("body.switch_off.window_seconds",
			"seconds from 60 to 86400, or 0 or no key to follow the server",
			"window_seconds is "+strconv.Itoa(desired.SwitchOff.WindowSeconds))
	}

	if err := install.CheckSwitchOff("body.switch_off", switchOff); err != nil {
		return nil, httpfault.RequestInvalid("body.switch_off.failures",
			"failures from 0 to 100, or no key to follow the server", err.Error())
	}

	service := *spec.Service
	service.MemoryMiB = memoryMiB
	service.OnOverMemory = onOverMemory
	service.SwitchOff = switchOff

	return &service, nil
}

// withStaged is the service block carrying the limits a write sets for the
// staged routes the operator approved at install (ADR-20261120-xys rule 5).
// A write changes a limit and never which routes are staged, so a route the
// body leaves out keeps its limit, and a route the declaration does not stage
// is refused. The block is copied, because the spec it came from is the
// declaration still in force.
func withStaged(spec Spec, desired []httpapi.ModuleStagedRoute) (*install.Service, error) {
	if len(desired) == 0 {
		return spec.Service, nil
	}

	if spec.Runtime != install.RuntimeService || spec.Service == nil {
		return nil, httpfault.RequestInvalid("body.staged_routes",
			"no key, because only a service module has staged routes",
			"this instance runs no process of its own")
	}

	service := *spec.Service
	service.StagedRoutes = slices.Clone(spec.Service.StagedRoutes)

	for index, route := range desired {
		key := "body.staged_routes[" + strconv.Itoa(index) + "]"

		held := slices.IndexFunc(service.StagedRoutes, func(granted install.StagedRoute) bool {
			return granted.Method == route.Method && granted.Path == route.Path
		})
		if held < 0 {
			return nil, httpfault.RequestInvalid(key,
				"a route the install approved under routes.staged",
				route.Method+" "+route.Path+" is not a staged route of this module. Approve it by "+
					"installing the module again")
		}

		if err := install.CheckStagedMiB(key+".max_mib", route.MaxMiB); err != nil {
			return nil, httpfault.RequestInvalid(key+".max_mib",
				"a limit in MiB from 1 to "+strconv.Itoa(manifest.MaxStagedMiB), err.Error())
		}

		service.StagedRoutes[held].MaxMiB = route.MaxMiB
	}

	return &service, nil
}

// validConfig checks that the settings block is a JSON object, as the
// published schema says, and canonicalises it. Two ways of writing the same
// settings then compare equal, and a reload does not count as an edit.
//
// An empty object is stored as no settings. A read of an instance with no
// settings returns an empty object, so storing it would make a client's
// unchanged write count as a change and raise the generation.
func validConfig(raw httpapi.ResourceConfig) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}

	if trimmed[0] != '{' {
		return nil, httpfault.RequestInvalid("body.config",
			"expected an object of the implementation's own settings",
			"the settings block is not a JSON object")
	}

	canonical, err := canonicalJSON(trimmed)
	if err != nil {
		return nil, httpfault.RequestInvalid("body.config",
			"expected an object of the implementation's own settings", "the settings block is not valid JSON")
	}

	if bytes.Equal(canonical, emptyObject) {
		return nil, nil
	}

	return canonical, nil
}

// asClientFault returns a registered fault unchanged and wraps any other
// error. A validator that chose a code has chosen what the client is told.
func asClientFault(err error) error {
	if _, ok := errors.AsType[*fault.Fault](err); ok {
		return err
	}

	return httpfault.RequestInvalid("body.config", "the implementation rejected these settings", err.Error())
}

// builtinKinds is the supervised kinds that have a builtin runtime. This API
// creates instances of these kinds only.
func builtinKinds() []ref.Kind {
	out := make([]ref.Kind, 0, len(ref.SupervisedKinds()))

	for _, kind := range ref.SupervisedKinds() {
		if slices.Contains(ref.KindRuntimes(kind), install.RuntimeBuiltin) {
			out = append(out, kind)
		}
	}

	return out
}

// kindNames renders a list of kinds, for a refusal that names the legal
// values.
func kindNames(kinds []ref.Kind) string {
	names := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		names = append(names, kind.String())
	}

	return strings.Join(names, ", ")
}

// sortPresets orders the overlay's presets by name, for the same reason as
// sortEntries.
func sortPresets(presets []overlayPreset) {
	slices.SortFunc(presets, func(a, b overlayPreset) int { return strings.Compare(a.Name, b.Name) })
}

// sortEntries orders the overlay's instances by kind and then by name, so a
// write changes only the lines of the instance it wrote.
func sortEntries(entries []overlayEntry) {
	slices.SortFunc(entries, func(a, b overlayEntry) int {
		kinds := slices.Index(ref.Kinds(), ref.Kind(a.Kind)) - slices.Index(ref.Kinds(), ref.Kind(b.Kind))
		if kinds != 0 {
			return kinds
		}

		return strings.Compare(a.Name, b.Name)
	})
}
