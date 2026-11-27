package resourcehost

import (
	"github.com/prusa3d/prusa-connect/internal/kernel/ref"
	"github.com/prusa3d/prusa-connect/sdk/driverwire"
)

// DriverClient is the host's side of one driver module instance. Every
// process the host launches for the instance answers to the same one, and a
// session keeps the process it was opened on (ADR-20261123-sat).
func (h *Host) DriverClient(instance ref.Ref) *driverwire.Client {
	h.mu.Lock()
	defer h.mu.Unlock()

	client, ok := h.drivers[instance]
	if !ok {
		client = driverwire.NewClient()
		h.drivers[instance] = client
	}

	return client
}

// DriverPeer is the session with the running child, and the channel that
// closes when that session ends. A driver module's sessions are opened on it
// and end with it.
func (s *serviceChild) DriverPeer() (driverwire.Peer, <-chan struct{}) {
	conn := s.child.Conn()

	return conn, conn.Done()
}
