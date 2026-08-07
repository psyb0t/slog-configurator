package logring

import (
	"log/slog"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A record larger than the WHOLE ring used to pass the per-record check (which
// defaults to 1 MiB no matter what MaxBytes is), get appended, and then be
// evicted by the loop that bounds the ring — taking every older entry with it
// and counting as neither stored nor dropped. Stats reported a clean ring.
func TestARecordLargerThanTheRingIsDroppedWithoutWipingIt(t *testing.T) {
	t.Parallel()

	h := New(Options{MaxBytes: 400})
	logger := slog.New(h)

	for range 3 {
		logger.Info("small")
	}

	before, _, _ := h.Stats()
	require.Equal(t, 3, before, "the small records must fit")

	logger.Info(strings.Repeat("x", 1000))

	entries, bytes, dropped := h.Stats()

	assert.Equal(t, 3, entries, "an oversized record must not evict the ring")
	assert.Equal(t, uint64(1), dropped, "and it must be counted as dropped")
	assert.LessOrEqual(t, bytes, 400)
}

func TestMaxRecordBytesIsClampedToMaxBytes(t *testing.T) {
	t.Parallel()

	h := New(Options{MaxBytes: 500, MaxRecordBytes: DefaultMaxRecordBytes})

	assert.Equal(t, 500, h.store.maxRecordBytes)
}

// Both stdlib handlers terminate a record with a newline. An Entry is already
// the record boundary, so storing it wastes a byte of the budget and hands
// every caller a trailing character to strip.
func TestStoredLineCarriesNoTrailingNewline(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		text bool
	}{
		{name: "json", text: false},
		{name: "text", text: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := New(Options{Text: tc.text})
			slog.New(h).Info("hello")

			got := h.Search(SearchOptions{})
			require.Len(t, got, 1)
			assert.NotContains(t, got[0].Line, "\n")
		})
	}
}

// A record whose MESSAGE contains newlines still occupies exactly one Entry —
// the ring captures per Handle call, it never splits a stream on a delimiter.
func TestAMultiLineMessageStaysOneEntry(t *testing.T) {
	t.Parallel()

	h := New(Options{Text: true})
	slog.New(h).Info("first\nsecond\nthird")

	got := h.Search(SearchOptions{})
	require.Len(t, got, 1)
	assert.Equal(t, "first\nsecond\nthird", got[0].Msg)
}

// The attrs bound through With live on the INNER handler, and a slog.Record
// carries only the per-call ones — so a ring reading the record alone would
// miss the single most useful thing to search by.
func TestAttrsBoundThroughWithAreSearchable(t *testing.T) {
	t.Parallel()

	h := New(Options{})
	slog.New(h).With("request_id", "abc").Info("handled", "status", "500")

	got := h.Search(SearchOptions{Attrs: map[string]string{"request_id": "abc"}})
	require.Len(t, got, 1)

	value, ok := got[0].Attr("status")
	require.True(t, ok, "the per-call attr must be captured too")
	assert.Equal(t, "500", value)
}

// Attribute search must not assume the stored line is JSON. Capturing from the
// record rather than parsing the line is what makes this hold in text mode.
func TestAttributeSearchWorksInTextMode(t *testing.T) {
	t.Parallel()

	h := New(Options{Text: true})
	slog.New(h).With("request_id", "abc").Info("handled")

	got := h.Search(SearchOptions{Attrs: map[string]string{"request_id": "abc"}})
	assert.Len(t, got, 1)
}

func TestGroupedAttrsAreCapturedUnderDottedKeys(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		derive  func(*Handler) *slog.Logger
		wantKey string
	}{
		{
			name: "one group",
			derive: func(h *Handler) *slog.Logger {
				return slog.New(h).WithGroup("http")
			},
			wantKey: "http.status",
		},
		{
			name: "nested groups",
			derive: func(h *Handler) *slog.Logger {
				return slog.New(h).WithGroup("http").WithGroup("res")
			},
			wantKey: "http.res.status",
		},
		{
			name: "a group value inside a bound attr",
			derive: func(h *Handler) *slog.Logger {
				return slog.New(h).With(
					slog.Group("bound", slog.String("inner", "500")),
				)
			},
			wantKey: "bound.inner",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := New(Options{})
			tc.derive(h).Info("handled", "status", "500")

			got := h.Search(SearchOptions{
				Attrs: map[string]string{tc.wantKey: "500"},
			})
			assert.Len(t, got, 1)
		})
	}
}

// Deriving has to copy: two loggers branching off the same parent must not see
// each other's attrs.
func TestSiblingLoggersDoNotShareAttrs(t *testing.T) {
	t.Parallel()

	h := New(Options{})
	base := slog.New(h)

	base.With("who", "left").Info("l")
	base.With("who", "right").Info("r")

	left := h.Search(SearchOptions{Attrs: map[string]string{"who": "left"}})
	right := h.Search(SearchOptions{Attrs: map[string]string{"who": "right"}})

	require.Len(t, left, 1)
	require.Len(t, right, 1)
	assert.Equal(t, "l", left[0].Msg)
	assert.Equal(t, "r", right[0].Msg)
}

