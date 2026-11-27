package app

import (
	"slices"

	"github.com/prusa3d/prusa-connect/internal/app/config/install"
	"github.com/prusa3d/prusa-connect/internal/app/resource"
	"github.com/prusa3d/prusa-connect/sdk/manifest"
)

// The file types a manifest claims, as the install records them and a plan
// reads them.

// fileTypeClaims is the file types a release claims, as an index row states
// them.
type fileTypeClaims struct {
	FileTypes []string `json:"file_types,omitempty"`
	ViewTypes []string `json:"view_types,omitempty"`
}

// fileTypesOf is the file types a manifest's file_analysis block names, and
// nil for a module that reads none for the core.
func fileTypesOf(declared *manifest.FileAnalysis) []string {
	if declared == nil {
		return nil
	}

	return declared.Types
}

// cameraTypesOf is the camera types a manifest offers, as the install records
// them (ADR-20261106-km2).
func cameraTypesOf(offered []manifest.CameraType) []install.CameraType {
	if len(offered) == 0 {
		return nil
	}

	out := make([]install.CameraType, 0, len(offered))
	for _, one := range offered {
		out = append(out, install.CameraType{Name: one.Name, Title: one.Title})
	}

	return out
}

// viewTypesOf is the file types a manifest's file-view entries draw for, each
// once, and resource.EveryFileType for an entry that names none.
func viewTypesOf(held manifest.Manifest) []string {
	if held.UI == nil {
		return nil
	}

	var out []string

	for _, entry := range held.UI.Contributes.Sections[manifest.SlotFileView] {
		types := entry.FileTypes
		if len(types) == 0 {
			types = []string{resource.EveryFileType}
		}

		for _, fileType := range types {
			if !slices.Contains(out, fileType) {
				out = append(out, fileType)
			}
		}
	}

	return out
}

// driverBlockOf is the driver block a driver module's install records, and
// nil for any other module.
func driverBlockOf(offered *manifest.Driver) *install.DriverBlock {
	if offered == nil {
		return nil
	}

	return &install.DriverBlock{Dialect: offered.Dialect, Baseline: offered.Baseline}
}
