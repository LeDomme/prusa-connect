package install_test

import (
	"encoding/json"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/prusa3d/prusa-connect/internal/app/config/install"
	"github.com/prusa3d/prusa-connect/sdk/connectorapi"
)

// A driver block keeps connectorapi's names in a YAML file as in the JSON
// overlay, so a limit survives both.
func TestADriverBlockReadsTheSameInBothFiles(t *testing.T) {
	t.Parallel()

	block := install.DriverBlock{
		Dialect:  json.RawMessage(`{"needs":"address"}`),
		Baseline: json.RawMessage(`{"core.command.set_speed#speed":{"flag":true,"limit":999,"has_limit":true}}`),
	}

	asYAML, err := yaml.Marshal(block)
	if err != nil {
		t.Fatal(err)
	}

	var fromYAML install.DriverBlock
	if err = yaml.Unmarshal(asYAML, &fromYAML); err != nil {
		t.Fatal(err)
	}

	asJSON, err := json.Marshal(block)
	if err != nil {
		t.Fatal(err)
	}

	var fromJSON install.DriverBlock
	if err = json.Unmarshal(asJSON, &fromJSON); err != nil {
		t.Fatal(err)
	}

	for name, read := range map[string]install.DriverBlock{"yaml": fromYAML, "json": fromJSON} {
		var baseline connectorapi.Set
		if err = json.Unmarshal(read.Baseline, &baseline); err != nil {
			t.Fatal(err)
		}

		if limit, ok := baseline.Limit("core.command.set_speed#speed"); !ok || limit != 999 {
			t.Errorf("%s lost the limit: %s", name, read.Baseline)
		}
	}
}
