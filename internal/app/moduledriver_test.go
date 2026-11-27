package app

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/prusa3d/prusa-connect/internal/app/resource"
)

// An endpoint whose connector names a driver module is dialled through the
// module, with no connector of that name declared (ADR-20261123-sat rule 11).
func TestAnEndpointNamingADriverModuleIsDialledThroughIt(t *testing.T) {
	t.Parallel()

	device := newListeningPrinter(t, "DIAL-MODULE")
	connector := dialConnectorFor(t, newPlane(t))

	loops := newDials(discard(), nil, fastLadder())
	loops.modules = func(name string) (dialledConnector, bool) {
		return connector, name == "example"
	}
	loops.start(t.Context())

	t.Cleanup(loops.stop)

	loops.SyncEndpoints(t.Context(), []resource.Endpoint{{
		Name: "bench", Connector: "example", Address: device.address(), Enabled: true,
	}})

	require.Eventually(t, func() bool {
		return device.accepted() > 0
	}, settle, 5*time.Millisecond, "an endpoint naming a driver module was not dialled")
}