// filterCorpus seeds entries directly so every one has a known time, level and
// attr set. Going through Handle would stamp times at call speed, which makes
// the Since / Until filters untestable; the Handle path is covered above.
func filterCorpus(t *testing.T, now time.Time) *Handler {
	t.Helper()

	h := New(Options{})

	for _, entry := range []Entry{
		{
			Time:  now.Add(-2 * time.Hour),
			Level: slog.LevelDebug,
			Msg:   "alpha",
			Line:  "alpha",
			Attrs: []Attr{{Key: "svc", Value: "api"}},
		},
		{
			Time:  now.Add(-time.Hour),
			Level: slog.LevelInfo,
			Msg:   "bravo",
			Line:  "bravo apple",
			Attrs: []Attr{{Key: "svc", Value: "worker"}},
		},
		{
			Time:  now.Add(-30 * time.Minute),
			Level: slog.LevelWarn,
			Msg:   "charlie",
			Line:  "charlie BANANA",
			Attrs: []Attr{{Key: "svc", Value: "api"}},
		},
		{
			Time:  now.Add(-time.Minute),
			Level: slog.LevelError,
			Msg:   "delta",
			Line:  "delta cherry",
			Attrs: []Attr{{Key: "svc", Value: "api"}},
		},
	} {
		h.store.push(entry)
	}

	return h
}

// lines pulls the Line off each entry so a case can express its expectation as
// a plain, readable slice.
func lines(entries []Entry) []string {
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.Line)
	}

	return sliceOrNil(out)
}

func TestSearchFilters(t *testing.T) {
	t.Parallel()

	now := time.Now()

	testCases := []struct {
		name string
		opts SearchOptions
		want []string
	}{
		{
			name: "exclude drops a match",
			opts: SearchOptions{Exclude: "banana"},
			want: []string{"delta cherry", "bravo apple", "alpha"},
		},
		{
			name: "contains and exclude compose",
			opts: SearchOptions{Contains: "a", Exclude: "cherry"},
			want: []string{"charlie BANANA", "bravo apple", "alpha"},
		},
		{
			name: "regexp matches the line",
			opts: SearchOptions{Match: regexp.MustCompile(`^(delta|alpha)`)},
			want: []string{"delta cherry", "alpha"},
		},
		{
			name: "regexp is case-sensitive unless the pattern says otherwise",
			opts: SearchOptions{Match: regexp.MustCompile(`banana`)},
			want: nil,
		},
		{
			name: "until excludes anything newer",
			opts: SearchOptions{Until: now.Add(-45 * time.Minute)},
			want: []string{"bravo apple", "alpha"},
		},
		{
			name: "since and until bound a window",
			opts: SearchOptions{
				Since: now.Add(-90 * time.Minute),
				Until: now.Add(-15 * time.Minute),
			},
			want: []string{"charlie BANANA", "bravo apple"},
		},
		{
			name: "exact levels skip the ones between",
			opts: SearchOptions{
				Levels: []slog.Level{slog.LevelDebug, slog.LevelError},
			},
			want: []string{"delta cherry", "alpha"},
		},
		{
			name: "levels and min level both have to pass",
			opts: SearchOptions{
				Levels:   []slog.Level{slog.LevelDebug, slog.LevelError},
				MinLevel: slog.LevelWarn,
			},
			want: []string{"delta cherry"},
		},
		{
			name: "attrs match an exact pair",
			opts: SearchOptions{Attrs: map[string]string{"svc": "worker"}},
			want: []string{"bravo apple"},
		},
		{
			name: "an attr with the wrong value does not match",
			opts: SearchOptions{Attrs: map[string]string{"svc": "nope"}},
			want: nil,
		},
		{
			name: "a missing attr key does not match",
			opts: SearchOptions{Attrs: map[string]string{"absent": "x"}},
			want: nil,
		},
		{
			name: "ascending returns oldest first",
			opts: SearchOptions{Ascending: true},
			want: []string{
				"alpha", "bravo apple", "charlie BANANA", "delta cherry",
			},
		},
		{
			name: "offset skips matches",
			opts: SearchOptions{Offset: 2},
			want: []string{"bravo apple", "alpha"},
		},
		{
			name: "offset and limit page the result",
			opts: SearchOptions{Offset: 1, Limit: 2},
			want: []string{"charlie BANANA", "bravo apple"},
		},
		{
			name: "offset applies after filtering",
			opts: SearchOptions{MinLevel: slog.LevelWarn, Offset: 1},
			want: []string{"charlie BANANA"},
		},
		{
			name: "offset past the end returns nothing",
			opts: SearchOptions{Offset: 99},
			want: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := filterCorpus(t, now)

			assert.Equal(t, tc.want, lines(h.Search(tc.opts)))
		})
	}
}

