package manifest

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// KindDriver is the kind of a module that serves a printer driver
// (ADR-20261123-sat).
const KindDriver = "driver"

// Driver is the driver block of a driver module: what the host needs of the
// driver before its process runs. The host reads it at install and never from
// the process (ADR-20261123-sat rule 3).
//
// Both halves are JSON in the shape sdk/connectorapi defines, and they stay
// JSON here. The host decodes and checks them, so a package that reads a
// manifest does not reach the printer contract.
type Driver struct {
	// Dialect is a connectorapi.Dialect: what an endpoint needs, the models,
	// the admission and the state set.
	Dialect json.RawMessage `json:"dialect"`
	// Baseline is a connectorapi.Set: what the driver can do for any printer.
	Baseline json.RawMessage `json:"baseline"`
}

// readDriver reads the driver block, and answers nil for a module that has
// none.
func readDriver(tree map[string]any) (*Driver, error) {
	block, declared, err := node(tree, "", "driver")
	if err != nil || !declared {
		return nil, err
	}

	raw, err := json.Marshal(block)
	if err != nil {
		return nil, fmt.Errorf("%w: driver: %w", ErrInvalid, err)
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()

	var held Driver
	if err = decoder.Decode(&held); err != nil {
		return nil, fmt.Errorf("%w: driver: %w", ErrInvalid, err)
	}

	for key, half := range map[string]json.RawMessage{"dialect": held.Dialect, "baseline": held.Baseline} {
		if !bytes.HasPrefix(bytes.TrimSpace(half), []byte("{")) {
			return nil, fmt.Errorf("%w: driver.%s is missing, expected a mapping", ErrInvalid, key)
		}
	}

	return &held, nil
}

// holdDriver keeps the driver block and the kind in step: a driver module runs
// a process and says what its driver is, and only a driver module does.
func holdDriver(held Manifest) error {
	switch {
	case held.Driver != nil && held.Kind != KindDriver:
		return fmt.Errorf("%w: driver is set and kind is %q, and only a module of kind %s serves a driver",
			ErrInvalid, held.Kind, KindDriver)
	case held.Driver != nil && held.ContractMajor == 0:
		return fmt.Errorf("%w: driver is set and contract_major is not, and a driver runs in the "+
			"module's process", ErrInvalid)
	case held.Kind == KindDriver && held.ContractMajor > 0 && held.Driver == nil:
		return fmt.Errorf("%w: kind is %s and driver is not set, so the host cannot know what the "+
			"driver does before it runs", ErrInvalid, KindDriver)
	}

	return nil
}
