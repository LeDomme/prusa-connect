package app

// The modules one module depends on, as an install records them, and the
// rules a removal is held to. The command and the API both reach these, so a
// change refused on one path is refused on the other.

import (
	"context"
	"maps"
	"slices"
	"strings"

	"github.com/prusa3d/prusa-connect/internal/app/config"
	"github.com/prusa3d/prusa-connect/internal/app/config/install"
	"github.com/prusa3d/prusa-connect/internal/app/resource"
	"github.com/prusa3d/prusa-connect/internal/authz"
	"github.com/prusa3d/prusa-connect/internal/kernel/ref"
	"github.com/prusa3d/prusa-connect/internal/transport/http/httpapi"
	"github.com/prusa3d/prusa-connect/sdk/manifest"
)

// recordedSuggests is the manifest's suggest as the declaration keeps it.
func recordedSuggests(declared []manifest.Suggestion) []install.Suggestion {
	if len(declared) == 0 {
		return nil
	}

	out := make([]install.Suggestion, 0, len(declared))
	for _, suggested := range declared {
		out = append(out, install.Suggestion{Module: suggested.Module, Reason: suggested.Reason})
	}

	return out
}

// recordedConflicts is the manifest's conflict as the declaration keeps it.
func recordedConflicts(declared []manifest.Dependency) []install.Dependency {
	if len(declared) == 0 {
		return nil
	}

	out := make([]install.Dependency, 0, len(declared))
	for _, conflict := range declared {
		out = append(out, install.Dependency{Module: conflict.Module, Version: conflict.Version})
	}

	return out
}

// requiredModules is the modules a manifest requires, without its
// capabilities and core range.
func requiredModules(declared []manifest.Requirement) []install.Dependency {
	var out []install.Dependency

	for _, required := range declared {
		if required.Module != "" {
			out = append(out, install.Dependency{Module: required.Module, Version: required.Version})
		}
	}

	return out
}

// moduleRecordOf is the declaration an install is about to write, with only
// what the resolver reads: the type, the switch, the runtime, the release and
// the dependencies.
func moduleRecordOf(instance ref.Ref, namespace string, held manifest.Manifest, enabled bool) resource.Declaration {
	runtime := install.RuntimeAsset
	if held.ContractMajor > 0 {
		runtime = install.RuntimeService
	}

	return resource.Declaration{
		Ref:    instance,
		Source: resource.SourceOverlay,
		Spec: resource.Spec{
			Type:    namespace + "/" + held.Name,
			Enabled: enabled,
			Runtime: runtime,
			Module: &install.ModuleEntry{
				Version:     held.Version,
				Requires:    recordedRequirements(held),
				Suggests:    recordedSuggests(held.Suggests),
				Conflicts:   recordedConflicts(held.Conflicts),
				FileTypes:   fileTypesOf(held.FileAnalysis),
				ViewTypes:   viewTypesOf(held),
				CameraTypes: cameraTypesOf(held.CameraTypes),
				Driver:      driverBlockOf(held.Driver),
			},
		},
	}
}

// removalResolves refuses a removal another installed module requires, and
// answers the modules an install added for others that nothing will require
// once this one is gone. Those are removed only when the operator asks.
func removalResolves(core config.Core, configPath string, found resource.Declaration) ([]resource.Declaration, error) {
	doc, err := resource.Load(resource.Paths{File: configPath, Overlay: core.Overlay})
	if err != nil {
		return nil, err
	}

	if err = resource.RefuseRemoval(doc.Instances, found); err != nil {
		return nil, err
	}

	if err = resource.CheckChange(doc.Instances, resource.Without(doc.Instances, found.Ref), found.Ref); err != nil {
		return nil, err
	}

	return resource.Unused(doc.Instances, found.Ref), nil
}

// withComposer fills an offer's require, suggest and conflict from the maps
// an index row or a marketplace release states, in Composer's shape: a
// module, core or core-<capability> against a constraint, and a module
// against a reason for suggest.
func withComposer(offer *httpapi.ModuleOffer, require, suggest, conflict map[string]string) {
	offer.Requires, offer.Suggests, offer.Conflicts = nil, nil, nil

	for _, name := range slices.Sorted(maps.Keys(require)) {
		switch {
		case name == manifest.PlatformCore:
			offer.Requires = append(offer.Requires, httpapi.ResourceRequirement{Core: require[name]})
		case strings.HasPrefix(name, manifest.PlatformCapability):
			offer.Requires = append(offer.Requires, httpapi.ResourceRequirement{
				Capability: strings.TrimPrefix(name, manifest.PlatformCapability),
			})
		default:
			offer.Requires = append(offer.Requires, httpapi.ResourceRequirement{Module: name, Version: require[name]})
		}
	}

	for _, name := range slices.Sorted(maps.Keys(suggest)) {
		offer.Suggests = append(offer.Suggests, httpapi.ResourceRequirement{Module: name, Reason: suggest[name]})
	}

	for _, name := range slices.Sorted(maps.Keys(conflict)) {
		offer.Conflicts = append(offer.Conflicts, httpapi.ResourceRequirement{Module: name, Version: conflict[name]})
	}
}

