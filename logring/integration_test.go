package logring_test

import (
	"log/slog"
	"testing"

	slogconfigurator "github.com/psyb0t/slog-configurator"
	"github.com/psyb0t/slog-configurator/logring"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The ring is meant to be stacked onto the configured fan-out, and it has to
// keep its OWN level gate once it is there. This matters because a process can
// run at DEBUG: a fan-out dispatching on its own Enabled alone would pour every
// trace line into the ring and bury what you actually search for.
func TestRingKeepsItsLevelGateInsideTheConfiguredFanOut(t *testing.T) {
	t.Setenv(slogconfigurator.EnvVarNameLevel, "debug")
	require.NoError(t, slogconfigurator.Init(slogconfigurator.Options{}))

	// Init replaces the process-global default logger, so put the ambient
	// configuration back for whatever runs after this test.
	t.Cleanup(func() {
		require.NoError(t, slogconfigurator.Init(slogconfigurator.Options{}))
	})

	ring := logring.New(logring.Options{Level: slog.LevelWarn})
	require.True(
		t,
		slogconfigurator.AddHandler(ring),
		"the default logger should be this package's fan-out",
	)

	slog.Debug("a debug line the ring must not keep")
	slog.Info("an info line the ring must not keep")
	slog.Warn("a warn line the ring must keep")

	entries := ring.Search(logring.SearchOptions{}).Entries

	require.Len(t, entries, 1)
	assert.Contains(t, entries[0].Line, "a warn line the ring must keep")
}

// A ring stacked onto the fan-out must not stop the handlers already there.
func TestAddingTheRingLeavesTheExistingHandlersIntact(t *testing.T) {
	require.NoError(t, slogconfigurator.Init(slogconfigurator.Options{}))

	t.Cleanup(func() {
		require.NoError(t, slogconfigurator.Init(slogconfigurator.Options{}))
	})

	before := slog.Default().Handler()

	fanOut, ok := before.(*slogconfigurator.FanOutHandler)
	require.True(t, ok, "expected the configured fan-out")

	countBefore := fanOut.Len()

	require.True(t, slogconfigurator.AddHandler(logring.New(logring.Options{})))

	after, ok := slog.Default().Handler().(*slogconfigurator.FanOutHandler)
	require.True(t, ok)
	assert.Equal(t, countBefore+1, after.Len())
}
