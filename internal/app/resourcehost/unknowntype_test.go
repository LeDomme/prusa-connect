package resourcehost_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/prusa3d/prusa-connect/internal/app/resourcehost"
	"github.com/prusa3d/prusa-connect/internal/kernel/condition"
	"github.com/prusa3d/prusa-connect/internal/kernel/ref"
	"github.com/prusa3d/prusa-connect/internal/transport/http/httpapi"
)

// TestATypeThisBuildDoesNotHaveFailsItsInstanceAndNothingElse is earlier research
// section 10.4 for every kind this server reads that an operator's file can
// declare a compiled-in type under. The misspelt instance goes failed, not
// pending and not retrying, with a message that names it and the types this
// build has. The instance beside it still starts.
func TestATypeThisBuildDoesNotHaveFailsItsInstanceAndNothingElse(t *testing.T) {
	t.Parallel()

	w := newWired(t)

	var (
		file     strings.Builder
		misspelt []ref.Ref
	)

	file.WriteString("resources:\n")

	for _, kind := range ref.SupervisedKinds() {
		// A kind nothing reads back is refused at load whatever its type, so
		// it never reaches this host. Nor does a builtin driver: only a driver
		// module is read back (ADR-20261123-sat).
		if !kind.Read() || kind == ref.KindDriver || !slices.Contains(ref.KindRuntimes(kind), ref.RuntimeBuiltin) {
			continue
		}

		instance, err := ref.New(kind, "misspelt")
		require.NoError(t, err)

		misspelt = append(misspelt, instance)

		fmt.Fprintf(&file, "  %s:\n    misspelt:\n      type: nope\n", kind)

		if kind == ref.KindSink {
			file.WriteString("    webhook:\n      type: counted\n")
		}
	}

	require.NotEmpty(t, misspelt)

	w.writeFile(file.String())
	require.NoError(t, w.store.Reload(t.Context()))

	w.waitReady(theSink(t))

	for _, instance := range misspelt {
		status := w.waitSettled(instance)
		require.Equal(t, condition.StateFailed().String(), status.State, "%s: %s", instance, w.describe(instance))

		message := startedMessage(status)
		require.Contains(t, message, resourcehost.CodeUnknownType().String(), "%s", instance)
		require.Contains(t, message, instance.String(), "the message names the instance")
		require.Contains(t, message, `"nope"`, "the message names the type")
		require.Contains(t, message, ". It has ", "the message names the types there are")
		require.Equal(t, 1, strings.Count(message, `"nope"`), "the message says it once: %s", message)
		require.Equal(t, 1, status.Attempt, "%s: a type no build of this binary has is not retried", instance)
	}

	sink, err := ref.New(ref.KindSink, "misspelt")
	require.NoError(t, err)

	status, ok := w.host.Status(sink)
	require.True(t, ok)
	require.Contains(t, startedMessage(status), "core/counted", "the sink kind lists the sink types it has")
}

// waitSettled blocks until the first start of an instance has an outcome,
// failed or retrying. A regression that retries then fails the assertion after
// it at once, instead of running into the await deadline.
func (w *wired) waitSettled(instance ref.Ref) httpapi.ResourceStatus {
	w.t.Helper()

	var status httpapi.ResourceStatus

	w.await(func() bool {
		found, ok := w.host.Status(instance)
		status = found

		return ok && (found.State == condition.StateFailed().String() ||
			found.State == condition.StateRetrying().String())
	}, "the first start never had an outcome", func() string { return w.describe(instance) })

	return status
}

// startedMessage is the human sentence on the Started condition, which is
// where a failed start says why.
func startedMessage(status httpapi.ResourceStatus) string {
	for _, held := range status.Conditions {
		if held.Type == condition.TypeStarted.String() {
			return held.Message
		}
	}

	return ""
}
