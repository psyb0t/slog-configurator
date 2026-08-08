// Package loki pushes log records to Loki's HTTP API as an slog.Handler.
//
// It composes with this module's fan-out:
//
//	client, _ := loki.NewClient()
//	handler, _ := loki.NewHandler(client, slog.LevelInfo, nil)
//	slogconf.AddHandler(handler)
//
// Pushes are best-effort and never block the caller on failure: a logging
// handler that returned errors into slog would be discarded anyway, and one
// that retried would turn an unreachable Loki into an application stall.
package loki

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/psyb0t/ctxerrors"
)

const (
	httpTimeout = 5 * time.Second

	pushPath = "/loki/api/v1/push"

	headerContentType = "Content-Type"
	contentTypeJSON   = "application/json"

	// defaultServiceLabel keeps every stream carrying a `service` label, so a
	// query that groups by it never silently drops records that lacked one.
	defaultServiceLabel = "system"
)

// The environment variables read when a config is not supplied directly.
const (
	EnvVarNameURL     = "SLOGGING_LOKI_URL"
	EnvVarNameAppName = "SLOGGING_LOKI_APPNAME"
)

// ClientConfig holds the connection settings for the Loki client.
type ClientConfig struct {
	// URL is the Loki base URL, without the push path.
	URL string
}

func (c ClientConfig) validate() error {
	if c.URL == "" {
		return ctxerrors.New("loki URL is required")
	}

	return nil
}

// Client pushes log entries directly to Loki's HTTP API.
type Client struct {
	url        string
	httpClient *http.Client
}

// NewClient creates a Loki client configured from SLOGGING_LOKI_URL.
//
// The variable is read directly rather than through a struct-tag config loader:
// it is one string, and a tag-based loader is what previously made this
// module's variable names impossible to change without forking it.
func NewClient() (*Client, error) {
	return NewClientWithConfig(ClientConfig{
		URL: os.Getenv(EnvVarNameURL),
	})
}

// NewClientWithConfig creates a Loki client from the given config.
func NewClientWithConfig(cfg ClientConfig) (*Client, error) {
	if err := cfg.validate(); err != nil {
		return nil, ctxerrors.Wrap(err, "could not validate client config")
	}

	return &Client{
		url: strings.TrimRight(cfg.URL, "/"),
		httpClient: &http.Client{
			Timeout: httpTimeout,
		},
	}, nil
}

type pushPayload struct {
	Streams []stream `json:"streams"`
}

type stream struct {
	Stream map[string]string `json:"stream"`
	Values [][]string        `json:"values"`
}

// Push sends a single log line with labels to Loki.
//
// Failures are dropped deliberately. This is called from a slog.Handler, and
// slog discards whatever Handle returns — so surfacing an error would achieve
// nothing, while blocking or retrying would let an unreachable Loki stall the
// application that is only trying to log.
func (c *Client) Push(
	ctx context.Context,
	labels map[string]string,
	line string,
) {
	timestamp := strconv.FormatInt(time.Now().UnixNano(), 10)

	payload := pushPayload{
		Streams: []stream{
			{
				Stream: labels,
				Values: [][]string{{timestamp, line}},
			},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		slog.Debug("loki payload marshal failed", "err", err)

		return
	}

	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, c.url+pushPath, bytes.NewReader(body),
	)
	if err != nil {
		slog.Debug("loki request build failed", "err", err)

		return
	}

	req.Header.Set(headerContentType, contentTypeJSON)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		slog.Debug("loki push failed", "err", err)

		return
	}

	if err := resp.Body.Close(); err != nil {
		slog.Debug("loki response close failed", "err", err)
	}
}

// PushLog formats a log entry and sends it to Loki under the given app name.
func (c *Client) PushLog(
	ctx context.Context,
	appName string,
	source string,
	level string,
	msg string,
	data map[string]any,
) {
	labels := map[string]string{
		"app":    appName,
		"source": source,
		"level":  strings.ToLower(level),
	}

	var line strings.Builder

	line.WriteString(msg)

	for key, value := range data {
		fmt.Fprintf(&line, " %s=%v", key, value)
	}

	c.Push(ctx, labels, line.String())
}

