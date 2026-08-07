package logring

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// entriesAny marks a case whose exact entry count is not deterministic — the
// JSON timestamp trims trailing zeros, so line lengths (and therefore how many
// survive a byte cap) vary run to run. Such cases assert "not emptied" instead.
const entriesAny = -1

func TestLevelIsConfigurable(t *testing.T) {
	testCases := []struct {
		name  string
		level slog.Leveler
		want  []string
	}{
		{
			name:  "nil level falls back to the INFO default",
			level: nil,
			want:  []string{"e", "w", "i"},
		},
		{
			name:  "debug keeps all",
			level: slog.LevelDebug,
			want:  []string{"e", "w", "i", "d"},
		},
		{
			name:  "info drops debug",
			level: slog.LevelInfo,
			want:  []string{"e", "w", "i"},
		},
		{
			name:  "warn drops debug and info",
			level: slog.LevelWarn,
			want:  []string{"e", "w"},
		},
		{
			name:  "error keeps only error",
			level: slog.LevelError,
			want:  []string{"e"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			h := New(Options{Level: tc.level})
			logger := slog.New(h)

			logger.Debug("d")
			logger.Info("i")
			logger.Warn("w")
			logger.Error("e")

			got := h.Search(SearchOptions{})
			require.Len(t, got, len(tc.want))

			for i, msg := range tc.want {
				assert.Contains(t, got[i].Line, `"msg":"`+msg+`"`)
			}
		})
	}
}

func TestEnabled(t *testing.T) {
	testCases := []struct {
		name    string
		ring    slog.Leveler
		queried slog.Level
		want    bool
	}{
		{
			name:    "below threshold",
			ring:    slog.LevelWarn,
			queried: slog.LevelInfo,
			want:    false,
		},
		{
			name:    "at threshold",
			ring:    slog.LevelWarn,
			queried: slog.LevelWarn,
			want:    true,
		},
		{
			name:    "above threshold",
			ring:    slog.LevelWarn,
			queried: slog.LevelError,
			want:    true,
		},
		{
			name:    "default drops debug",
			ring:    nil,
			queried: slog.LevelDebug,
			want:    false,
		},
		{
			name:    "default keeps info",
			ring:    nil,
			queried: slog.LevelInfo,
			want:    true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			h := New(Options{Level: tc.ring})

			assert.Equal(t, tc.want, h.Enabled(t.Context(), tc.queried))
		})
	}
}

func TestOutputFormat(t *testing.T) {
	testCases := []struct {
		name string
		text bool
		want string
	}{
		{name: "json by default", text: false, want: `"key":"value"`},
		{name: "text on request", text: true, want: "key=value"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			h := New(Options{Text: tc.text})
			slog.New(h).Info("hello", "key", "value")

			got := h.Search(SearchOptions{})
			require.Len(t, got, 1)
			assert.Contains(t, got[0].Line, tc.want)
		})
	}
}

// The retained line has to be the real shipped format, not an approximation —
// anything reading the ring back parses it.
func TestStoresValidJSONMatchingTheShippedFormat(t *testing.T) {
	h := New(Options{})
	slog.New(h).Info("hello", "key", "value")

	got := h.Search(SearchOptions{})
	require.Len(t, got, 1)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(got[0].Line), &parsed))
	assert.Equal(t, "hello", parsed["msg"])
	assert.Equal(t, "value", parsed["key"])
}

