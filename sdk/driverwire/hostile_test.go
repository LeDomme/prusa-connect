package driverwire_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/prusa3d/prusa-connect/sdk/connectorapi"
	"github.com/prusa3d/prusa-connect/sdk/driverwire"
	"github.com/prusa3d/prusa-connect/sdk/wirerpc"
)

// verbOver is a verb no baseline of these tests has.
const verbOver = connectorapi.Capability("core.command.flash")

// hostile is a module that answers driver.connect itself, so a case can send
// the host what no honest driver would.
type hostile struct {
	child   *wirerpc.Conn
	session connectorapi.Session
	id      driverwire.SessionID
}

// connectHostile connects through a module that reports caps on connect.
func connectHostile(t *testing.T, caps connectorapi.Set) *hostile {
	t.Helper()

	hostEnd, childEnd := duplex(t)
	client := driverwire.NewClient()
	h := &hostile{}

	host, err := wirerpc.NewConn(hostEnd, wirerpc.Options{Role: wirerpc.RoleHost, Methods: client.Handlers()})
	if err != nil {
		t.Fatal(err)
	}

	h.child, err = wirerpc.NewConn(childEnd, wirerpc.Options{Role: wirerpc.RoleChild, Methods: wirerpc.Methods{
		driverwire.MethodConnect: func(_ context.Context, call wirerpc.Call) (any, error) {
			var params driverwire.ConnectParams
			if decodeErr := json.Unmarshal(call.Params, &params); decodeErr != nil {
				return nil, decodeErr
			}

			h.id = params.Session

			return driverwire.Connected{Capabilities: caps, Epoch: 1}, nil
		},
		driverwire.MethodClose: func(context.Context, wirerpc.Call) (any, error) { return struct{}{}, nil },
	}})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	go func() { _ = host.Serve(ctx) }()
	go func() { _ = h.child.Serve(ctx) }()

	baseline := (&memDriver{}).Baseline()

	h.session, err = client.Connect(t.Context(), host, host.Done(), connectorapi.Endpoint{Address: "mem"}, baseline)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = h.session.Close() })

	return h
}

func (h *hostile) call(t *testing.T, method wirerpc.Method, params any) error {
	t.Helper()

	_, err := h.child.Call(t.Context(), method, params)

	return err
}

// ADR-20261123-sat rule 10: a set over the baseline is held to it, on connect
// and on a later change.
func TestAModuleCannotOfferMoreThanItsBaseline(t *testing.T) {
	t.Parallel()

	over := connectorapi.Set{verbInfo: {Flag: true}, verbOver: {Flag: true}}
	h := connectHostile(t, over)

	if caps, _ := h.session.Capabilities(); caps.Has(verbOver) || !caps.Has(verbInfo) {
		t.Fatalf("on connect the host holds %v", caps.Keys())
	}

	err := h.call(t, driverwire.MethodCapabilities, driverwire.CapabilitiesParams{
		Session: h.id, Capabilities: over, Epoch: 2,
	})
	if err != nil {
		t.Fatal(err)
	}

	if caps, epoch := h.session.Capabilities(); caps.Has(verbOver) || epoch != 2 {
		t.Fatalf("after a change the host holds %v at epoch %d", caps.Keys(), epoch)
	}
}

// ADR-20261123-sat rule 10: a patch off the state paths, or a call over the
// wire limits, is refused whole and reaches nothing.
func TestAModuleCannotPatchOffTheStatePaths(t *testing.T) {
	t.Parallel()

	h := connectHostile(t, (&memDriver{}).Baseline())
	patch := func(path string) connectorapi.StatePatch {
		return connectorapi.StatePatch{Path: path, Value: json.RawMessage(`1`)}
	}

	for name, patches := range map[string][]connectorapi.StatePatch{
		"a path off the root":   {patch("/ok"), patch("temp/bed")},
		"an empty path":         {patch("")},
		"a path over the limit": {patch("/" + strings.Repeat("a", driverwire.MaxPathBytes))},
		"too many patches":      make([]connectorapi.StatePatch, driverwire.MaxPatches+1),
	} {
		err := h.call(t, driverwire.MethodState, driverwire.StateParams{Session: h.id, Patches: patches})

		if coded, ok := errors.AsType[*wirerpc.Error](err); !ok || coded.Code == 0 {
			t.Errorf("%s: the host answered %v", name, err)
		}
	}

	select {
	case got := <-h.session.State():
		t.Fatalf("a refused call reached the session: %+v", got)
	case <-time.After(50 * time.Millisecond):
	}
}
