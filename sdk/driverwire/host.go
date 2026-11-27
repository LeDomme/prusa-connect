package driverwire

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/prusa3d/prusa-connect/sdk/connectorapi"
	"github.com/prusa3d/prusa-connect/sdk/wirerpc"
)

// stateBuffer is how many patches a session holds for a host that has not read
// them. The oldest is dropped past it, as connectorapi.Session asks.
const stateBuffer = 256

// closeTimeout bounds the call that tells a module a session is closed.
const closeTimeout = 5 * time.Second

// Client is the host's side of one driver module. It outlives the module's
// process: the host installs its Handlers on each process it launches, and
// each session keeps the peer it was opened on.
type Client struct {
	mu         sync.Mutex
	next       SessionID
	nextUpload uint64
	sessions   map[SessionID]*remoteSession
	uploads    map[uint64]connectorapi.Upload
}

// NewClient returns a client that holds no session.
func NewClient() *Client {
	return &Client{sessions: map[SessionID]*remoteSession{}, uploads: map[uint64]connectorapi.Upload{}}
}

// HostMethods are the names Handlers answers, for a manifest's require block.
func HostMethods() []wirerpc.Method {
	return []wirerpc.Method{MethodState, MethodCapabilities, MethodEnded, MethodContent, MethodProgress}
}

// Handlers answer the calls the module makes on the host.
func (c *Client) Handlers() wirerpc.Methods {
	return wirerpc.Methods{
		MethodState:        c.state,
		MethodCapabilities: c.capabilities,
		MethodEnded:        c.ended,
		MethodContent:      c.content,
		MethodProgress:     c.progress,
	}
}

// Connect opens one session on the module at the other end of peer. gone is
// closed when that module's session with the host ends, and every session
// opened on it ends with it.
func (c *Client) Connect(
	ctx context.Context, peer Peer, gone <-chan struct{}, endpoint connectorapi.Endpoint, profile connectorapi.Set,
) (connectorapi.Session, error) {
	c.mu.Lock()
	c.next++
	s := &remoteSession{
		client: c, peer: peer, id: c.next, ceiling: profile,
		states: make(chan connectorapi.StatePatch, stateBuffer), done: make(chan struct{}),
	}
	c.sessions[s.id] = s
	c.mu.Unlock()

	raw, err := peer.Call(ctx, MethodConnect, ConnectParams{Session: s.id, Endpoint: endpoint, Profile: profile})
	if err != nil {
		s.end()

		return nil, localError(MethodConnect, err)
	}

	var connected Connected
	if err = json.Unmarshal(raw, &connected); err != nil {
		s.end()
		_ = s.Close() // the module holds a session the host cannot read

		return nil, fmt.Errorf("driverwire: %s: read the answer: %w", MethodConnect, err)
	}

	s.identity = connected.Identity
	s.caps, s.epoch = s.held(connected.Capabilities), connected.Epoch

	go func() {
		select {
		case <-gone:
			s.end()
		case <-s.done:
		}
	}()

	return expose(s, connected.Parts), nil
}

func (c *Client) find(id SessionID) (*remoteSession, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	s, ok := c.sessions[id]
	if !ok {
		return nil, wireError(ErrNoSession)
	}

	return s, nil
}

func (c *Client) state(_ context.Context, call wirerpc.Call) (any, error) {
	params, err := decode[StateParams](call)
	if err != nil {
		return nil, err
	}

	if len(params.Patches) > MaxPatches {
		return nil, wireError(ErrTooMany)
	}

	for _, patch := range params.Patches {
		if !strings.HasPrefix(patch.Path, "/") || len(patch.Path) > MaxPathBytes {
			return nil, &wirerpc.Error{Code: wirerpc.CodeInvalidParams, Message: "a patch names no state path"}
		}
	}

	s, err := c.find(params.Session)
	if err != nil {
		return nil, err
	}

	for _, patch := range params.Patches {
		s.push(patch)
	}

	return struct{}{}, nil
}

func (c *Client) capabilities(_ context.Context, call wirerpc.Call) (any, error) {
	params, err := decode[CapabilitiesParams](call)
	if err != nil {
		return nil, err
	}

	s, err := c.find(params.Session)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.caps, s.epoch = s.held(params.Capabilities), params.Epoch
	s.mu.Unlock()

	return struct{}{}, nil
}

func (c *Client) ended(_ context.Context, call wirerpc.Call) (any, error) {
	params, err := decode[EndedParams](call)
	if err != nil {
		return nil, err
	}

	s, err := c.find(params.Session)
	if err != nil {
		return nil, err
	}

	s.end()

	return struct{}{}, nil
}

func (c *Client) upload(id uint64) (connectorapi.Upload, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	up, ok := c.uploads[id]
	if !ok {
		return connectorapi.Upload{}, wireError(ErrNoSession)
	}

	return up, nil
}

// content writes an upload's bytes from an offset onto the call's stream, then
// ends the stream, so the module reads them as one file.
func (c *Client) content(ctx context.Context, call wirerpc.Call) (any, error) {
	params, err := decode[ContentParams](call)
	if err != nil {
		return nil, err
	}

	up, err := c.upload(params.Upload)
	if err != nil {
		return nil, err
	}

	r, err := up.Content.Open(ctx, params.Offset)
	if err != nil {
		return nil, wireError(err)
	}

	_, err = io.Copy(call.Stream, r)
	_ = r.Close() // read only

	if err != nil {
		return nil, wireError(err)
	}

	if err = call.Stream.CloseWrite(); err != nil {
		return nil, wireError(err)
	}

	return struct{}{}, nil
}

