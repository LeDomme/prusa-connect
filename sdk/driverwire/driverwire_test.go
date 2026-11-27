package driverwire_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/prusa3d/prusa-connect/sdk/connectorapi"
	"github.com/prusa3d/prusa-connect/sdk/connectorapi/connectorapitest"
	"github.com/prusa3d/prusa-connect/sdk/driverwire"
	"github.com/prusa3d/prusa-connect/sdk/fault"
	"github.com/prusa3d/prusa-connect/sdk/wirerpc"
)

const (
	verbInfo   = connectorapi.Capability("core.command.send_state_info")
	verbUpload = connectorapi.VerbFileUpload
)

// The driver contract holds for a driver reached through the wire, so the
// host can run a module driver as it runs a builtin one (ADR-20261123-sat
// rules 1 and 16).
func TestADriverThroughTheWireKeepsTheContract(t *testing.T) {
	t.Parallel()

	connectorapitest.RunDriverContract(t, func(t *testing.T) connectorapitest.Subject {
		return connectorapitest.Subject{
			Driver:      overTheWire(t, &memDriver{}),
			Endpoint:    connectorapi.Endpoint{Address: "mem"},
			Connectable: true,
			SafeVerb:    verbInfo,
		}
	})
}

// A fault the driver raises reaches the host with its code and params.
func TestARefusalKeepsItsFault(t *testing.T) {
	t.Parallel()

	session := connect(t, overTheWire(t, &memDriver{}))

	_, err := session.Do(t.Context(), connectorapi.Command{Verb: "core.command.pause_print"})
	if !fault.HasCode(err, connectorapi.CapabilityNotSupported.Code()) {
		t.Fatalf("the refusal arrived as %v", err)
	}
}

// Patches cross in order, and a session the module ends closes the host's
// state channel.
func TestPatchesCrossAndAnEndedSessionCloses(t *testing.T) {
	t.Parallel()

	driver := &memDriver{}
	session := connect(t, overTheWire(t, driver))

	first := <-session.State()
	if first.Path != "/state" {
		t.Fatalf("the first patch is %+v", first)
	}

	driver.last().end()

	deadline := time.After(5 * time.Second)

	for {
		select {
		case _, open := <-session.State():
			if !open {
				return
			}
		case <-deadline:
			t.Fatal("the host's state channel stayed open after the module ended the session")
		}
	}
}

// An upload resumed at an offset reads from there, and its progress reaches
// the host.
func TestAnUploadReadsFromItsOffset(t *testing.T) {
	t.Parallel()

	driver := &memDriver{offset: 3}
	session := connect(t, overTheWire(t, driver))

	transfers, ok := session.(connectorapi.Transfers)
	if !ok {
		t.Fatal("the session lost its transfers part on the wire")
	}

	var progressed sync.WaitGroup

	progressed.Add(1)

	once := sync.Once{}

	err := transfers.Send(t.Context(), connectorapi.Upload{
		Path:     "local/part.gcode",
		Content:  memContent("0123456789"),
		Progress: func(connectorapi.Progress) { once.Do(progressed.Done) },
	})
	if err != nil {
		t.Fatal(err)
	}

	progressed.Wait()

	if got := driver.last().received(); got != "3456789" {
		t.Fatalf("the driver read %q", got)
	}
}

// A session whose module goes away ends.
func TestASessionEndsWithItsModule(t *testing.T) {
	t.Parallel()

	gone := make(chan struct{})
	driver := overTheWireGone(t, &memDriver{}, gone)
	session := connect(t, driver)

	close(gone)

	for range session.State() {
		// drain until the channel closes
	}
}

func connect(t *testing.T, driver connectorapi.Driver) connectorapi.Session {
	t.Helper()

	outbound, _ := driver.(connectorapi.Outbound)

	session, err := outbound.Connect(t.Context(), connectorapi.Endpoint{Address: "mem"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = session.Close() })

	return session
}

// remote is a driver as the host sees one in a module: the baseline from the
// manifest, and every session through the wire.
type remote struct {
	baseline connectorapi.Set
	client   *driverwire.Client
	peer     driverwire.Peer
	gone     <-chan struct{}
}

func (r *remote) Baseline() connectorapi.Set { return r.baseline.Clone() }

func (r *remote) Connect(
	ctx context.Context, endpoint connectorapi.Endpoint, profile connectorapi.Set,
) (connectorapi.Session, error) {
	return r.client.Connect(ctx, r.peer, r.gone, endpoint, profile)
}

func overTheWire(t *testing.T, driver *memDriver) connectorapi.Driver {
	t.Helper()

	return overTheWireGone(t, driver, nil)
}