// HandlerConfig holds the settings for the slog Loki handler.
type HandlerConfig struct {
	// AppName is emitted as the `app` label on every stream.
	AppName string

	// LabelKeys are slog attribute keys that become Loki LABELS instead of
	// part of the log line.
	//
	// Choose these carefully: Loki indexes by label, and every distinct value
	// creates a new stream. A high-cardinality key like request_id would make
	// one stream per request.
	LabelKeys map[string]bool
}

func (c HandlerConfig) validate() error {
	if c.AppName == "" {
		return ctxerrors.New("loki app name is required")
	}

	return nil
}

// Handler implements slog.Handler and pushes records to Loki. Several handlers
// can share one Client.
type Handler struct {
	client    *Client
	appName   string
	labelKeys map[string]bool
	level     slog.Level
	attrs     []slog.Attr
	groups    []string
}

// NewHandler creates a Loki slog handler with the app name read from
// SLOGGING_LOKI_APPNAME.
func NewHandler(
	client *Client,
	level slog.Level,
	labelKeys map[string]bool,
) (*Handler, error) {
	return NewHandlerWithConfig(client, HandlerConfig{
		AppName:   os.Getenv(EnvVarNameAppName),
		LabelKeys: labelKeys,
	}, level)
}

// NewHandlerWithConfig creates a Loki slog handler from the given config,
// using the provided client.
func NewHandlerWithConfig(
	client *Client,
	cfg HandlerConfig,
	level slog.Level,
) (*Handler, error) {
	if err := cfg.validate(); err != nil {
		return nil, ctxerrors.Wrap(err, "could not validate handler config")
	}

	return &Handler{
		client:    client,
		appName:   cfg.AppName,
		labelKeys: cfg.LabelKeys,
		level:     level,
	}, nil
}

// Enabled reports whether the handler emits records at this level.
func (h *Handler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

// Handle renders the record into a Loki line plus labels and pushes it.
func (h *Handler) Handle(_ context.Context, r slog.Record) error {
	labels := map[string]string{
		"app":   h.appName,
		"level": strings.ToLower(r.Level.String()),
	}

	h.addSourceLabels(r, labels)

	line := &strings.Builder{}
	line.WriteString(r.Message)

	// Attrs bound through With come first, then the record's own — so a
	// per-call attr wins the label slot over one bound earlier.
	for _, attr := range h.attrs {
		h.processAttr(attr, labels, line)
	}

	r.Attrs(func(attr slog.Attr) bool {
		h.processAttr(attr, labels, line)

		return true
	})

	if _, ok := labels["service"]; !ok {
		labels["service"] = defaultServiceLabel
	}

	//nolint:contextcheck // a slog.Handler has no parent ctx to inherit
	h.client.Push(context.Background(), labels, line.String())

	return nil
}

// addSourceLabels attaches the file and function the record came from, when
// slog captured a program counter for it.
func (h *Handler) addSourceLabels(r slog.Record, labels map[string]string) {
	if r.PC == 0 {
		return
	}

	frame, _ := runtime.CallersFrames([]uintptr{r.PC}).Next()

	if frame.File != "" {
		labels["source_file"] = frame.File
	}

	if frame.Function != "" {
		labels["source_func"] = frame.Function
	}
}

// processAttr routes one attribute to either the label set or the log line.
func (h *Handler) processAttr(
	attr slog.Attr,
	labels map[string]string,
	line *strings.Builder,
) {
	key := attr.Key
	for _, group := range h.groups {
		key = group + "." + key
	}

	if h.labelKeys[key] {
		labels[key] = attr.Value.String()

		return
	}

	line.WriteString(" ")
	line.WriteString(key)
	line.WriteString("=")
	line.WriteString(attr.Value.String())
}

// WithAttrs returns a handler with the attrs bound, sharing the same client.
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := h.clone()
	next.attrs = append(slices.Clip(h.attrs), attrs...)

	return next
}

// WithGroup returns a handler with the group applied, sharing the same client.
func (h *Handler) WithGroup(name string) slog.Handler {
	next := h.clone()
	next.groups = append(slices.Clip(h.groups), name)

	return next
}

// clone copies the handler's settings. The attr and group slices are clipped by
// the callers before appending, so two handlers derived from one parent cannot
// write into the same backing array.
func (h *Handler) clone() *Handler {
	return &Handler{
		client:    h.client,
		appName:   h.appName,
		labelKeys: h.labelKeys,
		level:     h.level,
		attrs:     h.attrs,
		groups:    h.groups,
	}
}
