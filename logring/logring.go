// Package logring keeps the most recent log records in a bounded in-memory
// ring so a process can search its own logs without leaving the process.
//
// It is an slog.Handler, so it composes with this repo's fan-out:
//
//	slogconfigurator.AddHandler(logring.New(logring.Options{}))
//
// The ring is a debugging aid, not a log store. It is bounded, it is per
// process, and it dies with the process — the moment you most want logs (a
// crash) is the moment it is gone. Ship logs somewhere durable as well.
package logring

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/psyb0t/ctxerrors"
)

const (
	// DefaultMaxBytes bounds the ring by the size of the records it holds
	// rather than a record count: one pathological 100 KB line must not be
	// able to evict a hundred useful ones.
	DefaultMaxBytes = 100 << 20 // 100 MiB

	// DefaultMaxRecordBytes drops any single record larger than this instead
	// of letting it evict a large share of the ring on its own.
	DefaultMaxRecordBytes = 1 << 20 // 1 MiB

	// DefaultLevel skips DEBUG by default. A service logging every SQL
	// statement at DEBUG fills any ring with query traces in seconds and
	// buries the records worth searching for.
	DefaultLevel = slog.LevelInfo

	// DefaultSearchLimit bounds an unbounded Search so a caller cannot
	// accidentally pull the whole ring into a response.
	DefaultSearchLimit = 200
)

// Entry is one captured record: the formatted line exactly as the configured
// handler wrote it, plus the fields needed to filter without re-parsing.
type Entry struct {
	Time  time.Time
	Level slog.Level
	Line  string
}

// Options configures a ring. The zero value is usable — every field falls back
// to its Default* constant.
type Options struct {
	// MaxBytes caps the total size of retained lines. <= 0 uses
	// DefaultMaxBytes.
	MaxBytes int

	// MaxRecordBytes drops any single record larger than this. <= 0 uses
	// DefaultMaxRecordBytes.
	MaxRecordBytes int

	// Level is the minimum level retained. nil uses DefaultLevel (INFO).
	Level slog.Leveler

	// Text stores human-readable lines instead of JSON. Default is JSON, so
	// the retained line matches what a JSON-configured process ships.
	Text bool
}

// store is the shared bounded buffer. Handlers derived via WithAttrs /
// WithGroup point at the SAME store, so records reach one ring no matter which
// derived logger emitted them.
type store struct {
	mu             sync.RWMutex
	entries        []Entry
	curBytes       int
	maxBytes       int
	maxRecordBytes int
	dropped        uint64
}

// Handler captures records into a bounded ring. Safe for concurrent use.
type Handler struct {
	store *store
	level slog.Leveler

	// fmtMu guards fmtBuf and inner together: the inner handler writes its
	// formatted output into fmtBuf, so the pair is only usable by one
	// goroutine at a time.
	fmtMu  *sync.Mutex
	fmtBuf *bytes.Buffer
	inner  slog.Handler
}

// New builds a ring handler. Add it to a fan-out rather than installing it as
// the only handler — on its own it captures logs and emits nothing.
func New(opts Options) *Handler {
	maxBytes := opts.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}

	maxRecordBytes := opts.MaxRecordBytes
	if maxRecordBytes <= 0 {
		maxRecordBytes = DefaultMaxRecordBytes
	}

	var level slog.Leveler = DefaultLevel
	if opts.Level != nil {
		level = opts.Level
	}

	buf := &bytes.Buffer{}

	// The inner handler is deliberately given LevelDebug: this Handler's own
	// Enabled already gates on opts.Level, and a second threshold here would
	// silently win whenever it was the stricter of the two.
	handlerOpts := &slog.HandlerOptions{Level: slog.LevelDebug}

	var inner slog.Handler
	if opts.Text {
		inner = slog.NewTextHandler(buf, handlerOpts)
	} else {
		inner = slog.NewJSONHandler(buf, handlerOpts)
	}

	return &Handler{
		store: &store{
			maxBytes:       maxBytes,
			maxRecordBytes: maxRecordBytes,
		},
		level:  level,
		fmtMu:  &sync.Mutex{},
		fmtBuf: buf,
		inner:  inner,
	}
}

// Enabled reports whether the ring retains records at this level.
func (h *Handler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level.Level()
}