// overTheWireGone runs driver in a module at one end of a pipe and returns the
// host's view of it from the other end.
func overTheWireGone(t *testing.T, driver *memDriver, gone <-chan struct{}) connectorapi.Driver {
	t.Helper()

	hostEnd, childEnd := duplex(t)
	hostMethods, childMethods := wirerpc.Methods{}, wirerpc.Methods{}

	host, err := wirerpc.NewConn(hostEnd, wirerpc.Options{Role: wirerpc.RoleHost, Methods: hostMethods})
	if err != nil {
		t.Fatal(err)
	}

	child, err := wirerpc.NewConn(childEnd, wirerpc.Options{Role: wirerpc.RoleChild, Methods: childMethods})
	if err != nil {
		t.Fatal(err)
	}

	client := driverwire.NewClient()
	module := driverwire.NewModule(child, nil)

	if err = module.Use(driver); err != nil {
		t.Fatal(err)
	}

	maps.Copy(hostMethods, client.Handlers())
	maps.Copy(childMethods, module.Handlers())

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		module.Close()
		cancel()
	})

	go func() { _ = host.Serve(ctx) }()
	go func() { _ = child.Serve(ctx) }()

	if gone == nil {
		gone = host.Done()
	}

	return &remote{baseline: driver.Baseline(), client: client, peer: host, gone: gone}
}

// memDriver is a printer that lives in memory.
type memDriver struct {
	offset int64

	mu       sync.Mutex
	sessions []*memSession
}

func (*memDriver) Baseline() connectorapi.Set {
	return connectorapi.Set{verbInfo: {Flag: true}, verbUpload: {Flag: true}}
}

func (d *memDriver) Connect(
	_ context.Context, _ connectorapi.Endpoint, profile connectorapi.Set,
) (connectorapi.Session, error) {
	s := &memSession{
		caps: d.Baseline().Intersect(profile), offset: d.offset,
		states: make(chan connectorapi.StatePatch, 1), done: make(chan struct{}),
	}
	s.states <- connectorapi.StatePatch{Path: "/state", Value: json.RawMessage(`"IDLE"`)}

	d.mu.Lock()
	d.sessions = append(d.sessions, s)
	d.mu.Unlock()

	return s, nil
}

func (d *memDriver) last() *memSession {
	d.mu.Lock()
	defer d.mu.Unlock()

	return d.sessions[len(d.sessions)-1]
}

type memSession struct {
	caps   connectorapi.Set
	offset int64
	states chan connectorapi.StatePatch
	done   chan struct{}
	once   sync.Once

	mu  sync.Mutex
	got bytes.Buffer
}

func (*memSession) Identity() connectorapi.Identity {
	return connectorapi.Identity{Vendor: "Prusa", Model: "Example", Serial: "EX0000", UnitUID: "EX0000"}
}

func (s *memSession) Capabilities() (connectorapi.Set, uint64) { return s.caps.Clone(), 1 }

func (s *memSession) Do(ctx context.Context, cmd connectorapi.Command) (connectorapi.Result, error) {
	if err := ctx.Err(); err != nil {
		return connectorapi.Result{}, err
	}

	if !s.caps.Has(cmd.Verb) {
		return connectorapi.Result{}, connectorapi.CapabilityNotSupported.New("not here")
	}

	return connectorapi.Result{OK: true}, nil
}

func (s *memSession) State() <-chan connectorapi.StatePatch { return s.states }

func (s *memSession) end() { s.once.Do(func() { close(s.states) }) }

func (s *memSession) Close() error {
	s.end()

	return nil
}

func (s *memSession) Send(ctx context.Context, up connectorapi.Upload) error {
	r, err := up.Content.Open(ctx, s.offset)
	if err != nil {
		return err
	}

	defer func() { _ = r.Close() }()

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err = io.Copy(&s.got, r); err != nil {
		return err
	}

	up.Progress(connectorapi.Progress{Total: up.Content.Size(), Sent: up.Content.Size(), Known: true})

	return nil
}

func (s *memSession) received() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.got.String()
}

// duplex is the two ends of a channel built from operating system pipes. A
// pipe with no buffer, such as net.Pipe, deadlocks once both read loops write.
func duplex(t *testing.T) (host, child duplexPipe) {
	t.Helper()

	hostReads, childWrites, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	childReads, hostWrites, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	return duplexPipe{hostReads, hostWrites}, duplexPipe{childReads, childWrites}
}

type duplexPipe struct{ r, w *os.File }

func (d duplexPipe) Read(p []byte) (int, error)  { return d.r.Read(p) }
func (d duplexPipe) Write(p []byte) (int, error) { return d.w.Write(p) }
func (d duplexPipe) Close() error                { return errors.Join(d.r.Close(), d.w.Close()) }

type memContent string

func (c memContent) Size() int64 { return int64(len(c)) }

func (c memContent) Open(_ context.Context, offset int64) (io.ReadCloser, error) {
	if offset > int64(len(c)) {
		return nil, errors.New("past the end")
	}

	return io.NopCloser(bytes.NewReader([]byte(c)[offset:])), nil
}
