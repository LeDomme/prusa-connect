package driverwire

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/prusa3d/prusa-connect/sdk/connectorapi"
	"github.com/prusa3d/prusa-connect/sdk/wirerpc"
)

// Peer is the part of a wirerpc session this package uses. *wirerpc.Conn
// implements it.
type Peer interface {
	Call(ctx context.Context, method wirerpc.Method, params any) (json.RawMessage, error)
	Open() (*wirerpc.Stream, error)
}

// reportTimeout bounds one report the module makes to the host. A host that
// does not answer in that time is not reading, and the report is dropped.
const reportTimeout = 10 * time.Second

// Module runs one driver inside a module process and answers the host's
// driver.* calls with it.
type Module struct {
	host Peer
	log  *slog.Logger

	mu       sync.Mutex
	outbound connectorapi.Outbound
	sessions map[SessionID]*held
	closed   bool
}

// held is one session the module serves, with the last capability epoch it
// told the host.
type held struct {
	session connectorapi.Session
	epoch   uint64
}

// NewModule returns a module that calls the host through host. It serves
// nothing until Use gives it a driver.
func NewModule(host Peer, log *slog.Logger) *Module {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	return &Module{host: host, log: log, sessions: map[SessionID]*held{}}
}

// Use gives the module its driver. The driver must dial, because a printer
// never reaches a module (ADR-20261123-sat rule 4).
func (m *Module) Use(driver connectorapi.Driver) error {
	outbound, ok := driver.(connectorapi.Outbound)
	if !ok {
		return fmt.Errorf("driverwire: %T does not dial: %w", driver, ErrNotSupported)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.outbound = outbound

	return nil
}

// Close ends every session. The module answers no call after it.
func (m *Module) Close() {
	m.mu.Lock()
	m.closed = true
	sessions := m.sessions
	m.sessions = map[SessionID]*held{}
	m.mu.Unlock()

	for _, one := range sessions {
		_ = one.session.Close() // the module is going, and so is its host
	}
}

// Methods are the names Handlers answers, for wirerpc.Info.Serves.
func Methods() []wirerpc.Method {
	return []wirerpc.Method{
		MethodConnect, MethodDo, MethodStorages, MethodList,
		MethodCreateFolder, MethodDeleteFile, MethodDeleteFolder, MethodSend, MethodClose,
	}
}

// Handlers answer the calls the host makes on the module.
func (m *Module) Handlers() wirerpc.Methods {
	return wirerpc.Methods{
		MethodConnect:  m.connect,
		MethodDo:       m.do,
		MethodStorages: m.storages,
		MethodList: onPath(m, func(ctx context.Context, s connectorapi.Session, path string) (any, error) {
			return part[connectorapi.Filesystem](s, func(f connectorapi.Filesystem) (any, error) {
				return f.List(ctx, path)
			})
		}),
		MethodCreateFolder: onManager(m, connectorapi.FileManager.CreateFolder),
		MethodDeleteFile:   onManager(m, connectorapi.FileManager.DeleteFile),
		MethodDeleteFolder: onManager(m, connectorapi.FileManager.DeleteFolder),
		MethodSend:         m.send,
		MethodClose:        m.close,
	}
}

func (m *Module) connect(ctx context.Context, call wirerpc.Call) (any, error) {
	params, err := decode[ConnectParams](call)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	outbound := m.outbound
	_, taken := m.sessions[params.Session]
	m.mu.Unlock()

	switch {
	case outbound == nil:
		return nil, wireError(ErrNoDriver)
	case taken:
		return nil, &wirerpc.Error{Code: wirerpc.CodeInvalidParams, Message: "the session id is taken"}
	}

	profile := params.Profile
	if profile == nil {
		profile = outbound.Baseline()
	}

	session, err := outbound.Connect(ctx, params.Endpoint, profile)
	if err != nil {
		return nil, wireError(err)
	}

	caps, epoch := session.Capabilities()
	one := &held{session: session, epoch: epoch}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		_ = session.Close() // nobody is left to serve it

		return nil, wireError(ErrNoDriver)
	}

	m.sessions[params.Session] = one
	m.mu.Unlock()

	go m.pump(params.Session, one)

	return Connected{
		Identity: session.Identity(), Capabilities: caps, Epoch: epoch, Parts: partsOf(session),
	}, nil
}

// pump sends one session's patches to the host until its state channel
// closes, and then tells the host the session ended.
func (m *Module) pump(id SessionID, one *held) {
	states := one.session.State()

	for patch := range states {
		batch := []connectorapi.StatePatch{patch}

	drain:
		for len(batch) < MaxPatches {
			select {
			case more, open := <-states:
				if !open {
					break drain
				}

				batch = append(batch, more)
			default:
				break drain
			}
		}

		m.report(MethodState, StateParams{Session: id, Patches: batch})

		if caps, epoch := one.session.Capabilities(); epoch != one.epoch {
			one.epoch = epoch
			m.report(MethodCapabilities, CapabilitiesParams{Session: id, Capabilities: caps, Epoch: epoch})
		}
	}

	m.mu.Lock()
	_, ours := m.sessions[id]
	delete(m.sessions, id)
	m.mu.Unlock()

	// A session the host closed is not reported: the host already knows.
	if ours {
		m.report(MethodEnded, EndedParams{Session: id})
	}
}

// report makes one call on the host and logs a failure. A report is never
// retried: the next one carries the newer state.
func (m *Module) report(method wirerpc.Method, params any) {
	ctx, cancel := context.WithTimeout(context.Background(), reportTimeout)
	defer cancel()

	if _, err := m.host.Call(ctx, method, params); err != nil {
		m.log.WarnContext(ctx, "driverwire: the host did not take a report", "method", method, "err", err)
	}
}

