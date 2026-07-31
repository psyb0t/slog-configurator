package slogconfigurator

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

type errWriter struct{}

func (w *errWriter) Write(_ []byte) (int, error) {
	return 0, errors.New("write error")
}

type errHandler struct{}

func (h *errHandler) Enabled(_ context.Context, _ slog.Level) bool {
	return true
}

func (h *errHandler) Handle(_ context.Context, _ slog.Record) error {
	return errors.New("handler error")
}

func (h *errHandler) WithAttrs(_ []slog.Attr) slog.Handler {
	return h
}

func (h *errHandler) WithGroup(_ string) slog.Handler {
	return h
}

func TestNewMultiWriterHandler(t *testing.T) {
	testCases := []struct {
		name          string
		format        format
		opts          *slog.HandlerOptions
		stdout        *bytes.Buffer
		stderr        *bytes.Buffer
		nilStdout     bool
		nilStderr     bool
		expectError   bool
		expectedLevel slog.Level
	}{
		{
			name:          "text format with debug level",
			format:        formatText,
			opts:          &slog.HandlerOptions{Level: slog.LevelDebug},
			stdout:        &bytes.Buffer{},
			stderr:        &bytes.Buffer{},
			expectError:   false,
			expectedLevel: slog.LevelDebug,
		},
		{
			name:          "json format with error level",
			format:        formatJSON,
			opts:          &slog.HandlerOptions{Level: slog.LevelError},
			stdout:        &bytes.Buffer{},
			stderr:        &bytes.Buffer{},
			expectError:   false,
			expectedLevel: slog.LevelError,
		},
		{
			name:        "invalid format",
			format:      "invalid",
			opts:        &slog.HandlerOptions{},
			stdout:      &bytes.Buffer{},
			stderr:      &bytes.Buffer{},
			expectError: true,
		},
		{
			name:          "nil writers default to os.Stdout and os.Stderr",
			format:        formatText,
			opts:          &slog.HandlerOptions{},
			nilStdout:     true,
			nilStderr:     true,
			expectError:   false,
			expectedLevel: slog.LevelInfo,
		},
		{
			name:          "nil opts defaults to info level",
			format:        formatText,
			stdout:        &bytes.Buffer{},
			stderr:        &bytes.Buffer{},
			expectError:   false,
			expectedLevel: slog.LevelInfo,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr *bytes.Buffer
			if !tc.nilStdout {
				stdout = tc.stdout
			}

			if !tc.nilStderr {
				stderr = tc.stderr
			}

			handler, err := NewMultiWriterHandler(tc.format, tc.opts, stdout, stderr)
			if tc.expectError {
				require.Error(t, err)
				assert.Nil(t, handler)

				return
			}

			require.NoError(t, err)
			require.NotNil(t, handler)
			assert.Equal(t, tc.expectedLevel, handler.level)
		})
	}
}

func TestMultiWriterHandlerEnabled(t *testing.T) {
	testCases := []struct {
		name         string
		handlerLevel slog.Level
		checkLevel   slog.Level
		expected     bool
	}{
		{"debug enabled at debug level", slog.LevelDebug, slog.LevelDebug, true},
		{"info enabled at debug level", slog.LevelDebug, slog.LevelInfo, true},
		{"warn enabled at debug level", slog.LevelDebug, slog.LevelWarn, true},
		{"error enabled at debug level", slog.LevelDebug, slog.LevelError, true},
		{"debug disabled at info level", slog.LevelInfo, slog.LevelDebug, false},
		{"info enabled at info level", slog.LevelInfo, slog.LevelInfo, true},
		{"debug disabled at error level", slog.LevelError, slog.LevelDebug, false},
		{"warn disabled at error level", slog.LevelError, slog.LevelWarn, false},
		{"error enabled at error level", slog.LevelError, slog.LevelError, true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			stdout := &bytes.Buffer{}
			stderr := &bytes.Buffer{}

			handler, err := NewMultiWriterHandler(formatText, &slog.HandlerOptions{Level: tc.handlerLevel}, stdout, stderr)
			require.NoError(t, err)

			assert.Equal(t, tc.expected, handler.Enabled(context.Background(), tc.checkLevel))
		})
	}
}

