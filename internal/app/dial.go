package app

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/prusa3d/prusa-connect/internal/app/resource"
	"github.com/prusa3d/prusa-connect/internal/extension/supervisor"
	"github.com/prusa3d/prusa-connect/sdk/connectorapi"
)

// dialCap is the ceiling on the wait between two dials of one endpoint.
//
// The ladder is full jitter, one second to sixty, with no attempt ceiling, and
// it resets only on proof of health. The cap is a minute and not the five
// supervisor.DefaultBackoffCap gives a remote dependency, because a printer's
// disconnection is its steady state: a machine switched on at eight o'clock
// must not wait five minutes to be seen. For the same reason there is no
// circuit breaker here.
const dialCap = time.Minute

// dialledConnector is one built driver the host reaches, rather than one that
// reaches the host. It is what mountOne hands the dial supervisor.
type dialledConnector struct {
	// ref is the instance name under core.connectors, which is what an endpoint
	// names to say which dialect dials it.
	ref connectorapi.Ref
	// driver is the dialling half.
	driver connectorapi.Outbound
	// baseline is what that driver's code can do at all, for any device.
	baseline connectorapi.Set
	// arrivals is the host's half of the exchange: the same surface an inbound
	// dialect is handed, so a dialled printer is admitted, registered and
	// supervised exactly as one that dialled in.
	arrivals connectorapi.Arrivals
}

// dials owns one goroutine per enabled endpoint: connect, admit, arrive, wait
// for the host to release the session, and dial again.
//
// The list it works from is the machine-owned overlay's, so adding a printer is
// a write and not a restart. Every decision about which loops run is taken on
// the one goroutine run owns, and the endpoint list reaches it as a message, so
// there is no lock over the running set and no way for two syncs to interleave.
type dials struct {
	log        *slog.Logger
	connectors map[string]dialledConnector
	// modules finds a driver module an endpoint names, when no connector of
	// that name is declared (ADR-20261123-sat rule 11). Nil finds none.
	modules func(name string) (dialledConnector, bool)
	backoff supervisor.BackoffPolicy

	// changes carries the whole endpoint list. It is depth one and the send
	// replaces what is waiting: a sync that has not been read yet is a list
	// that is already out of date.
	changes chan []resource.Endpoint
	quit    chan struct{}
	done    chan struct{}
	closing sync.Once
}

// dialling is one endpoint's loop as the supervisor holds it.
type dialling struct {
	endpoint resource.Endpoint
	cancel   context.CancelFunc
	done     chan struct{}
}

// startDialling starts the supervisor and returns it. Nothing is dialled until
// an endpoint list arrives through SyncEndpoints, which is what the resource
// store calls once the store and the connectors both exist.
func startDialling(
	ctx context.Context, log *slog.Logger, connectors []dialledConnector,
	modules func(name string) (dialledConnector, bool),
) *dials {
	d := newDials(log, connectors, dialLadder())
	d.modules = modules
	d.start(ctx)

	return d
}

// dialLadder is the ladder every endpoint is walked up, and the one place the
// three numbers in it are written.
func dialLadder() supervisor.BackoffPolicy {
	return supervisor.BackoffPolicy{
		Base:   supervisor.DefaultBackoffBase,
		Cap:    dialCap,
		Spread: supervisor.DefaultStartSpread,
	}
}

// newDials builds the supervisor without starting it. The ladder is an argument
// so a case can walk a fast one: the rungs themselves are supervisor's to
// measure, and a case that waited out the real ones would be measuring them
// again in twenty seconds.
func newDials(
	log *slog.Logger, connectors []dialledConnector, backoff supervisor.BackoffPolicy,
) *dials {
	indexed := make(map[string]dialledConnector, len(connectors))
	for _, connector := range connectors {
		indexed[connector.ref.String()] = connector
	}

	return &dials{
		log:        log,
		connectors: indexed,
		backoff:    backoff,
		changes:    make(chan []resource.Endpoint, 1),
		quit:       make(chan struct{}),
		done:       make(chan struct{}),
	}
}

// start runs the supervisor until ctx ends or stop is called.
func (d *dials) start(ctx context.Context) { go d.run(ctx) }

// SyncEndpoints takes the endpoint list the overlay holds. It never blocks:
// the channel holds one list and a newer one replaces an unread older one.
func (d *dials) SyncEndpoints(_ context.Context, endpoints []resource.Endpoint) {
	// Whatever is waiting is dropped first. An unread list is one that is
	// already out of date, and this call is made while the resource store holds
	// its lock, so it must not wait on a goroutine that could be mid-dial.
	select {
	case <-d.changes:
	default:
	}

	select {
	case d.changes <- endpoints:
	case <-d.done:
	}
}

