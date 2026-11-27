package manifest_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/prusa3d/prusa-connect/sdk/manifest"
)

// goodDriver is a driver block the parser takes.
const goodDriver = `{"dialect": {"needs": "address"},
  "baseline": {"core.command.send_state_info": {"flag": true}}}`

// withDriver is the full manifest as a driver module with the given block.
func withDriver(block string) string {
	document := strings.Replace(full, `"kind": "module"`, `"kind": "driver"`, 1)

	return strings.Replace(document, `"lifecycle": "beta",`, `"lifecycle": "beta", "driver": `+block+`,`, 1)
}

// ADR-20261123-sat rule 3: the host reads what a driver is from the manifest.
func TestADriverModuleSaysWhatItsDriverIs(t *testing.T) {
	t.Parallel()

	held, err := manifest.Parse("module.yaml", []byte(withDriver(goodDriver)), decode)
	if err != nil {
		t.Fatalf("a driver block was refused: %v", err)
	}

	if held.Driver == nil || !strings.Contains(string(held.Driver.Dialect), `"address"`) ||
		!strings.Contains(string(held.Driver.Baseline), "send_state_info") {
		t.Fatalf("read %+v", held.Driver)
	}
}

func TestADriverBlockIsHeldToItsShape(t *testing.T) {
	t.Parallel()

	for name, block := range map[string]string{
		"no baseline":            `{"dialect": {"needs": "address"}}`,
		"no dialect":             `{"baseline": {}}`,
		"a key it does not read": `{"dialect": {"needs": "address"}, "baseline": {}, "states": {}}`,
		"a list for a mapping":   `{"dialect": [], "baseline": {}}`,
	} {
		_, err := manifest.Parse("module.yaml", []byte(withDriver(block)), decode)
		if !errors.Is(err, manifest.ErrInvalid) {
			t.Errorf("%s: got %v", name, err)
		}
	}
}

// Only a driver module has a driver block, and a driver module with a process
// always has one.
func TestTheDriverBlockAndTheKindAgree(t *testing.T) {
	t.Parallel()

	for name, document := range map[string]string{
		"a block on another kind": strings.Replace(withDriver(goodDriver), `"kind": "driver"`, `"kind": "module"`, 1),
		"a driver with no block":  strings.Replace(full, `"kind": "module"`, `"kind": "driver"`, 1),
		"a block with no process": strings.Replace(withDriver(goodDriver), `"contract_major": 1,`, ``, 1),
	} {
		_, err := manifest.Parse("module.yaml", []byte(document), decode)
		if !errors.Is(err, manifest.ErrInvalid) {
			t.Errorf("%s: got %v", name, err)
		}
	}
}