func TestMultiWriterHandlerHandle(t *testing.T) {
	testCases := []struct {
		name         string
		level        slog.Level
		expectStdout bool
		expectStderr bool
	}{
		{"debug goes to stdout", slog.LevelDebug, true, false},
		{"info goes to stdout", slog.LevelInfo, true, false},
		{"warn goes to stderr", slog.LevelWarn, false, true},
		{"error goes to stderr", slog.LevelError, false, true},
	}

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	handler, err := NewMultiWriterHandler(formatText, &slog.HandlerOptions{Level: slog.LevelDebug}, stdout, stderr)
	require.NoError(t, err)

	ctx := context.Background()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			stdout.Reset()
			stderr.Reset()

			record := slog.NewRecord(time.Now(), tc.level, "test message", 0)
			err := handler.Handle(ctx, record)
			require.NoError(t, err)

			if tc.expectStdout {
				assert.Contains(t, stdout.String(), "test message")
				assert.Empty(t, stderr.String())

				return
			}

			assert.Contains(t, stderr.String(), "test message")
			assert.Empty(t, stdout.String())
		})
	}
}

func TestMultiWriterHandlerHandleErrors(t *testing.T) {
	testCases := []struct {
		name         string
		level        slog.Level
		errOnStdout  bool
		errOnStderr  bool
		errorContain string
	}{
		{
			name:         "stdout handler error on info level",
			level:        slog.LevelInfo,
			errOnStdout:  true,
			errorContain: "stdout handler failed",
		},
		{
			name:         "stderr handler error on error level",
			level:        slog.LevelError,
			errOnStderr:  true,
			errorContain: "stderr handler failed",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var stdoutW, stderrW *errWriter

			if tc.errOnStdout {
				stdoutW = &errWriter{}
			}

			if tc.errOnStderr {
				stderrW = &errWriter{}
			}

			opts := &slog.HandlerOptions{Level: slog.LevelDebug}

			var stdoutHandler, stderrHandler slog.Handler
			if stdoutW != nil {
				stdoutHandler = slog.NewTextHandler(stdoutW, opts)
			} else {
				stdoutHandler = slog.NewTextHandler(&bytes.Buffer{}, opts)
			}

			if stderrW != nil {
				stderrHandler = slog.NewTextHandler(stderrW, opts)
			} else {
				stderrHandler = slog.NewTextHandler(&bytes.Buffer{}, opts)
			}

			handler := &MultiWriterHandler{
				stdoutHandler: stdoutHandler,
				stderrHandler: stderrHandler,
				level:         slog.LevelDebug,
			}

			record := slog.NewRecord(time.Now(), tc.level, "error test", 0)
			err := handler.Handle(context.Background(), record)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.errorContain)
		})
	}
}

func TestMultiWriterHandlerWithAttrs(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	handler, err := NewMultiWriterHandler(formatText, &slog.HandlerOptions{Level: slog.LevelDebug}, stdout, stderr)
	require.NoError(t, err)

	newHandler := handler.WithAttrs([]slog.Attr{slog.String("extra", "attr")})
	require.NotNil(t, newHandler)

	mwh, ok := newHandler.(*MultiWriterHandler)
	require.True(t, ok)

	record := slog.NewRecord(time.Now(), slog.LevelInfo, "attrs test", 0)
	err = mwh.Handle(context.Background(), record)
	require.NoError(t, err)

	assert.Contains(t, stdout.String(), "extra=attr")
	assert.Contains(t, stdout.String(), "attrs test")
}

func TestMultiWriterHandlerWithGroup(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	handler, err := NewMultiWriterHandler(formatText, &slog.HandlerOptions{Level: slog.LevelDebug}, stdout, stderr)
	require.NoError(t, err)

	newHandler := handler.WithGroup("mygroup")
	require.NotNil(t, newHandler)

	mwh, ok := newHandler.(*MultiWriterHandler)
	require.True(t, ok)

	record := slog.NewRecord(time.Now(), slog.LevelInfo, "group test", 0)
	record.AddAttrs(slog.String("key", "val"))

	err = mwh.Handle(context.Background(), record)
	require.NoError(t, err)

	assert.Contains(t, stdout.String(), "mygroup.key=val")
}

func TestNewFanOutHandler(t *testing.T) {
	testCases := []struct {
		name     string
		count    int
		expected int
	}{
		{"single handler", 1, 1},
		{"two handlers", 2, 2},
		{"three handlers", 3, 3},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			handlers := make([]slog.Handler, tc.count)
			for i := range tc.count {
				handlers[i] = slog.NewTextHandler(&bytes.Buffer{}, nil)
			}

			fanOut := NewFanOutHandler(handlers...)
			require.NotNil(t, fanOut)
			assert.Len(t, fanOut.handlers, tc.expected)
		})
	}
}