// stop ends every dial loop and waits for each to return. It runs before the
// device plane closes, so a loop cannot dial a printer into a plane that is
// already tearing its sessions down.
func (d *dials) stop() {
	d.closing.Do(func() { close(d.quit) })

	<-d.done
}

// run owns the set of loops. It is the only goroutine that reads or writes it.
func (d *dials) run(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	running := map[string]dialling{}

	defer func() {
		cancel()

		for _, loop := range running {
			<-loop.done
		}

		close(d.done)
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-d.quit:
			return
		case endpoints := <-d.changes:
			d.apply(ctx, endpoints, running)
		}
	}
}

// apply starts a loop for every endpoint that should have one and stops the
// rest. An endpoint whose declaration moved is stopped and started again: the
// address is what the loop dials, and a running loop cannot be told a new one.
//
// A replaced loop is waited for before its successor starts, so one endpoint is
// never dialled twice at once.
func (d *dials) apply(ctx context.Context, endpoints []resource.Endpoint, running map[string]dialling) {
	wanted := make(map[string]resource.Endpoint, len(endpoints))

	for _, endpoint := range endpoints {
		if endpoint.Enabled {
			wanted[endpoint.Name] = endpoint
		}
	}

	for name, loop := range running {
		declared, keep := wanted[name]
		if keep && declared.Same(loop.endpoint) {
			continue
		}

		loop.cancel()
		<-loop.done
		delete(running, name)

		d.log.InfoContext(ctx, "an endpoint is no longer dialled as it was",
			"endpoint", name, "address", loop.endpoint.Address)
	}

	for name, endpoint := range wanted {
		if _, already := running[name]; already {
			continue
		}

		connector, known := d.connectors[endpoint.Connector]
		if !known && d.modules != nil {
			connector, known = d.modules(endpoint.Connector)
		}

		if !known {
			// Not a startup failure. The endpoint list survives a configuration
			// change, so an address naming a connector this deployment has
			// stopped declaring is a row somebody has to fix. The server still
			// serves the printers it can reach.
			d.log.WarnContext(ctx, "an endpoint names a connector this build does not dial with, "+
				"so nothing dials it", "endpoint", name, "connector", endpoint.Connector)

			continue
		}

		loopCtx, cancel := context.WithCancel(ctx)
		loop := dialling{endpoint: endpoint, cancel: cancel, done: make(chan struct{})}
		running[name] = loop

		go func() {
			defer close(loop.done)

			d.dial(loopCtx, endpoint, connector)
		}()
	}
}

// dial runs one endpoint for as long as it is declared.
//
// The ladder is walked here and nowhere else. attempt is reset when the host
// takes a session, which is the proof of health: a printer that has served and
// then gone is dialled again within a second, and one that has never answered
// climbs to a minute and stays there.
func (d *dials) dial(ctx context.Context, endpoint resource.Endpoint, connector dialledConnector) {
	log := d.log.With(
		"endpoint", endpoint.Name,
		"connector", endpoint.Connector,
		"address", endpoint.Address,
	)

	// Scattered so a deployment with four hundred addresses does not dial every
	// one of them on the same millisecond.
	if !supervisor.SleepFor(ctx, d.backoff.Scatter()) {
		return
	}

	attempt := 0

	for ctx.Err() == nil {
		if d.once(ctx, log, endpoint, connector, attempt) {
			attempt = 0
		}

		attempt++

		if !supervisor.SleepFor(ctx, d.backoff.Delay(attempt)) {
			return
		}
	}
}

