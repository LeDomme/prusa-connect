package resourcehost

import (
	"log/slog"
	"testing"

	"github.com/prusa3d/prusa-connect/internal/app/resource"
	"github.com/prusa3d/prusa-connect/internal/kernel/ref"
)

// A planned change to a driver module waits while one of its printers holds a
// print, and only then (ADR-20261123-sat rule 14).
func TestADriverModuleChangeWaitsForItsPrints(t *testing.T) {
	t.Parallel()

	driver, err := ref.New(ref.KindDriver, "example")
	if err != nil {
		t.Fatal(err)
	}

	sink, err := ref.New(ref.KindSink, "queue")
	if err != nil {
		t.Fatal(err)
	}

	printing := map[ref.Ref][]string{driver: {"bench"}}

	h := &Host{
		cfg:     Config{Logger: slog.New(slog.DiscardHandler)},
		running: map[ref.Ref]*instance{driver: {}, sink: {}},
		done:    make(chan struct{}),
	}
	t.Cleanup(func() { close(h.done) })

	h.HoldDrivers(func(instance ref.Ref) []string { return printing[instance] })

	for _, tc := range []struct {
		name string
		step resource.Step
		held bool
	}{
		{"a stop while printing", resource.Step{Outcome: resource.OutcomeStop, Ref: driver}, true},
		{"a restart while printing", resource.Step{Outcome: resource.OutcomeRestart, Ref: driver}, true},
		{"a change that restarts nothing", resource.Step{Outcome: resource.OutcomeInPlace, Ref: driver}, false},
		{"another kind", resource.Step{Outcome: resource.OutcomeStop, Ref: sink}, false},
	} {
		if got := h.held(tc.step); got != tc.held {
			t.Errorf("%s: held %v", tc.name, got)
		}
	}

	printing[driver] = nil

	if h.held(resource.Step{Outcome: resource.OutcomeStop, Ref: driver}) {
		t.Error("a stop with no print running was held")
	}
}