func TestFanOutHandlerEnabled(t *testing.T) {
	testCases := []struct {
		name           string
		handlerLevels  []slog.Level
		checkLevel     slog.Level
		expected       bool
	}{
		{
			name:          "debug enabled when one handler accepts debug",
			handlerLevels: []slog.Level{slog.LevelWarn, slog.LevelDebug},
			checkLevel:    slog.LevelDebug,
			expected:      true,
		},
		{
			name:          "debug disabled when all handlers require higher",
			handlerLevels: []slog.Level{slog.LevelError},
			checkLevel:    slog.LevelDebug,
			expected:      false,
		},
		{
			name:          "warn disabled when only error handler",
			handlerLevels: []slog.Level{slog.LevelError},
			checkLevel:    slog.LevelWarn,
			expected:      false,
		},
		{
			name:          "error enabled on error-only handler",
			handlerLevels: []slog.Level{slog.LevelError},
			checkLevel:    slog.LevelError,
			expected:      true,
		},
		{
			name:          "info enabled when all handlers accept debug",
			handlerLevels: []slog.Level{slog.LevelDebug, slog.LevelDebug},
			checkLevel:    slog.LevelInfo,
			expected:      true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			handlers := make([]slog.Handler, len(tc.handlerLevels))
			for i, lvl := range tc.handlerLevels {
				handlers[i] = slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: lvl})
			}

			fanOut := NewFanOutHandler(handlers...)
			assert.Equal(t, tc.expected, fanOut.Enabled(context.Background(), tc.checkLevel))
		})
	}
}

func TestFanOutHandlerHandle(t *testing.T) {
	testCases := []struct {
		name          string
		handlerLevels []slog.Level
		recordLevel   slog.Level
		expectWritten []bool
	}{
		{
			name:          "dispatches to all enabled handlers",
			handlerLevels: []slog.Level{slog.LevelDebug, slog.LevelDebug},
			recordLevel:   slog.LevelInfo,
			expectWritten: []bool{true, true},
		},
		{
			name:          "skips disabled handlers",
			handlerLevels: []slog.Level{slog.LevelError, slog.LevelDebug},
			recordLevel:   slog.LevelInfo,
			expectWritten: []bool{false, true},
		},
		{
			name:          "all disabled",
			handlerLevels: []slog.Level{slog.LevelError, slog.LevelError},
			recordLevel:   slog.LevelDebug,
			expectWritten: []bool{false, false},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			bufs := make([]*bytes.Buffer, len(tc.handlerLevels))
			handlers := make([]slog.Handler, len(tc.handlerLevels))

			for i, lvl := range tc.handlerLevels {
				bufs[i] = &bytes.Buffer{}
				handlers[i] = slog.NewTextHandler(bufs[i], &slog.HandlerOptions{Level: lvl})
			}

			fanOut := NewFanOutHandler(handlers...)

			record := slog.NewRecord(time.Now(), tc.recordLevel, "fanout message", 0)
			err := fanOut.Handle(context.Background(), record)
			require.NoError(t, err)

			for i, expectWritten := range tc.expectWritten {
				if expectWritten {
					assert.Contains(t, bufs[i].String(), "fanout message", "handler %d should have output", i)

					continue
				}

				assert.Empty(t, bufs[i].String(), "handler %d should be empty", i)
			}
		})
	}
}

func TestFanOutHandlerHandleError(t *testing.T) {
	fanOut := NewFanOutHandler(&errHandler{})

	record := slog.NewRecord(time.Now(), slog.LevelInfo, "error test", 0)
	err := fanOut.Handle(context.Background(), record)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "handler 0 failed")
}

// A failing sink must not stop the ones after it. slog discards whatever
// Handle returns, so returning early on the first error would silently drop
// every later handler's output — an unreachable Loki taking stdout with it,
// with nothing to say why.
func TestFanOutHandlerHandleKeepsGoingAfterAFailure(t *testing.T) {
	testCases := []struct {
		name     string
		handlers func(working slog.Handler) []slog.Handler
	}{
		{
			name: "failing handler first",
			handlers: func(working slog.Handler) []slog.Handler {
				return []slog.Handler{&errHandler{}, working}
			},
		},
		{
			name: "failing handler last",
			handlers: func(working slog.Handler) []slog.Handler {
				return []slog.Handler{working, &errHandler{}}
			},
		},
		{
			name: "failing handlers on both sides",
			handlers: func(working slog.Handler) []slog.Handler {
				return []slog.Handler{&errHandler{}, working, &errHandler{}}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			buf := &bytes.Buffer{}
			working := slog.NewJSONHandler(buf, nil)

			fanOut := NewFanOutHandler(tc.handlers(working)...)
			record := slog.NewRecord(
				time.Now(), slog.LevelInfo, "reaches every sink", 0,
			)

			// The failures are reported...
			err := fanOut.Handle(context.Background(), record)
			require.Error(t, err)

			// ...and the working sink still got the record.
			assert.Contains(t, buf.String(), "reaches every sink")
		})
	}
}

