package install

import (
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"
)

// DriverBlock is a driver module's driver block as its manifest wrote it
// (ADR-20261123-sat rule 3). Both halves are JSON with connectorapi's names,
// such as has_limit, so a YAML file holds them in that same shape: they are
// read and written through JSON in both files.
type DriverBlock struct {
	// Dialect is a connectorapi.Dialect in JSON.
	Dialect json.RawMessage `json:"dialect" yaml:"dialect"`
	// Baseline is a connectorapi.Set in JSON.
	Baseline json.RawMessage `json:"baseline" yaml:"baseline"`
}

// driverJSON is DriverBlock with no methods, so encoding it does not recurse.
type driverJSON DriverBlock

// MarshalYAML writes the block as the tree its JSON makes.
func (d DriverBlock) MarshalYAML() (any, error) {
	raw, err := json.Marshal(driverJSON(d))
	if err != nil {
		return nil, fmt.Errorf("encode the driver block: %w", err)
	}

	var tree any
	if err = json.Unmarshal(raw, &tree); err != nil {
		return nil, fmt.Errorf("encode the driver block: %w", err)
	}

	return tree, nil
}

// UnmarshalYAML reads the block through its JSON.
func (d *DriverBlock) UnmarshalYAML(node *yaml.Node) error {
	var tree any
	if err := node.Decode(&tree); err != nil {
		return fmt.Errorf("read the driver block: %w", err)
	}

	raw, err := json.Marshal(tree)
	if err != nil {
		return fmt.Errorf("read the driver block: %w", err)
	}

	var held driverJSON
	if err = json.Unmarshal(raw, &held); err != nil {
		return fmt.Errorf("read the driver block: %w", err)
	}

	*d = DriverBlock(held)

	return nil
}
