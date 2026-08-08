package loki

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// capture is a stand-in Loki that records what was pushed to it.
type capture struct {
	mu       sync.Mutex
	payloads []pushPayload
	status   int
}

func (c *capture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var payload pushPayload

	_ = json.NewDecoder(r.Body).Decode(&payload)

	c.mu.Lock()
	c.payloads = append(c.payloads, payload)
	c.mu.Unlock()

	if c.status != 0 {
		w.WriteHeader(c.status)
	}
}

func (c *capture) last() (pushPayload, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.payloads) == 0 {
		return pushPayload{}, false
	}

	return c.payloads[len(c.payloads)-1], true
}

func (c *capture) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return len(c.payloads)
}

// newTestHandler wires a handler to a fake Loki and returns both.
func newTestHandler(t *testing.T, cfg HandlerConfig) (*Handler, *capture) {
	t.Helper()

	sink := &capture{}
	server := httptest.NewServer(sink)

	t.Cleanup(server.Close)

	client, err := NewClientWithConfig(ClientConfig{URL: server.URL})
	require.NoError(t, err)

	handler, err := NewHandlerWithConfig(client, cfg, slog.LevelInfo)
	require.NoError(t, err)

	return handler, sink
}

func TestClientConfigRequiresAURL(t *testing.T) {
	t.Parallel()

	_, err := NewClientWithConfig(ClientConfig{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "URL is required")
}

func TestHandlerConfigRequiresAnAppName(t *testing.T) {
	t.Parallel()

	_, err := NewHandlerWithConfig(&Client{}, HandlerConfig{}, slog.LevelInfo)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "app name is required")
}

// A trailing slash on the configured URL must not produce a double slash in
// the push path — some proxies treat that as a different route.
func TestClientTrimsTrailingSlash(t *testing.T) {
	t.Parallel()

	client, err := NewClientWithConfig(ClientConfig{URL: "http://loki:3100/"})
	require.NoError(t, err)

	assert.Equal(t, "http://loki:3100", client.url)
}

func TestNewClientReadsTheEnvironment(t *testing.T) {
	t.Setenv(EnvVarNameURL, "http://loki:3100")

	client, err := NewClient()
	require.NoError(t, err)
	assert.Equal(t, "http://loki:3100", client.url)
}

func TestNewClientWithoutTheEnvVarFails(t *testing.T) {
	t.Setenv(EnvVarNameURL, "")

	_, err := NewClient()
	require.Error(t, err)
}

func TestNewHandlerReadsTheEnvironment(t *testing.T) {
	t.Setenv(EnvVarNameAppName, "myapp")

	handler, err := NewHandler(&Client{}, slog.LevelInfo, nil)
	require.NoError(t, err)
	assert.Equal(t, "myapp", handler.appName)
}

func TestEnabledRespectsTheLevel(t *testing.T) {
	t.Parallel()

	handler := &Handler{level: slog.LevelWarn}

	assert.False(t, handler.Enabled(context.Background(), slog.LevelInfo))
	assert.True(t, handler.Enabled(context.Background(), slog.LevelWarn))
	assert.True(t, handler.Enabled(context.Background(), slog.LevelError))
}

// The app name and level are always labels; everything else lands in the line
// unless it was named in LabelKeys.
func TestHandleSplitsLabelsFromTheLine(t *testing.T) {
	t.Parallel()

	handler, sink := newTestHandler(t, HandlerConfig{
		AppName:   "myapp",
		LabelKeys: map[string]bool{"tenant": true},
	})

	slog.New(handler).Warn("upstream slow", "tenant", "acme", "ms", 1900)

	payload, ok := sink.last()
	require.True(t, ok, "the record must have been pushed")
	require.Len(t, payload.Streams, 1)

	labels := payload.Streams[0].Stream
	assert.Equal(t, "myapp", labels["app"])
	assert.Equal(t, "warn", labels["level"])
	assert.Equal(t, "acme", labels["tenant"], "a LabelKeys attr becomes a label")

	line := payload.Streams[0].Values[0][1]
	assert.Contains(t, line, "upstream slow")
	assert.Contains(t, line, "ms=1900", "a non-label attr belongs in the line")
	assert.NotContains(t, line, "tenant=acme", "a label must NOT be duplicated into the line")
}

// Every stream carries a service label, so a query grouping by it cannot
// silently drop records that never set one.
func TestHandleAlwaysSetsAServiceLabel(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name      string
		labelKeys map[string]bool
		attrs     []any
		want      string
	}{
		{name: "defaulted when absent", want: defaultServiceLabel},
		{
			name:      "caller's value wins",
			labelKeys: map[string]bool{"service": true},
			attrs:     []any{"service", "billing"},
			want:      "billing",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			handler, sink := newTestHandler(t, HandlerConfig{
				AppName:   "myapp",
				LabelKeys: tc.labelKeys,
			})

			slog.New(handler).Info("hello", tc.attrs...)

			payload, ok := sink.last()
			require.True(t, ok)
			assert.Equal(t, tc.want, payload.Streams[0].Stream["service"])
		})
	}
}