// Every failure is reported, not just the first one found.
func TestFanOutHandlerHandleJoinsEveryFailure(t *testing.T) {
	fanOut := NewFanOutHandler(&errHandler{}, &errHandler{}, &errHandler{})

	record := slog.NewRecord(time.Now(), slog.LevelInfo, "all fail", 0)
	err := fanOut.Handle(context.Background(), record)

	require.Error(t, err)

	for _, want := range []string{
		"handler 0 failed",
		"handler 1 failed",
		"handler 2 failed",
	} {
		assert.Contains(t, err.Error(), want)
	}
}

func TestFanOutHandlerWithAttrs(t *testing.T) {
	buf := &bytes.Buffer{}

	fanOut := NewFanOutHandler(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	newHandler := fanOut.WithAttrs([]slog.Attr{slog.String("extra", "attr")})
	require.NotNil(t, newHandler)

	foh, ok := newHandler.(*FanOutHandler)
	require.True(t, ok)
	assert.Len(t, foh.handlers, 1)

	record := slog.NewRecord(time.Now(), slog.LevelInfo, "with attrs", 0)
	err := foh.Handle(context.Background(), record)
	require.NoError(t, err)

	assert.Contains(t, buf.String(), "extra=attr")
}

func TestFanOutHandlerWithGroup(t *testing.T) {
	buf := &bytes.Buffer{}

	fanOut := NewFanOutHandler(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	newHandler := fanOut.WithGroup("mygroup")
	require.NotNil(t, newHandler)

	foh, ok := newHandler.(*FanOutHandler)
	require.True(t, ok)
	assert.Len(t, foh.handlers, 1)

	record := slog.NewRecord(time.Now(), slog.LevelInfo, "with group", 0)
	record.AddAttrs(slog.String("key", "val"))

	err := foh.Handle(context.Background(), record)
	require.NoError(t, err)

	assert.Contains(t, buf.String(), "mygroup.key=val")
}

func TestSetHandlers(t *testing.T) {
	testCases := []struct {
		name          string
		handlerCount  int
		expectedCount int
	}{
		{"single handler", 1, 1},
		{"two handlers", 2, 2},
		{"three handlers", 3, 3},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			originalHandler := slog.Default().Handler()
			defer slog.SetDefault(slog.New(originalHandler))

			handlers := make([]slog.Handler, tc.handlerCount)
			for i := range tc.handlerCount {
				handlers[i] = slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelDebug})
			}

			SetHandlers(handlers...)

			current := slog.Default().Handler()
			fanOut, ok := current.(*FanOutHandler)
			require.True(t, ok)
			assert.Len(t, fanOut.handlers, tc.expectedCount)
		})
	}
}

func TestSetHandlersActuallyLogs(t *testing.T) {
	originalHandler := slog.Default().Handler()
	defer slog.SetDefault(slog.New(originalHandler))

	buf := &bytes.Buffer{}
	h := slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})

	SetHandlers(h)

	slog.Info("sethandlers test message")

	assert.Contains(t, buf.String(), "sethandlers test message")
}

func TestAddHandler(t *testing.T) {
	testCases := []struct {
		name           string
		setupFanOut    bool
		addCount       int
		expectedCount  int
	}{
		{
			name:          "add to existing FanOutHandler",
			setupFanOut:   true,
			addCount:      1,
			expectedCount: 2,
		},
		{
			name:          "add two to existing FanOutHandler",
			setupFanOut:   true,
			addCount:      2,
			expectedCount: 3,
		},
		{
			name:          "add to non-FanOutHandler wraps both",
			setupFanOut:   false,
			addCount:      1,
			expectedCount: 2,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			originalHandler := slog.Default().Handler()
			defer slog.SetDefault(slog.New(originalHandler))

			if tc.setupFanOut {
				SetHandlers(slog.NewTextHandler(&bytes.Buffer{}, nil))
			} else {
				// Set a plain handler, not a FanOutHandler
				slog.SetDefault(slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
			}

			for range tc.addCount {
				AddHandler(slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelDebug}))
			}

			current := slog.Default().Handler()
			fanOut, ok := current.(*FanOutHandler)
			require.True(t, ok, "should be a FanOutHandler after AddHandler")
			assert.Len(t, fanOut.handlers, tc.expectedCount)
		})
	}
}

func TestAddHandlerActuallyLogs(t *testing.T) {
	originalHandler := slog.Default().Handler()
	defer slog.SetDefault(slog.New(originalHandler))

	buf := &bytes.Buffer{}
	extra := slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})

	AddHandler(extra)

	slog.Info("addhandler test message")

	assert.Contains(t, buf.String(), "addhandler test message")
}
