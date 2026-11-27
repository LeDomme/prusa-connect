package resourcehost

import (
	"context"
	"time"

	"github.com/prusa3d/prusa-connect/internal/app/resource"
	"github.com/prusa3d/prusa-connect/internal/kernel/ref"
)

// holdTick is how often a held step looks again at the module's printers.
const holdTick = 15 * time.Second

// holdTimeout bounds the reconcile that carries a released step out.
const holdTimeout = time.Minute

// HoldDrivers makes a planned stop of a driver module wait while printing names
// one of its printers (ADR-20261123-sat rule 14). A print streamed by the
// module ends with its process, so an upgrade, a switch-off or a settings
// change waits for the print instead. A crash, the watchdog and the memory
// kill never come through a plan, so nothing here holds them.
func (h *Host) HoldDrivers(printing func(instance ref.Ref) []string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.printing = printing
}

// held reports whether step waits for a print to end, and starts the wait.
// A newer step for the same instance replaces one that is waiting.
func (h *Host) held(step resource.Step) bool {
	if step.Ref.Kind() != ref.KindDriver {
		return false
	}

	h.mu.Lock()
	printing := h.printing
	_, running := h.running[step.Ref]
	delete(h.holds, step.Ref)
	h.mu.Unlock()

	switch {
	case printing == nil, !running:
		return false
	case step.Outcome != resource.OutcomeStop && step.Outcome != resource.OutcomeRestart &&
		step.Outcome != resource.OutcomeReplace:
		return false
	}

	printers := printing(step.Ref)
	if len(printers) == 0 {
		return false
	}

	waiting := &step

	h.mu.Lock()
	if h.holds == nil {
		h.holds = map[ref.Ref]*resource.Step{}
	}

	h.holds[step.Ref] = waiting
	h.mu.Unlock()

	h.cfg.Logger.Warn("a driver module's printers hold a print, so the module is changed when they finish",
		"resource", step.Ref.String(), "outcome", step.Outcome.String(), "printers", printers)

	go h.release(waiting)

	return true
}

// release carries a held step out once the module's printers hold no print,
// unless a newer step replaced it first.
func (h *Host) release(waiting *resource.Step) {
	ticker := time.NewTicker(holdTick)
	defer ticker.Stop()

	for {
		select {
		case <-h.done:
			return
		case <-ticker.C:
		}

		h.mu.Lock()
		current := h.holds[waiting.Ref]
		printing := h.printing
		h.mu.Unlock()

		if current != waiting {
			return
		}

		if len(printing(waiting.Ref)) > 0 {
			continue
		}

		h.mu.Lock()
		delete(h.holds, waiting.Ref)
		h.mu.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), holdTimeout)
		err := h.Reconcile(ctx, resource.Plan{Steps: []resource.Step{*waiting}})

		cancel()

		if err != nil {
			h.cfg.Logger.Warn("a driver module's held change failed once its printers finished",
				"resource", waiting.Ref.String(), "error", err.Error())
		}

		return
	}
}
