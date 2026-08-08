package handlers

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestHandler wires a Handler at DEBUG over two buffers.
func newTestHandler(t *testing.T, opts Options) (*Handler, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}

	if opts.Level == nil {
		opts.Level = slog.LevelDebug
	}

	h, err := New(opts, Stdout(stdout), Stderr(stderr))
	require.NoError(t, err)

	return h, stdout, stderr
}

func TestSplitsByLevel(t *testing.T) {
	t.Parallel()

	h, stdout, stderr := newTestHandler(t, Options{})
	logger := slog.New(h)

	logger.Debug("dbg")
	logger.Info("inf")
	logger.Warn("wrn")
	logger.Error("err")

	assert.Contains(t, stdout.String(), "dbg")
	assert.Contains(t, stdout.String(), "inf")
	assert.NotContains(t, stdout.String(), "wrn")
	assert.NotContains(t, stdout.String(), "err")

	assert.Contains(t, stderr.String(), "wrn")
	assert.Contains(t, stderr.String(), "err")
	assert.NotContains(t, stderr.String(), "inf")
}

// The split point is a setting, not a hardcoded branch.
func TestSplitAtIsConfigurable(t *testing.T) {
	t.Parallel()

	h, stdout, stderr := newTestHandler(t, Options{SplitAt: slog.LevelError})
	logger := slog.New(h)

	logger.Warn("wrn")
	logger.Error("err")

	assert.Contains(t, stdout.String(), "wrn",
		"with SplitAt=Error, a warning belongs on stdout")
	assert.Contains(t, stderr.String(), "err")
}

// Pointing both sides at one writer reproduces what stdlib slog does, which is
// to put every level on a single stream. "Split" is a configuration, not a
// separate kind of handler.
func TestBothSidesMayBeTheSameWriter(t *testing.T) {
	t.Parallel()

	both := &bytes.Buffer{}

	h, err := New(
		Options{Level: slog.LevelDebug},
		Stdout(both),
		Stderr(both),
	)
	require.NoError(t, err)

	logger := slog.New(h)
	logger.Info("inf")
	logger.Error("err")

	assert.Contains(t, both.String(), "inf")
	assert.Contains(t, both.String(), "err")
}

// N writers per stream — the thing "multi writer" was always supposed to mean.
func TestSeveralWritersPerStream(t *testing.T) {
	t.Parallel()

	outA, outB, errC := &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{}

	h, err := New(
		Options{Level: slog.LevelDebug},
		Stdout(outA, outB),
		Stderr(errC),
	)
	require.NoError(t, err)

	logger := slog.New(h)
	logger.Info("fanned")
	logger.Error("boom")

	assert.Contains(t, outA.String(), "fanned")
	assert.Contains(t, outB.String(), "fanned", "every stdout writer receives it")
	assert.NotContains(t, outA.String(), "boom")
	assert.Contains(t, errC.String(), "boom")
}

// Repeated Stdout options accumulate rather than overwrite.
func TestWriterOptionsAccumulate(t *testing.T) {
	t.Parallel()

	first, second := &bytes.Buffer{}, &bytes.Buffer{}

	h, err := New(
		Options{Level: slog.LevelDebug},
		Stdout(first),
		Stdout(second),
	)
	require.NoError(t, err)

	slog.New(h).Info("both")

	assert.Contains(t, first.String(), "both")
	assert.Contains(t, second.String(), "both")
}

// THE regression this type exists to fix.
//
// Level is stored as a slog.Leveler and resolved on every check. Storing the
// resolved slog.Level instead — which is what the predecessor did — silently
// ignores every later change, so a *slog.LevelVar bumped at runtime does
// nothing and no test, build or lint notices.
func TestLevelVarTakesEffectAfterConstruction(t *testing.T) {
	t.Parallel()

	levelVar := new(slog.LevelVar)
	levelVar.Set(slog.LevelError)

	h, stdout, _ := newTestHandler(t, Options{Level: levelVar})

	require.False(t, h.Enabled(context.Background(), slog.LevelInfo),
		"starting at Error, Info must be disabled")

	levelVar.Set(slog.LevelDebug)

	assert.True(t, h.Enabled(context.Background(), slog.LevelInfo),
		"after lowering the LevelVar, Info must be enabled")

	slog.New(h).Info("now visible")
	assert.Contains(t, stdout.String(), "now visible")
}