func (c *Client) progress(_ context.Context, call wirerpc.Call) (any, error) {
	params, err := decode[ProgressParams](call)
	if err != nil {
		return nil, err
	}

	up, err := c.upload(params.Upload)
	if err != nil {
		return nil, err
	}

	if up.Progress != nil {
		up.Progress(params.Progress)
	}

	return struct{}{}, nil
}

// remoteSession is a session that lives in a module process.
type remoteSession struct {
	client *Client
	peer   Peer
	id     SessionID

	mu       sync.Mutex
	identity connectorapi.Identity
	ceiling  connectorapi.Set
	caps     connectorapi.Set
	epoch    uint64
	states   chan connectorapi.StatePatch
	ended    bool
	done     chan struct{}
}

// held narrows a set the module reports to the profile the host connected
// with, so a module never offers more than its baseline (ADR-20261123-sat
// rule 10). A nil profile names no ceiling.
func (s *remoteSession) held(set connectorapi.Set) connectorapi.Set {
	if s.ceiling == nil {
		return set
	}

	return set.Intersect(s.ceiling)
}

func (s *remoteSession) Identity() connectorapi.Identity { return s.identity }

func (s *remoteSession) Capabilities() (connectorapi.Set, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.caps.Clone(), s.epoch
}

func (s *remoteSession) State() <-chan connectorapi.StatePatch { return s.states }

// push hands one patch to the host. It never blocks: past the buffer the
// oldest patch is dropped.
func (s *remoteSession) push(patch connectorapi.StatePatch) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.ended {
		return
	}

	for {
		select {
		case s.states <- patch:
			return
		default:
		}

		select {
		case <-s.states:
		default:
		}
	}
}

// end forgets the session and closes its state channel, which is how the host
// learns it is over. It is safe to call more than once.
func (s *remoteSession) end() {
	s.client.mu.Lock()
	delete(s.client.sessions, s.id)
	s.client.mu.Unlock()

	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.ended {
		s.ended = true
		close(s.states)
		close(s.done)
	}
}

func (s *remoteSession) Close() error {
	s.mu.Lock()
	ended := s.ended
	s.mu.Unlock()

	s.end()

	if ended {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
	defer cancel()

	if _, err := s.peer.Call(ctx, MethodClose, SessionParams{Session: s.id}); err != nil {
		return localError(MethodClose, err)
	}

	return nil
}

func (s *remoteSession) call(ctx context.Context, method wirerpc.Method, params, out any) error {
	raw, err := s.peer.Call(ctx, method, params)
	if err != nil {
		return localError(method, err)
	}

	if out == nil {
		return nil
	}

	if err = json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("driverwire: %s: read the answer: %w", method, err)
	}

	return nil
}

func (s *remoteSession) Do(ctx context.Context, cmd connectorapi.Command) (connectorapi.Result, error) {
	var result connectorapi.Result

	err := s.call(ctx, MethodDo, DoParams{Session: s.id, Command: cmd}, &result)

	return result, err
}

// The optional parts, each a view of one remote session. expose combines the
// ones the module said the session has, so the host finds exactly those with
// a type assertion.
type (
	files     struct{ s *remoteSession }
	manager   struct{ s *remoteSession }
	transfers struct{ s *remoteSession }
)

func (f files) Storages(ctx context.Context) ([]connectorapi.Storage, error) {
	var held Storages

	err := f.s.call(ctx, MethodStorages, SessionParams{Session: f.s.id}, &held)

	return held.Storages, err
}

func (f files) List(ctx context.Context, path string) (connectorapi.Listing, error) {
	var listing connectorapi.Listing

	err := f.s.call(ctx, MethodList, PathParams{Session: f.s.id, Path: path}, &listing)

	return listing, err
}

func (m manager) CreateFolder(ctx context.Context, path string) error {
	return m.s.call(ctx, MethodCreateFolder, PathParams{Session: m.s.id, Path: path}, nil)
}

func (m manager) DeleteFile(ctx context.Context, path string) error {
	return m.s.call(ctx, MethodDeleteFile, PathParams{Session: m.s.id, Path: path}, nil)
}

func (m manager) DeleteFolder(ctx context.Context, path string) error {
	return m.s.call(ctx, MethodDeleteFolder, PathParams{Session: m.s.id, Path: path}, nil)
}

// Send registers the upload for the module's content and progress calls for
// as long as the module's send runs.
func (t transfers) Send(ctx context.Context, up connectorapi.Upload) error {
	c := t.s.client

	c.mu.Lock()
	c.nextUpload++
	id := c.nextUpload
	c.uploads[id] = up
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.uploads, id)
		c.mu.Unlock()
	}()

	return t.s.call(ctx, MethodSend, SendParams{
		Session: t.s.id, Upload: id, Path: up.Path, Size: up.Content.Size(),
	}, nil)
}

func expose(s *remoteSession, p Parts) connectorapi.Session {
	f, m, t := files{s}, manager{s}, transfers{s}

	switch p {
	case Parts{Files: true, Manager: true, Transfers: true}:
		return struct {
			*remoteSession
			files
			manager
			transfers
		}{s, f, m, t}
	case Parts{Files: true, Manager: true}:
		return struct {
			*remoteSession
			files
			manager
		}{s, f, m}
	case Parts{Files: true, Transfers: true}:
		return struct {
			*remoteSession
			files
			transfers
		}{s, f, t}
	case Parts{Manager: true, Transfers: true}:
		return struct {
			*remoteSession
			manager
			transfers
		}{s, m, t}
	case Parts{Files: true}:
		return struct {
			*remoteSession
			files
		}{s, f}
	case Parts{Manager: true}:
		return struct {
			*remoteSession
			manager
		}{s, m}
	case Parts{Transfers: true}:
		return struct {
			*remoteSession
			transfers
		}{s, t}
	}

	return s
}