// once dials the endpoint and stays until the host releases the session it
// handed over. It reports whether the host took one, which is the only proof of
// health the ladder resets on.
//
// profile is the driver's own baseline and not a resolved tier. The host cannot
// resolve one before it has an identity, and the identity does not exist until
// Connect has run. Nothing is lost by it: capability.Wrap in
// devices.arrived is what a command is validated against, so the tier the host
// resolved is enforced whichever direction the connection was opened in. The
// driver's own narrowing is a second guard, and against its own baseline it is
// a no-op.
func (d *dials) once(
	ctx context.Context,
	log *slog.Logger,
	endpoint resource.Endpoint,
	connector dialledConnector,
	attempt int,
) bool {
	// The dial has a bounded timeout as well as a jittered backoff. The
	// deadline covers the call and never the session, which is
	// what connectorapi.Outbound's own contract already says about the state
	// stream outliving Connect. Without it, a driver that never returns from a
	// handshake holds this endpoint for the life of the process.
	// It is released as soon as Connect returns rather than deferred, so a
	// driver that kept it loses it at the handover and not thirty seconds into
	// a working session. That is the same bound Arrivals.Arrived puts on the
	// inbound side, where the context ends when the handshake response is
	// written.
	dialCtx, done := context.WithTimeout(ctx, supervisor.DefaultStartTimeout)

	session, err := connector.driver.Connect(dialCtx, connectorapi.Endpoint{
		Address: endpoint.Address,
		Options: endpoint.Options,
	}, connector.baseline.Clone())

	done()

	if err != nil {
		// Loud once and quiet after that. A printer that is switched off is
		// dialled for ever, and a line per attempt at the top of the ladder is
		// a log an operator stops reading.
		if attempt == 0 {
			log.WarnContext(ctx, "dialling a printer failed", "error", err.Error())
		} else {
			log.DebugContext(ctx, "dialling a printer failed again",
				"attempt", attempt, "error", err.Error())
		}

		return false
	}

	watched, released := watchSession(session)
	identity := session.Identity()

	if _, err = connector.arrivals.Admit(ctx, identity); err != nil {
		log.WarnContext(ctx, "a dialled printer was not admitted", "error", err.Error())
		closeDialled(log, session)

		return false
	}

	if err = connector.arrivals.Arrived(ctx, watched); err != nil {
		// A loop that is stopping, or whose endpoint changed, ends the arrival
		// itself. That is not a printer the host refused, so it is not a warning.
		if ctx.Err() == nil {
			log.WarnContext(ctx, "the host did not take a dialled printer's session", "error", err.Error())
		} else {
			log.DebugContext(ctx, "the host did not take a dialled printer's session while its loop stopped",
				"error", err.Error())
		}

		closeDialled(log, session)

		return false
	}

	log.InfoContext(ctx, "a dialled printer arrived",
		"printer", dialledID(connector.arrivals, identity), "model", identity.Model,
		"serial", serialOf(identity), "unit_uid", identity.UnitUID)

	select {
	case <-released:
		log.InfoContext(ctx, "a dialled printer's session ended, so the address is dialled again")
	case <-ctx.Done():
	}

	return true
}

// dialledID is the id the host bound this printer to, for the log line that
// says it arrived.
//
// It asks the host and computes nothing, because an id is minted and bound in
// one place (deviceIdentities) and a second derivation here would be a log
// naming a device nothing else does. Admit has already run, so the
// binding exists; a host that mints no id logs the empty string, which is what
// such a host can honestly say the printer is called.
func dialledID(host connectorapi.Arrivals, identity connectorapi.Identity) string {
	named, mints := host.(interface {
		DeviceID(connectorapi.Identity) string
	})
	if !mints {
		return ""
	}

	return named.DeviceID(identity)
}

// closeDialled releases a session the host did not take, which is what
// connectorapi.Arrivals obliges whoever built it to do.
func closeDialled(log *slog.Logger, session connectorapi.Session) {
	if err := session.Close(); err != nil {
		log.Error("closing a dialled session the host did not take", "error", err.Error())
	}
}

// watchedSession is the session the dial loop handed the host, carrying the one
// signal the loop needs back: the host has released it, so the address is free
// to be dialled again.
//
// Close is where the signal is, because that is the whole of the host's half of
// the contract. devices.pump closes the session when the driver closes its
// state channel, and devices.Close closes every open session when the server
// stops, so there is no ending that does not reach here.
//
// The host finds the driver's optional interfaces, such as its files, its probe
// and its link counters, with capability.As, which calls Unwrap. Do not give
// this type a Dropped or a Refused method: every dialled printer would then
// report a driver that counts, and a driver that counts nothing would read as
// zero (see devicelink.go).
type watchedSession struct {
	connectorapi.Session

	release sync.Once
	done    chan struct{}
}

// Close releases the driver's session and tells the dial loop it may dial
// again. It is safe to call more than once, which connectorapi.Session
// promises.
func (s *watchedSession) Close() error {
	s.release.Do(func() { close(s.done) })

	return s.Session.Close()
}

// Unwrap returns the driver's own session. capability.As calls it to find the
// driver's optional interfaces.
func (s *watchedSession) Unwrap() connectorapi.Session { return s.Session }

// watchSession wraps a driver's session so the dial loop learns when the host
// has finished with it.
func watchSession(inner connectorapi.Session) (connectorapi.Session, <-chan struct{}) {
	watched := &watchedSession{Session: inner, done: make(chan struct{})}

	return watched, watched.done
}