func TestEnabledRespectsAStaticLevel(t *testing.T) {
	t.Parallel()

	h, _, _ := newTestHandler(t, Options{Level: slog.LevelWarn})

	assert.False(t, h.Enabled(context.Background(), slog.LevelInfo))
	assert.True(t, h.Enabled(context.Background(), slog.LevelWarn))
	assert.True(t, h.Enabled(context.Background(), slog.LevelError))
}

func TestFormats(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		format   Format
		contains string
	}{
		{name: "json", format: FormatJSON, contains: `"msg":"hello"`},
		{name: "text", format: FormatText, contains: "msg=hello"},
		{name: "empty defaults to text", format: "", contains: "msg=hello"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h, stdout, _ := newTestHandler(t, Options{Format: tc.format})
			slog.New(h).Info("hello")

			assert.Contains(t, stdout.String(), tc.contains)
		})
	}
}

func TestInvalidFormatIsRejected(t *testing.T) {
	t.Parallel()

	h, err := New(Options{Format: "yaml"}, Stdout(io.Discard), Stderr(io.Discard))

	require.Error(t, err)
	assert.Nil(t, h)
	assert.True(t, errors.Is(err, ErrInvalidFormat))
}

func TestAddSource(t *testing.T) {
	t.Parallel()

	h, stdout, _ := newTestHandler(t, Options{Format: FormatJSON, AddSource: true})
	slog.New(h).Info("located")

	assert.Contains(t, stdout.String(), `"source"`)
}

// NewStd must not error and must actually target the process streams.
func TestNewStd(t *testing.T) {
	t.Parallel()

	h, err := NewStd(Options{})

	require.NoError(t, err)
	require.NotNil(t, h)
	assert.Equal(t, slog.LevelInfo, h.level.Level(), "unset Level means Info")
	assert.Equal(t, DefaultSplitLevel, h.splitAt.Level())
}

// Naming no writers falls back to the process streams rather than discarding.
func TestUnnamedSidesFallBackToTheProcessStreams(t *testing.T) {
	t.Parallel()

	h, err := New(Options{})

	require.NoError(t, err)
	require.NotNil(t, h.stdout)
	require.NotNil(t, h.stderr)
}

func TestWithAttrsReachesBothSides(t *testing.T) {
	t.Parallel()

	h, stdout, stderr := newTestHandler(t, Options{})
	logger := slog.New(h).With("bound", "yes")

	logger.Info("inf")
	logger.Error("err")

	assert.Contains(t, stdout.String(), "bound=yes")
	assert.Contains(t, stderr.String(), "bound=yes")
}

func TestWithGroupReachesBothSides(t *testing.T) {
	t.Parallel()

	h, stdout, stderr := newTestHandler(t, Options{})
	logger := slog.New(h).WithGroup("grp")

	logger.Info("inf", "k", "v")
	logger.Error("err", "k", "v")

	assert.Contains(t, stdout.String(), "grp.k=v")
	assert.Contains(t, stderr.String(), "grp.k=v")
}

// Deriving must not let the derived handler lose its level or split settings.
func TestDerivedHandlersKeepTheirSettings(t *testing.T) {
	t.Parallel()

	h, _, _ := newTestHandler(t, Options{
		Level:   slog.LevelWarn,
		SplitAt: slog.LevelError,
	})

	for name, derived := range map[string]slog.Handler{
		"WithAttrs": h.WithAttrs([]slog.Attr{slog.String("a", "b")}),
		"WithGroup": h.WithGroup("g"),
	} {
		t.Run(name, func(t *testing.T) {
			next, ok := derived.(*Handler)
			require.True(t, ok)

			assert.Equal(t, slog.LevelWarn, next.level.Level())
			assert.Equal(t, slog.LevelError, next.splitAt.Level())
		})
	}
}

// A failing writer must surface as an error rather than being swallowed —
// the fan-out above relies on that to report which sink broke.
func TestWriteFailuresSurface(t *testing.T) {
	t.Parallel()

	h, err := New(
		Options{Level: slog.LevelDebug},
		Stdout(failingWriter{}),
		Stderr(failingWriter{}),
	)
	require.NoError(t, err)

	ctx := context.Background()

	stdoutErr := h.Handle(ctx, slog.NewRecord(recordTime(), slog.LevelInfo, "x", 0))
	require.Error(t, stdoutErr)
	assert.Contains(t, stdoutErr.Error(), "stdout handler failed")

	stderrErr := h.Handle(ctx, slog.NewRecord(recordTime(), slog.LevelError, "x", 0))
	require.Error(t, stderrErr)
	assert.Contains(t, stderrErr.Error(), "stderr handler failed")
}
