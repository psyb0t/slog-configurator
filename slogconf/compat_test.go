package slogconf_test

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/psyb0t/slogging/handlers"
	"github.com/psyb0t/slogging/slogconf"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The call shapes downstream repos compile against. A signature change that
// breaks them fails HERE rather than in six other repositories.
func TestCallShapesStillCompile(t *testing.T) {
	handler := slog.NewJSONHandler(&bytes.Buffer{}, nil)

	slogconf.AddSink(handler)
	_ = slogconf.AddSink(handler)
	slogconf.SetOutput(handler)
	_ = slogconf.SetOutput(handler)
	slogconf.SetHandlers(handler)

	require.NoError(t, slogconf.Init(slogconf.Options{}))

	_, ok := slog.Default().Handler().(*handlers.FanOutHandler)
	assert.True(t, ok, "the configured default must be a fan-out")
}

// SetOutput replaces where the process prints WITHOUT discarding sinks, and
// AddSink appends WITHOUT touching the output.
//
// This is the whole reason the output occupies a reserved slot. Under one flat
// list and a single Add verb, stacking a second console handler printed every
// line twice, and replacing the console took the ring and the shipper with it.
// Both failures are silent, so they are asserted rather than documented.
func TestSetOutputReplacesOutputAndKeepsSinks(t *testing.T) {
	original := slog.Default().Handler()
	t.Cleanup(func() { slog.SetDefault(slog.New(original)) })

	firstOutput := &bytes.Buffer{}
	secondOutput := &bytes.Buffer{}
	sink := &bytes.Buffer{}

	opts := &slog.HandlerOptions{Level: slog.LevelDebug}

	slogconf.SetHandlers(slog.NewTextHandler(firstOutput, opts))
	slogconf.AddSink(slog.NewTextHandler(sink, opts))

	require.True(t, slogconf.SetOutput(slog.NewTextHandler(secondOutput, opts)))

	slog.Info("after swap")

	assert.Empty(t, firstOutput.String(),
		"the replaced output must stop receiving records")
	assert.Contains(t, secondOutput.String(), "after swap",
		"the new output must receive records")
	assert.Contains(t, sink.String(), "after swap",
		"a sink added earlier must SURVIVE replacing the output")
}

// A second output-shaped handler added as a SINK reaches both — which is the
// double-print this API exists to make avoidable, not impossible. The point is
// that AddSink and SetOutput now say which one you meant.
func TestAddSinkAppendsRatherThanReplaces(t *testing.T) {
	original := slog.Default().Handler()
	t.Cleanup(func() { slog.SetDefault(slog.New(original)) })

	output := &bytes.Buffer{}
	sink := &bytes.Buffer{}

	opts := &slog.HandlerOptions{Level: slog.LevelDebug}

	slogconf.SetHandlers(slog.NewTextHandler(output, opts))
	require.True(t, slogconf.AddSink(slog.NewTextHandler(sink, opts)))

	slog.Info("teed")

	assert.Contains(t, output.String(), "teed")
	assert.Contains(t, sink.String(), "teed")
}

// SetHandlers is the escape hatch: it discards the sinks too.
func TestSetHandlersDiscardsEverything(t *testing.T) {
	original := slog.Default().Handler()
	t.Cleanup(func() { slog.SetDefault(slog.New(original)) })

	sink := &bytes.Buffer{}
	replacement := &bytes.Buffer{}

	opts := &slog.HandlerOptions{Level: slog.LevelDebug}

	slogconf.SetHandlers(slog.NewTextHandler(&bytes.Buffer{}, opts))
	slogconf.AddSink(slog.NewTextHandler(sink, opts))
	slogconf.SetHandlers(slog.NewTextHandler(replacement, opts))

	slog.Info("after reset")

	assert.Empty(t, sink.String(), "SetHandlers must discard sinks too")
	assert.Contains(t, replacement.String(), "after reset")
}

// Both report false when something else owns slog's default, because the
// caller has then lost the stdout/stderr split without being told.
func TestReportFalseWhenTheDefaultIsForeign(t *testing.T) {
	original := slog.Default().Handler()
	t.Cleanup(func() { slog.SetDefault(slog.New(original)) })

	opts := &slog.HandlerOptions{Level: slog.LevelDebug}

	slog.SetDefault(slog.New(slog.NewTextHandler(&bytes.Buffer{}, opts)))
	assert.False(t, slogconf.AddSink(slog.NewTextHandler(&bytes.Buffer{}, opts)))

	slog.SetDefault(slog.New(slog.NewTextHandler(&bytes.Buffer{}, opts)))
	assert.False(t, slogconf.SetOutput(slog.NewTextHandler(&bytes.Buffer{}, opts)))
}

// The exported constants are part of the contract: a consumer naming its own
// variables still wants to reference the defaults.
func TestDefaultEnvVarNamesAreExported(t *testing.T) {
	for _, name := range []string{
		slogconf.EnvVarNameLevel,
		slogconf.EnvVarNameFormat,
		slogconf.EnvVarNameAddSource,
	} {
		assert.NotEmpty(t, name)
	}
}
