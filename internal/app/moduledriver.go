package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/prusa3d/prusa-connect/internal/app/resource"
	"github.com/prusa3d/prusa-connect/internal/app/resourcehost"
	"github.com/prusa3d/prusa-connect/internal/authz"
	"github.com/prusa3d/prusa-connect/internal/extension/supervisor"
	"github.com/prusa3d/prusa-connect/internal/kernel/ref"
	"github.com/prusa3d/prusa-connect/internal/printer/capability"
	"github.com/prusa3d/prusa-connect/internal/printer/profile"
	"github.com/prusa3d/prusa-connect/sdk/connectorapi"
	"github.com/prusa3d/prusa-connect/sdk/driverwire"
)

// errDriverModuleDown reports an endpoint whose driver module is declared and
// not serving, so nothing can dial it until the module runs.
var errDriverModuleDown = errors.New("the driver module is not serving")

// driverPeer is what a running driver module's target hands out: its session
// with the host, and the channel that closes when that session ends. Only the
// resource host's service child has it.
type driverPeer interface {
	DriverPeer() (driverwire.Peer, <-chan struct{})
}

// moduleDriver is a printer driver that runs in a module process
// (ADR-20261123-sat). The host reaches it as it reaches a builtin driver, and
// the dial supervisor cannot tell the two apart.
type moduleDriver struct {
	instance ref.Ref
	baseline connectorapi.Set
	host     *resourcehost.Host
	client   *driverwire.Client
}

func (m *moduleDriver) Baseline() connectorapi.Set { return m.baseline.Clone() }

// Connect opens a session on the module's running process. The session ends
// with that process, and the dial loop then dials again.
func (m *moduleDriver) Connect(
	ctx context.Context, endpoint connectorapi.Endpoint, profile connectorapi.Set,
) (connectorapi.Session, error) {
	runner, declared := m.host.Supervising(ref.KindDriver)[m.instance]
	if !declared {
		return nil, fmt.Errorf("%w: %s", errDriverModuleDown, m.instance)
	}

	type reached struct {
		peer driverwire.Peer
		gone <-chan struct{}
	}

	// Only the lookup runs under supervision. A connect can wait seconds on a
	// printer that is booting, which is longer than a supervised call may take.
	running, err := supervisor.Call(ctx, runner, "connect", func(_ context.Context, target any) (reached, error) {
		child, ok := target.(driverPeer)
		if !ok {
			return reached{}, fmt.Errorf("%w: %s runs no process", errDriverModuleDown, m.instance)
		}

		peer, gone := child.DriverPeer()

		return reached{peer: peer, gone: gone}, nil
	})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", m.instance, err)
	}

	session, err := m.client.Connect(ctx, running.peer, running.gone, endpoint, profile)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", m.instance, err)
	}

	return session, nil
}

// moduleDrivers finds the driver module an endpoint names, from the
// declarations as they stand when the endpoint is dialled. A module installed
// while the server runs is dialled with no restart.
type moduleDrivers struct {
	store *resource.Store
	host  *resourcehost.Host
	plane *devices
	log   *slog.Logger
}

// lookup builds the dialled half of the driver module declared under name,
// and answers false for a name that declares none.
func (m moduleDrivers) lookup(name string) (dialledConnector, bool) {
	instance, err := ref.New(ref.KindDriver, name)
	if err != nil {
		return dialledConnector{}, false
	}

	for _, declared := range m.store.Document().Instances {
		if declared.Ref != instance || declared.Spec.Module == nil || declared.Spec.Module.Driver == nil {
			continue
		}

		recorded := declared.Spec.Module.Driver

		baseline, unread := driverOf(recorded.Dialect, recorded.Baseline)
		if unread != nil {
			m.log.Warn("a driver module's recorded driver block does not read, so nothing dials through it",
				"resource", instance.String(), "error", unread.Error())

			return dialledConnector{}, false
		}

		connector := connectorapi.Ref(name)

		// A module driver ships no profile registry, so its whole baseline
		// applies (ADR-20261123-sat rule 13).
		resolver := capability.NewResolver(profile.Registry{}, m.log)

		return dialledConnector{
			ref: connector,
			driver: &moduleDriver{
				instance: instance,
				baseline: baseline,
				host:     m.host,
				client:   m.host.DriverClient(instance),
			},
			baseline: baseline,
			arrivals: m.plane.Arrivals(connector, baseline, resolver, authz.DeviceKindPrinter),
		}, true
	}

	return dialledConnector{}, false
}

// printingOn names the printers dialled through connector that hold a print:
// printing, paused, or waiting on a person mid-print. The resource host holds
// a driver module's planned stop while it names any (ADR-20261123-sat rule 14).
func (d *devices) printingOn(instance ref.Ref) []string {
	d.mu.Lock()

	entries := make([]*device, 0, len(d.entries))
	for _, entry := range d.entries {
		if entry.driver == instance.ID() {
			entries = append(entries, entry)
		}
	}

	d.mu.Unlock()

	var printing []string

	for _, entry := range entries {
		if slices.Contains(jobRunningStates, entry.stateText()) {
			printing = append(printing, entry.id)
		}
	}

	return printing
}

// errBadDriverBlock refuses a driver module whose driver block the host cannot
// dial with.
var errBadDriverBlock = errors.New("the driver block of the module is not usable")

// driverOf decodes and checks a driver module's driver block, and returns its
// baseline: a dialect that
// an endpoint dials at an address, and a baseline with at least one verb
// (ADR-20261123-sat rules 3 and 4). The install refuses a block it fails, and
// the dial lookup reads the same block.
func driverOf(rawDialect, rawBaseline json.RawMessage) (connectorapi.Set, error) {
	var (
		dialect  connectorapi.Dialect
		baseline connectorapi.Set
	)

	if err := strictJSON(rawDialect, &dialect); err != nil {
		return nil, fmt.Errorf("%w: driver.dialect: %w", errBadDriverBlock, err)
	}

	if err := strictJSON(rawBaseline, &baseline); err != nil {
		return nil, fmt.Errorf("%w: driver.baseline: %w", errBadDriverBlock, err)
	}

	if err := connectorapi.ValidateDialect(dialect); err != nil {
		return nil, fmt.Errorf("%w: driver.dialect: %w", errBadDriverBlock, err)
	}

	if dialect.Needs != connectorapi.NeedsAddress {
		return nil, fmt.Errorf("%w: driver.dialect.needs is %q, and the host dials a module "+
			"driver at an address", errBadDriverBlock, dialect.Needs)
	}

	if len(baseline.Verbs()) == 0 {
		return nil, fmt.Errorf("%w: driver.baseline declares no verb", errBadDriverBlock)
	}

	return baseline, nil
}

// strictJSON decodes raw into out and refuses a key out does not have.
func strictJSON(raw []byte, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()

	return decoder.Decode(out) //nolint:wrapcheck // the caller names the half it read
}