func TestCount(t *testing.T) {
	t.Parallel()

	now := time.Now()

	testCases := []struct {
		name string
		opts SearchOptions
		want int
	}{
		{name: "no filter counts everything", opts: SearchOptions{}, want: 4},
		{
			name: "counts what the filter keeps",
			opts: SearchOptions{MinLevel: slog.LevelWarn},
			want: 2,
		},
		{
			name: "limit does not cap the count",
			opts: SearchOptions{Limit: 1},
			want: 4,
		},
		{
			name: "offset does not shrink the count",
			opts: SearchOptions{Offset: 3},
			want: 4,
		},
		{
			name: "no match counts zero",
			opts: SearchOptions{Contains: "durian"},
			want: 0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := filterCorpus(t, now)

			assert.Equal(t, tc.want, h.Count(tc.opts))
		})
	}
}

func TestTail(t *testing.T) {
	t.Parallel()

	now := time.Now()

	testCases := []struct {
		name string
		n    int
		want []string
	}{
		{
			name: "fewer than the ring holds",
			n:    2,
			want: []string{"charlie BANANA", "delta cherry"},
		},
		{
			name: "more than the ring holds returns everything",
			n:    99,
			want: []string{
				"alpha", "bravo apple", "charlie BANANA", "delta cherry",
			},
		},
		{name: "zero returns nothing", n: 0, want: nil},
		{name: "negative returns nothing", n: -1, want: nil},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := filterCorpus(t, now)

			assert.Equal(t, tc.want, lines(h.Tail(tc.n)),
				"Tail must read oldest to newest")
		})
	}
}

// Clear empties the ring but must NOT reset the drop counter — that is a
// lifetime total, and losing it hides an ongoing oversized-record problem.
func TestClearEmptiesTheRingButKeepsTheDropCount(t *testing.T) {
	t.Parallel()

	// Big enough to admit a short record, small enough to refuse the long one
	// below — a JSON line is ~70 bytes before the message even starts.
	h := New(Options{MaxRecordBytes: 200})
	logger := slog.New(h)

	logger.Info("kept")
	logger.Info(strings.Repeat("x", 500))

	_, _, droppedBefore := h.Stats()
	require.Equal(t, uint64(1), droppedBefore)

	h.Clear()

	entries, bytes, dropped := h.Stats()
	assert.Zero(t, entries)
	assert.Zero(t, bytes)
	assert.Equal(t, uint64(1), dropped)

	// The ring stays usable after a Clear.
	logger.Info("after")
	assert.Len(t, h.Search(SearchOptions{}), 1)
}

// A derived handler shares the store, so clearing through any of them clears
// the one ring.
func TestClearThroughADerivedHandler(t *testing.T) {
	t.Parallel()

	h := New(Options{})
	derived, ok := h.WithAttrs([]slog.Attr{slog.String("k", "v")}).(*Handler)
	require.True(t, ok)

	slog.New(h).Info("one")
	derived.Clear()

	assert.Empty(t, h.Search(SearchOptions{}))
}

// The byte budget bounds MEMORY, so what an entry retains beyond its line has
// to count against it — otherwise a record with large attrs is charged only
// for the part of itself that happens to be formatted.
//
// Seeded rather than logged on purpose: a real record repeats its attrs inside
// the formatted line, so a ring that counted ONLY the line would still look
// bigger for an attr-carrying record and the check would pass while measuring
// nothing. Decoupling Line from Attrs is what makes this assertion real.
func TestAttrsAndMessageCountTowardTheByteBudget(t *testing.T) {
	t.Parallel()

	const value = "0123456789"

	h := New(Options{})
	h.store.push(Entry{
		Line:  "line",
		Msg:   "msg",
		Attrs: []Attr{{Key: "k", Value: value}},
	})

	_, bytes, _ := h.Stats()
	assert.Equal(t, len("line")+len("msg")+len("k")+len(value), bytes)
}

// Eviction must credit back exactly what push charged. An asymmetry here would
// drift curBytes away from the truth and, once it drifted high enough, evict
// the ring down to nothing on every subsequent write.
func TestEvictionRestoresTheExactByteCount(t *testing.T) {
	t.Parallel()

	entry := Entry{
		Line:  "line",
		Msg:   "msg",
		Attrs: []Attr{{Key: "k", Value: "0123456789"}},
	}

	h := New(Options{MaxBytes: entry.size()})

	h.store.push(entry)
	h.store.push(entry)

	entries, bytes, _ := h.Stats()
	require.Equal(t, 1, entries, "the cap fits exactly one")
	assert.Equal(t, entry.size(), bytes,
		"evicting must subtract what pushing added")
}