// withManifest fills an offer's require, suggest, conflict and file types from
// a release's manifest.
func withManifest(offer *httpapi.ModuleOffer, declared manifest.Manifest) {
	require, suggest, conflict := map[string]string{}, map[string]string{}, map[string]string{}

	for _, required := range declared.Requires {
		switch {
		case required.Module != "":
			require[required.Module] = required.Version
		case required.Core != "":
			require[manifest.PlatformCore] = required.Core
		case required.Capability != "":
			require[manifest.PlatformCapability+required.Capability] = "*"
		}
	}

	for _, suggested := range declared.Suggests {
		suggest[suggested.Module] = suggested.Reason
	}

	for _, held := range declared.Conflicts {
		conflict[held.Module] = held.Version
	}

	withComposer(offer, require, suggest, conflict)

	offer.FileTypes, offer.ViewTypes = fileTypesOf(declared.FileAnalysis), viewTypesOf(declared)
	offer.License = declared.License
	offer.Egress, offer.EgressKnown = egressOf(declared.Egress), true
}

// egressOf is the manifest's egress as the API states it.
func egressOf(declared []manifest.Egress) []httpapi.ModuleEgress {
	out := make([]httpapi.ModuleEgress, 0, len(declared))

	for _, one := range declared {
		out = append(out, httpapi.ModuleEgress{
			Host: one.Host, Option: one.Option, Ports: one.Ports,
			From: string(one.From), Why: one.Why, Sends: one.Sends,
		})
	}

	return out
}

// dependenciesOf fills an offer's dependencies from a local index row, or
// from the manifest of the release directory the row points at when the row
// states none, as requestsOf does for the requests.
func dependenciesOf(offer *httpapi.ModuleOffer, row moduleIndexRow) {
	offer.FileTypes, offer.ViewTypes = row.FileTypes, row.ViewTypes

	if row.Require != nil || row.Suggest != nil || row.Conflict != nil {
		withComposer(offer, row.Require, row.Suggest, row.Conflict)

		return
	}

	source := strings.TrimSpace(row.Source)
	if source == "" || strings.Contains(source, schemeSeparator) {
		return
	}

	if declared, err := manifestIn(source); err == nil {
		withManifest(offer, declared)
	}
}

// Removal is httpapi.ModuleInstaller. It says, before a removal, which
// installed modules require the module, and which modules an install added
// for it would be left unused.
func (m *moduleInstaller) Removal(
	ctx context.Context, principal string, namespace string,
) (httpapi.ModuleRemoval, error) {
	who, err := authz.ParseSubject(principal)
	if err != nil {
		return httpapi.ModuleRemoval{}, err
	}

	found, err := m.declarationOf(namespace)
	if err != nil {
		return httpapi.ModuleRemoval{}, err
	}

	if err = m.mayConfigure(ctx, who, found.Ref.Kind()); err != nil {
		return httpapi.ModuleRemoval{}, err
	}

	doc, err := resource.Load(resource.Paths{File: m.configPath, Overlay: m.core.Overlay})
	if err != nil {
		return httpapi.ModuleRemoval{}, installFault(namespace, err)
	}

	answer := httpapi.ModuleRemoval{RequiredBy: []httpapi.ModuleDependant{}, Unused: []httpapi.ModuleUnused{}}

	for _, dependant := range resource.RequiredBy(resource.Without(doc.Instances, found.Ref), namespace) {
		for _, dependency := range dependant.Requires {
			if dependency.Module == namespace {
				answer.RequiredBy = append(answer.RequiredBy, httpapi.ModuleDependant{
					Instance: dependant.Instance.String(), Module: dependant.Namespace, Version: dependency.Version,
					Enabled: dependant.Enabled,
				})
			}
		}
	}

	if len(answer.RequiredBy) > 0 {
		return answer, nil
	}

	for _, unused := range resource.Unused(doc.Instances, found.Ref) {
		held, _ := unused.ModuleNamespace()
		row := httpapi.ModuleUnused{Instance: unused.Ref.String(), Module: held}

		if unused.Spec.Module != nil {
			row.Version, row.Title = unused.Spec.Module.Version, unused.Spec.Module.Title
		}

		answer.Unused = append(answer.Unused, row)
	}

	return answer, nil
}

// constraintFor is the constraint an install records: the one the request
// carries, or the root constraint of what the operator asked for.
func constraintFor(request moduleRequest, asked string) string {
	if request.constraint != "" || request.pulled {
		return request.constraint
	}

	return rootConstraint(asked, false)
}

// rootConstraint is what an install the operator asked for records as their
// constraint on the module: what they wrote after the @, as composer require
// records what it is given. A module an install added for another records
// none.
func rootConstraint(asked string, pulled bool) string {
	if pulled {
		return ""
	}

	return asked
}

// recordedRequirements is the manifest's requires list in the shape the
// declaration keeps it, and a requirement for each request header the
// manifest marks required, so the host holds the instance to its grant at
// every start. The manifest has already held each entry to exactly one
// subject, so nothing is decided again here.
func recordedRequirements(declared manifest.Manifest) []install.Requirement {
	out := make([]install.Requirement, 0, len(declared.Requires))
	for _, one := range declared.Requires {
		out = append(out, install.Requirement{
			Capability: one.Capability,
			Core:       one.Core,
			Module:     one.Module,
			Version:    one.Version,
		})
	}

	for _, header := range declared.Routes.Headers {
		if header.Required {
			out = append(out, install.Requirement{Header: header.Name})
		}
	}

	if len(out) == 0 {
		return nil
	}

	return out
}