// The ring is bounded by BYTES, so one pathological line must not be able to
// evict a large share of it, and an oversized line is dropped outright rather
// than admitted and then allowed to evict everything else.
func TestRingBounds(t *testing.T) {
	testCases := []struct {
		name        string
		opts        Options
		recordSize  int
		records     int
		wantEntries int
		wantDropped uint64
		wantWithin  int
	}{
		{
			name:        "keeps everything under both caps",
			opts:        Options{},
			recordSize:  10,
			records:     5,
			wantEntries: 5,
			wantDropped: 0,
		},
		{
			name:        "evicts oldest once over the total cap",
			opts:        Options{MaxBytes: 400},
			recordSize:  20,
			records:     50,
			wantEntries: entriesAny,
			wantDropped: 0,
			wantWithin:  400,
		},
		{
			name:        "drops a record over the per-record cap",
			opts:        Options{MaxRecordBytes: 100},
			recordSize:  500,
			records:     3,
			wantEntries: 0,
			wantDropped: 3,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			h := New(tc.opts)
			logger := slog.New(h)

			for i := range tc.records {
				logger.Info(strings.Repeat("x", tc.recordSize), "i", i)
			}

			entries, bytes, dropped := h.Stats()

			assert.Equal(t, tc.wantDropped, dropped)

			if tc.wantEntries == entriesAny {
				assert.Positive(t, entries, "eviction must not empty the ring")
			} else {
				assert.Equal(t, tc.wantEntries, entries)
			}

			if tc.wantWithin > 0 {
				assert.LessOrEqual(t, bytes, tc.wantWithin,
					"ring must stay under its byte cap")

				got := h.Search(SearchOptions{})
				require.NotEmpty(t, got)
				assert.Contains(t, got[0].Line,
					fmt.Sprintf(`"i":%d`, tc.records-1),
					"the newest record must survive eviction")
			}
		})
	}
}

// searchCorpus seeds a ring directly so every entry has a known time and level.
// Going through Handle would stamp times at call speed, which makes a Since
// filter untestable; the Handle path is covered by the tests above.
func searchCorpus(t *testing.T, now time.Time) *Handler {
	t.Helper()

	h := New(Options{})

	for _, e := range []Entry{
		{
			Time:  now.Add(-2 * time.Hour),
			Level: slog.LevelDebug,
			Line:  "alpha",
		},
		{
			Time:  now.Add(-time.Hour),
			Level: slog.LevelInfo,
			Line:  "bravo apple",
		},
		{
			Time:  now.Add(-30 * time.Minute),
			Level: slog.LevelWarn,
			Line:  "charlie BANANA",
		},
		{
			Time:  now.Add(-time.Minute),
			Level: slog.LevelError,
			Line:  "delta cherry",
		},
	} {
		h.store.push(e)
	}

	return h
}

func TestSearch(t *testing.T) {
	now := time.Now()

	testCases := []struct {
		name string
		opts SearchOptions
		want []string
	}{
		{
			name: "no filter returns everything newest first",
			opts: SearchOptions{},
			want: []string{
				"delta cherry", "charlie BANANA", "bravo apple", "alpha",
			},
		},
		{
			name: "contains is case-insensitive on the needle",
			opts: SearchOptions{Contains: "BANANA"},
			want: []string{"charlie BANANA"},
		},
		{
			name: "contains is case-insensitive on the line",
			opts: SearchOptions{Contains: "banana"},
			want: []string{"charlie BANANA"},
		},
		{
			name: "min level keeps that level and above",
			opts: SearchOptions{MinLevel: slog.LevelWarn},
			want: []string{"delta cherry", "charlie BANANA"},
		},
		{
			name: "min level error keeps only error",
			opts: SearchOptions{MinLevel: slog.LevelError},
			want: []string{"delta cherry"},
		},
		{
			name: "since excludes anything older",
			opts: SearchOptions{Since: now.Add(-45 * time.Minute)},
			want: []string{"delta cherry", "charlie BANANA"},
		},
		{
			name: "limit caps the result",
			opts: SearchOptions{Limit: 2},
			want: []string{"delta cherry", "charlie BANANA"},
		},
		{
			name: "filters combine",
			opts: SearchOptions{MinLevel: slog.LevelWarn, Contains: "cherry"},
			want: []string{"delta cherry"},
		},
		{
			name: "limit applies after filtering",
			opts: SearchOptions{MinLevel: slog.LevelWarn, Limit: 1},
			want: []string{"delta cherry"},
		},
		{
			name: "no match returns empty",
			opts: SearchOptions{Contains: "durian"},
			want: nil,
		},
		{
			name: "since in the future returns empty",
			opts: SearchOptions{Since: now.Add(time.Hour)},
			want: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			h := searchCorpus(t, now)

			got := h.Search(tc.opts)

			lines := make([]string, 0, len(got))
			for _, e := range got {
				lines = append(lines, e.Line)
			}

			assert.Equal(t, tc.want, sliceOrNil(lines))
		})
	}
}

