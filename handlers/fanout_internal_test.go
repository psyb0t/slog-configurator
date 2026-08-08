package handlers

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordTime is a fixed timestamp — nothing here asserts on it, and a real
// clock would be the only nondeterminism in these tests.
func recordTime() time.Time {
	return time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errWriteFailed
}

var errWriteFailed = errors.New("write failed")

// countingHandler records what it was asked to do.
type countingHandler struct {
	level    slog.Level
	handled  int
	enabled  int
	attrs    []slog.Attr
	group    string
	failWith error
}

func (h *countingHandler) Enabled(_ context.Context, level slog.Level) bool {
	h.enabled++

	return level >= h.level
}

func (h *countingHandler) Handle(context.Context, slog.Record) error {
	h.handled++

	return h.failWith
}

func (h *countingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &countingHandler{level: h.level, attrs: attrs, failWith: h.failWith}
}

func (h *countingHandler) WithGroup(name string) slog.Handler {
	return &countingHandler{level: h.level, group: name, failWith: h.failWith}
}

func TestFanOutTeesToEveryHandler(t *testing.T) {
	t.Parallel()

	first, second := &countingHandler{level: slog.LevelDebug}, &countingHandler{level: slog.LevelDebug}
	fanOut := NewFanOut(first, second)

	require.NoError(t, fanOut.Handle(
		context.Background(),
		slog.NewRecord(recordTime(), slog.LevelInfo, "x", 0),
	))

	assert.Equal(t, 1, first.handled)
	assert.Equal(t, 1, second.handled)
	assert.Equal(t, 2, fanOut.Len())
}

// Each child re-checks its OWN level, so a quiet handler beside a noisy one
// does not receive what it filtered out. This is what makes "console at INFO,
// ring at DEBUG" work.
func TestFanOutRespectsPerHandlerLevels(t *testing.T) {
	t.Parallel()

	quiet := &countingHandler{level: slog.LevelError}
	noisy := &countingHandler{level: slog.LevelDebug}

	fanOut := NewFanOut(quiet, noisy)

	require.NoError(t, fanOut.Handle(
		context.Background(),
		slog.NewRecord(recordTime(), slog.LevelInfo, "x", 0),
	))

	assert.Equal(t, 0, quiet.handled, "the quiet handler filtered it out")
	assert.Equal(t, 1, noisy.handled)
}

// Enabled is ANY, not ALL: the fan-out must not veto a record one of its
// children wants. Were it ALL, a console at INFO would stop a DEBUG-retaining
// ring from ever seeing a debug line.
func TestFanOutEnabledIsAny(t *testing.T) {
	t.Parallel()

	fanOut := NewFanOut(
		&countingHandler{level: slog.LevelError},
		&countingHandler{level: slog.LevelDebug},
	)

	assert.True(t, fanOut.Enabled(context.Background(), slog.LevelDebug))
	assert.False(t, NewFanOut(&countingHandler{level: slog.LevelError}).
		Enabled(context.Background(), slog.LevelDebug))
	assert.False(t, NewFanOut().Enabled(context.Background(), slog.LevelError),
		"an empty fan-out enables nothing")
}

// One broken sink must not silence the ones after it. slog discards whatever
// Handle returns, so an early return would let an unreachable shipper take
// stdout down with it and nothing would say why.
func TestFanOutKeepsGoingAfterAFailureAndJoinsEveryError(t *testing.T) {
	t.Parallel()

	broken := &countingHandler{level: slog.LevelDebug, failWith: errWriteFailed}
	alsoBroken := &countingHandler{level: slog.LevelDebug, failWith: errWriteFailed}
	healthy := &countingHandler{level: slog.LevelDebug}

	err := NewFanOut(broken, alsoBroken, healthy).Handle(
		context.Background(),
		slog.NewRecord(recordTime(), slog.LevelInfo, "x", 0),
	)

	require.Error(t, err)
	assert.Equal(t, 1, healthy.handled, "a handler AFTER a broken one still runs")
	assert.Contains(t, err.Error(), "handler 0 failed")
	assert.Contains(t, err.Error(), "handler 1 failed",
		"every failure is reported, not just the first")
}

// Handlers returns a COPY. Handing out the backing array would let a caller
// reorder or nil an entry and silently change where the process logs.
func TestHandlersReturnsACopy(t *testing.T) {
	t.Parallel()

	original := &countingHandler{level: slog.LevelDebug}
	fanOut := NewFanOut(original)

	stolen := fanOut.Handlers()
	require.Len(t, stolen, 1)

	stolen[0] = nil

	require.NoError(t, fanOut.Handle(
		context.Background(),
		slog.NewRecord(recordTime(), slog.LevelInfo, "x", 0),
	))
	assert.Equal(t, 1, original.handled,
		"mutating the returned slice must not affect the fan-out")
}

func TestFanOutWithAttrsAndWithGroup(t *testing.T) {
	t.Parallel()

	attrs := []slog.Attr{slog.String("k", "v")}

	withAttrs, ok := NewFanOut(&countingHandler{}, &countingHandler{}).
		WithAttrs(attrs).(*FanOutHandler)
	require.True(t, ok)
	require.Equal(t, 2, withAttrs.Len())

	for _, h := range withAttrs.Handlers() {
		child, ok := h.(*countingHandler)
		require.True(t, ok)
		assert.Equal(t, attrs, child.attrs)
	}

	withGroup, ok := NewFanOut(&countingHandler{}).WithGroup("g").(*FanOutHandler)
	require.True(t, ok)

	child, ok := withGroup.Handlers()[0].(*countingHandler)
	require.True(t, ok)
	assert.Equal(t, "g", child.group)
}

// The fan-out carrying the slog.Handler INTERFACE is what lets unrelated sink
// types share one chain. A concrete element type would make it useless for the
// very things it exists to hold.
func TestFanOutCarriesUnrelatedHandlerTypes(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	opts := &slog.HandlerOptions{Level: slog.LevelDebug}

	own, err := New(Options{Level: slog.LevelDebug}, Stdout(buf), Stderr(buf))
	require.NoError(t, err)

	foreign := slog.NewJSONHandler(buf, opts)
	custom := &countingHandler{level: slog.LevelDebug}

	fanOut := NewFanOut(own, foreign, custom)

	require.NoError(t, fanOut.Handle(
		context.Background(),
		slog.NewRecord(recordTime(), slog.LevelInfo, "shared", 0),
	))

	assert.Equal(t, 1, custom.handled)
	assert.Contains(t, buf.String(), "shared")
}