// Attrs bound through With must survive onto every later record, and a group
// must prefix the keys that follow it.
func TestWithAttrsAndWithGroup(t *testing.T) {
	t.Parallel()

	handler, sink := newTestHandler(t, HandlerConfig{
		AppName:   "myapp",
		LabelKeys: map[string]bool{"http.status": true},
	})

	slog.New(handler).
		With("request_id", "abc123").
		WithGroup("http").
		Info("done", "status", "500")

	payload, ok := sink.last()
	require.True(t, ok)

	line := payload.Streams[0].Values[0][1]
	assert.Contains(t, line, "request_id=abc123", "a With-bound attr must survive")
	assert.Equal(t, "500", payload.Streams[0].Stream["http.status"],
		"a group must prefix the key, and the dotted key must match LabelKeys")
}

// Deriving must not let siblings scribble on each other's attrs.
func TestSiblingHandlersDoNotShareAttrs(t *testing.T) {
	t.Parallel()

	handler, sink := newTestHandler(t, HandlerConfig{AppName: "myapp"})

	base := slog.New(handler)
	base.With("who", "left").Info("l")
	base.With("who", "right").Info("r")

	require.Equal(t, 2, sink.count())

	payload, _ := sink.last()
	line := payload.Streams[0].Values[0][1]

	assert.Contains(t, line, "who=right")
	assert.NotContains(t, line, "who=left",
		"the second handler must not carry the first's attrs")
}

// A push carries a nanosecond timestamp Loki can order by.
func TestPushSendsANanosecondTimestamp(t *testing.T) {
	t.Parallel()

	handler, sink := newTestHandler(t, HandlerConfig{AppName: "myapp"})

	slog.New(handler).Info("hello")

	payload, ok := sink.last()
	require.True(t, ok)

	timestamp := payload.Streams[0].Values[0][0]
	assert.Len(t, timestamp, 19, "unix nanoseconds is a 19-digit value")
}

func TestPushLog(t *testing.T) {
	t.Parallel()

	sink := &capture{}
	server := httptest.NewServer(sink)

	t.Cleanup(server.Close)

	client, err := NewClientWithConfig(ClientConfig{URL: server.URL})
	require.NoError(t, err)

	client.PushLog(
		context.Background(), "myapp", "cron", "ERROR", "job failed",
		map[string]any{"attempt": 3},
	)

	payload, ok := sink.last()
	require.True(t, ok)

	labels := payload.Streams[0].Stream
	assert.Equal(t, "myapp", labels["app"])
	assert.Equal(t, "cron", labels["source"])
	assert.Equal(t, "error", labels["level"], "the level must be lowercased")

	assert.Contains(t, payload.Streams[0].Values[0][1], "attempt=3")
}

// An unreachable Loki must not surface an error or block. slog discards what
// Handle returns, so an error would achieve nothing — and a retry would let a
// dead Loki stall the application that is only trying to log.
func TestAnUnreachableLokiIsSwallowed(t *testing.T) {
	t.Parallel()

	client, err := NewClientWithConfig(ClientConfig{
		URL: "http://127.0.0.1:1", // nothing listens here
	})
	require.NoError(t, err)

	handler, err := NewHandlerWithConfig(
		client, HandlerConfig{AppName: "myapp"}, slog.LevelInfo,
	)
	require.NoError(t, err)

	assert.NotPanics(t, func() {
		require.NoError(t, handler.Handle(context.Background(),
			slog.NewRecord(time.Now(), slog.LevelInfo, "into the void", 0)))
	})
}

// A non-2xx response is equally not the caller's problem.
func TestAServerErrorIsSwallowed(t *testing.T) {
	t.Parallel()

	sink := &capture{status: http.StatusInternalServerError}
	server := httptest.NewServer(sink)

	t.Cleanup(server.Close)

	client, err := NewClientWithConfig(ClientConfig{URL: server.URL})
	require.NoError(t, err)

	handler, err := NewHandlerWithConfig(
		client, HandlerConfig{AppName: "myapp"}, slog.LevelInfo,
	)
	require.NoError(t, err)

	require.NoError(t, handler.Handle(context.Background(),
		slog.NewRecord(time.Now(), slog.LevelInfo, "hello", 0)))

	assert.Equal(t, 1, sink.count())
}

// slog captures a program counter when it has one; the source labels come off
// it, and a zero PC must simply omit them rather than emit empty labels.
func TestSourceLabels(t *testing.T) {
	t.Parallel()

	handler, sink := newTestHandler(t, HandlerConfig{AppName: "myapp"})

	// Going through slog.Logger populates the PC.
	slog.New(handler).Info("with source")

	payload, ok := sink.last()
	require.True(t, ok)
	assert.NotEmpty(t, payload.Streams[0].Stream["source_file"])
	assert.NotEmpty(t, payload.Streams[0].Stream["source_func"])

	// A hand-built record with PC 0 has no source to report.
	require.NoError(t, handler.Handle(context.Background(),
		slog.NewRecord(time.Now(), slog.LevelInfo, "no source", 0)))

	payload, ok = sink.last()
	require.True(t, ok)
	assert.NotContains(t, payload.Streams[0].Stream, "source_file")
	assert.NotContains(t, payload.Streams[0].Stream, "source_func")
}

func TestLineStartsWithTheMessage(t *testing.T) {
	t.Parallel()

	handler, sink := newTestHandler(t, HandlerConfig{AppName: "myapp"})

	slog.New(handler).Info("the message", "k", "v")

	payload, ok := sink.last()
	require.True(t, ok)

	assert.True(t, strings.HasPrefix(payload.Streams[0].Values[0][1], "the message"))
}