// Handle formats the record and appends it, evicting oldest-first to stay
// under the byte cap.
func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	h.fmtMu.Lock()

	h.fmtBuf.Reset()

	if err := h.inner.Handle(ctx, r); err != nil {
		h.fmtMu.Unlock()

		return ctxerrors.Wrap(err, "format record for ring")
	}

	line := h.fmtBuf.String()

	h.fmtMu.Unlock()

	h.store.push(Entry{Time: r.Time, Level: r.Level, Line: line})

	return nil
}

// WithAttrs returns a handler writing to the SAME ring with the attrs applied.
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return h.derive(h.inner.WithAttrs(attrs))
}

// WithGroup returns a handler writing to the SAME ring with the group applied.
func (h *Handler) WithGroup(name string) slog.Handler {
	return h.derive(h.inner.WithGroup(name))
}

// derive builds a sibling handler around an already-derived inner handler.
//
// The buffer and its mutex are shared with the parent deliberately, not
// carelessly: slog's WithAttrs/WithGroup keep the handler's original io.Writer,
// so the derived inner handler still writes into the parent's buffer and there
// is no way to retarget it. Sharing the mutex is therefore what makes
// concurrent Handle calls across derived loggers safe. The store is shared for
// the obvious reason — every derived logger feeds one ring.
func (h *Handler) derive(inner slog.Handler) *Handler {
	return &Handler{
		store:  h.store,
		level:  h.level,
		fmtMu:  h.fmtMu,
		fmtBuf: h.fmtBuf,
		inner:  inner,
	}
}

// Search returns matching entries, newest first.
func (h *Handler) Search(opts SearchOptions) []Entry {
	return h.store.search(opts)
}

// Stats reports, in order, how many entries the ring holds, how many bytes
// they occupy, and how many records it has dropped for being oversized.
func (h *Handler) Stats() (int, int, uint64) {
	h.store.mu.RLock()
	defer h.store.mu.RUnlock()

	return len(h.store.entries), h.store.curBytes, h.store.dropped
}

// SearchOptions filters a ring read. The zero value returns everything, newest
// first, capped at DefaultSearchLimit.
type SearchOptions struct {
	// Contains keeps only lines containing this substring (case-insensitive).
	Contains string

	// MinLevel keeps only records at or above this level. nil applies no
	// level filter at all.
	//
	// It is a Leveler rather than a slog.Level on purpose: slog.Level(0) is
	// LevelInfo, so a plain slog.Level field would make the zero value of
	// SearchOptions silently hide every DEBUG record the ring retained.
	MinLevel slog.Leveler

	// Since keeps only records at or after this instant.
	Since time.Time

	// Limit caps the results. <= 0 uses DefaultSearchLimit.
	Limit int
}

func (s *store) push(e Entry) {
	size := len(e.Line)

	s.mu.Lock()
	defer s.mu.Unlock()

	if size > s.maxRecordBytes {
		s.dropped++

		return
	}

	s.entries = append(s.entries, e)
	s.curBytes += size

	for s.curBytes > s.maxBytes && len(s.entries) > 0 {
		s.curBytes -= len(s.entries[0].Line)
		s.entries[0] = Entry{}
		s.entries = s.entries[1:]
	}
}

func (s *store) search(opts SearchOptions) []Entry {
	limit := opts.Limit
	if limit <= 0 {
		limit = DefaultSearchLimit
	}

	filter := newEntryFilter(opts)

	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]Entry, 0, min(limit, len(s.entries)))

	for i := len(s.entries) - 1; i >= 0 && len(out) < limit; i-- {
		if e := s.entries[i]; filter.keep(e) {
			out = append(out, e)
		}
	}

	return out
}

// entryFilter is SearchOptions reduced to the checks search actually runs,
// resolved once up front rather than re-derived for every entry.
type entryFilter struct {
	needle   string
	minLevel slog.Level
	byLevel  bool
	since    time.Time
}

func newEntryFilter(opts SearchOptions) entryFilter {
	filter := entryFilter{
		needle: strings.ToLower(opts.Contains),
		since:  opts.Since,
	}

	// byLevel keeps "no filter" distinct from "floor at DEBUG", so a custom
	// level below DEBUG still comes back from an unfiltered Search.
	if opts.MinLevel != nil {
		filter.byLevel = true
		filter.minLevel = opts.MinLevel.Level()
	}

	return filter
}

func (f entryFilter) keep(e Entry) bool {
	if f.byLevel && e.Level < f.minLevel {
		return false
	}

	if !f.since.IsZero() && e.Time.Before(f.since) {
		return false
	}

	if f.needle != "" &&
		!strings.Contains(strings.ToLower(e.Line), f.needle) {
		return false
	}

	return true
}