// sliceOrNil normalises an empty result to nil so a case can express "returns
// nothing" as a nil want without caring which empty form Search produced.
func sliceOrNil(in []string) []string {
	if len(in) == 0 {
		return nil
	}

	return in
}

// WithAttrs / WithGroup must feed the SAME ring, or attrs-carrying loggers
// (the common case in a real service) would silently write nowhere. This
// asserts a property ACROSS the derivations, so it stays one scenario rather
// than a table of independent cases.
func TestDerivedLoggersShareOneRing(t *testing.T) {
	h := New(Options{})
	base := slog.New(h)

	base.Info("from base")
	base.With("req", "abc").Info("from with")
	base.WithGroup("grp").Info("from group", "inner", 1)

	got := h.Search(SearchOptions{})
	require.Len(t, got, 3, "every derived logger must reach one ring")

	joined := strings.Join(
		[]string{got[0].Line, got[1].Line, got[2].Line}, "\n",
	)
	assert.Contains(t, joined, `"req":"abc"`)
	assert.Contains(t, joined, `"grp":{"inner":1}`)
}

func TestDerivedLoggerInheritsLevel(t *testing.T) {
	h := New(Options{Level: slog.LevelWarn})
	derived := slog.New(h).With("k", "v")

	derived.Info("dropped")
	derived.Error("kept")

	got := h.Search(SearchOptions{})
	require.Len(t, got, 1)
	assert.Contains(t, got[0].Line, "kept")
}

func TestConcurrentHandleAndSearch(t *testing.T) {
	h := New(Options{})
	logger := slog.New(h)

	var wg sync.WaitGroup

	for i := range 16 {
		wg.Add(3)

		go func() {
			defer wg.Done()

			logger.Info("concurrent", "i", i)
		}()

		go func() {
			defer wg.Done()

			logger.With("derived", i).Warn("concurrent derived")
		}()

		go func() {
			defer wg.Done()

			_ = h.Search(SearchOptions{Contains: "concurrent"})
		}()
	}

	wg.Wait()

	entries, _, _ := h.Stats()
	assert.Equal(t, 32, entries)
}

func TestZeroOptionsUseDefaults(t *testing.T) {
	h := New(Options{})

	assert.Equal(t, DefaultMaxBytes, h.store.maxBytes)
	assert.Equal(t, DefaultMaxRecordBytes, h.store.maxRecordBytes)
	assert.Equal(t, DefaultLevel, h.level.Level())
}

// A zero SearchOptions must mean "no level filter". The trap this pins:
// slog.Level(0) IS slog.LevelInfo, so typing MinLevel as a slog.Level made an
// unfiltered Search silently hide every DEBUG record the ring had retained —
// the store held them, the reader never saw them. This goes through Handle
// rather than the seeded corpus, so it covers the real logging path.
func TestZeroSearchOptionsDoesNotFilterByLevel(t *testing.T) {
	h := New(Options{Level: slog.LevelDebug})
	logger := slog.New(h)

	logger.Debug("d")
	logger.Info("i")

	entries, _, _ := h.Stats()
	require.Equal(t, 2, entries, "both records must reach the store")

	got := h.Search(SearchOptions{})
	require.Len(t, got, 2, "an unfiltered Search must return the DEBUG record")
	assert.Equal(t, slog.LevelDebug, got[1].Level)
}

// A level BELOW DebugLevel is still a level. "No filter" must not quietly mean
// "floor at DEBUG".
func TestSearchReturnsCustomSubDebugLevels(t *testing.T) {
	const levelTrace = slog.LevelDebug - 4

	h := New(Options{Level: levelTrace})
	slog.New(h).Log(context.Background(), levelTrace, "traced")

	require.Len(t, h.Search(SearchOptions{}), 1)
	assert.Empty(t, h.Search(SearchOptions{MinLevel: slog.LevelDebug}),
		"an explicit DEBUG floor must still exclude it")
}