func (m *Module) session(id SessionID) (connectorapi.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	one, ok := m.sessions[id]
	if !ok {
		return nil, wireError(ErrNoSession)
	}

	return one.session, nil
}

func (m *Module) do(ctx context.Context, call wirerpc.Call) (any, error) {
	params, err := decode[DoParams](call)
	if err != nil {
		return nil, err
	}

	session, err := m.session(params.Session)
	if err != nil {
		return nil, err
	}

	result, err := session.Do(ctx, params.Command)

	return result, wireError(err)
}

func (m *Module) storages(ctx context.Context, call wirerpc.Call) (any, error) {
	params, err := decode[SessionParams](call)
	if err != nil {
		return nil, err
	}

	session, err := m.session(params.Session)
	if err != nil {
		return nil, err
	}

	return part[connectorapi.Filesystem](session, func(f connectorapi.Filesystem) (any, error) {
		held, listErr := f.Storages(ctx)

		return Storages{Storages: held}, listErr
	})
}

func (m *Module) close(_ context.Context, call wirerpc.Call) (any, error) {
	params, err := decode[SessionParams](call)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	one, ok := m.sessions[params.Session]
	delete(m.sessions, params.Session)
	m.mu.Unlock()

	if !ok {
		return struct{}{}, nil // closing twice is not an error, as the contract says
	}

	return nil, wireError(one.session.Close())
}

func (m *Module) send(ctx context.Context, call wirerpc.Call) (any, error) {
	params, err := decode[SendParams](call)
	if err != nil {
		return nil, err
	}

	session, err := m.session(params.Session)
	if err != nil {
		return nil, err
	}

	return part[connectorapi.Transfers](session, func(t connectorapi.Transfers) (any, error) {
		return nil, t.Send(ctx, connectorapi.Upload{
			Path:    params.Path,
			Content: &remoteContent{host: m.host, upload: params.Upload, size: params.Size},
			Progress: func(p connectorapi.Progress) {
				m.report(MethodProgress, ProgressParams{Upload: params.Upload, Progress: p})
			},
		})
	})
}

// onPath answers a call that names one path on a session.
func onPath(
	m *Module, fn func(ctx context.Context, s connectorapi.Session, path string) (any, error),
) wirerpc.Handler {
	return func(ctx context.Context, call wirerpc.Call) (any, error) {
		params, err := decode[PathParams](call)
		if err != nil {
			return nil, err
		}

		if len(params.Path) > MaxPathBytes {
			return nil, wireError(ErrTooMany)
		}

		session, err := m.session(params.Session)
		if err != nil {
			return nil, err
		}

		return fn(ctx, session, params.Path)
	}
}

// onManager answers one of the FileManager calls.
func onManager(
	m *Module, fn func(connectorapi.FileManager, context.Context, string) error,
) wirerpc.Handler {
	return onPath(m, func(ctx context.Context, s connectorapi.Session, path string) (any, error) {
		return part[connectorapi.FileManager](s, func(f connectorapi.FileManager) (any, error) {
			return nil, fn(f, ctx, path)
		})
	})
}

// part runs fn on the optional part P of a session, and refuses a session
// without it.
func part[P any](s connectorapi.Session, fn func(P) (any, error)) (any, error) {
	has, ok := s.(P)
	if !ok {
		return nil, wireError(ErrNotSupported)
	}

	out, err := fn(has)

	return out, wireError(err)
}

func partsOf(s connectorapi.Session) Parts {
	_, files := s.(connectorapi.Filesystem)
	_, manager := s.(connectorapi.FileManager)
	_, transfers := s.(connectorapi.Transfers)

	return Parts{Files: files, Manager: manager, Transfers: transfers}
}

// remoteContent is an upload's bytes, which stay on the host and are read
// over the wire when the driver opens them.
type remoteContent struct {
	host   Peer
	upload uint64
	size   int64
}

func (c *remoteContent) Size() int64 { return c.size }

// Open asks the host for the bytes from offset. They arrive on the call's own
// stream, and the answer that follows them says how many there were.
func (c *remoteContent) Open(ctx context.Context, offset int64) (io.ReadCloser, error) {
	stream, err := c.host.Open()
	if err != nil {
		return nil, fmt.Errorf("driverwire: %s: open a stream: %w", MethodContent, err)
	}

	ctx, cancel := context.WithCancel(ctx)
	answered := make(chan error, 1)

	go func() {
		_, callErr := stream.Call(ctx, MethodContent, ContentParams{Upload: c.upload, Offset: offset})
		if callErr != nil {
			callErr = localError(MethodContent, callErr)
		}

		answered <- callErr
	}()

	return &contentReader{stream: stream, answered: answered, cancel: cancel}, nil
}

// contentReader reads an upload's bytes off one stream. Its end of file waits
// for the host's answer, so a host that failed half way is an error and not a
// short file.
type contentReader struct {
	stream   *wirerpc.Stream
	answered chan error
	cancel   context.CancelFunc
	answer   sync.Once
	failed   error
}

func (r *contentReader) Read(p []byte) (int, error) {
	n, err := r.stream.Read(p)
	if err == io.EOF { //nolint:errorlint // Stream.Read returns io.EOF itself
		r.answer.Do(func() { r.failed = <-r.answered })

		if r.failed != nil {
			return n, r.failed
		}
	}

	return n, err //nolint:wrapcheck // io.Reader's own errors, io.EOF among them
}

func (r *contentReader) Close() error {
	r.cancel()

	return r.stream.Close() //nolint:wrapcheck // the stream's own error
}
