package logring

import (
	"log/slog"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The total must count every match, not just what the page collected —
// otherwise it restates len(Entries) and paging is blind.
func TestSearchTotalCountsBeyondTheLimit(t *testing.T) {
	t.Parallel()

	h := New(Options{})
	logger := slog.New(h)

	for range 10 {
		logger.Info("noise")
	}

	page := h.Search(SearchOptions{Limit: 3})

	assert.Len(t, page.Entries, 3)
	assert.Equal(t, 10, page.Total, "Total must ignore Limit")
	assert.Zero(t, page.Offset)
}

func TestSearchOffset(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name        string
		offset      int
		limit       int
		wantEntries int
		wantTotal   int
		wantOffset  int
	}{
		{
			name: "no paging returns everything",
			// Limit 0 falls back to DefaultSearchLimit, which exceeds the ring.
			wantEntries: 10, wantTotal: 10,
		},
		{
			name: "offset skips matches but not the count",
			offset: 6, limit: 10,
			wantEntries: 4, wantTotal: 10, wantOffset: 6,
		},
		{
			name: "offset past the end yields an empty page with a real total",
			offset: 99, limit: 10,
			wantEntries: 0, wantTotal: 10, wantOffset: 99,
		},
		{
			name: "negative offset is treated as zero",
			offset: -5, limit: 10,
			wantEntries: 10, wantTotal: 10, wantOffset: 0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := New(Options{})
			logger := slog.New(h)

			for range 10 {
				logger.Info("noise")
			}

			page := h.Search(SearchOptions{
				Limit:  tc.limit,
				Offset: tc.offset,
			})

			assert.Len(t, page.Entries, tc.wantEntries)
			assert.Equal(t, tc.wantTotal, page.Total)
			assert.Equal(t, tc.wantOffset, page.Offset)
		})
	}
}

// Filters must apply to BOTH halves. A total counted without the filters would
// report the whole ring for a narrow query.
func TestSearchAppliesFiltersToTheTotal(t *testing.T) {
	t.Parallel()

	h := New(Options{})
	logger := slog.New(h)

	for range 4 {
		logger.Error("boom")
	}

	for range 6 {
		logger.Info("fine")
	}

	page := h.Search(SearchOptions{
		MinLevel: slog.LevelError,
		Limit:    2,
	})

	assert.Len(t, page.Entries, 2)
	assert.Equal(t, 4, page.Total, "the total must be filtered, not the ring size")

	for _, entry := range page.Entries {
		assert.Equal(t, slog.LevelError, entry.Level)
	}
}

// Paging must walk the matches in the same order Search would, so a caller can
// step Offset forward and see each record exactly once.
func TestSearchPagesWithoutSkippingOrRepeating(t *testing.T) {
	t.Parallel()

	const (
		records = 9
		perPage = 4
	)

	h := New(Options{})
	logger := slog.New(h)

	for i := range records {
		logger.Info("record", "i", i)
	}

	seen := map[string]int{}
	total := 0

	for offset := 0; offset < records; offset += perPage {
		page := h.Search(SearchOptions{
			Limit:     perPage,
			Offset:    offset,
			Ascending: true,
		})

		total = page.Total

		for _, entry := range page.Entries {
			value, ok := entry.Attr("i")
			require.True(t, ok)

			seen[value]++
		}
	}

	assert.Equal(t, records, total)
	assert.Len(t, seen, records, "every record must appear")

	for value, count := range seen {
		assert.Equal(t, 1, count, "record %s appeared %d times", value, count)
	}
}

func TestSearchAscendingMatchesSearch(t *testing.T) {
	t.Parallel()

	h := New(Options{})
	logger := slog.New(h)

	logger.Info("first")
	logger.Info("second")

	newest := h.Search(SearchOptions{})
	require.Len(t, newest.Entries, 2)
	assert.Equal(t, "second", newest.Entries[0].Msg)

	oldest := h.Search(SearchOptions{Ascending: true})
	require.Len(t, oldest.Entries, 2)
	assert.Equal(t, "first", oldest.Entries[0].Msg)
}

// An empty ring must report an empty page rather than a nil-entry surprise.
func TestSearchOnAnEmptyRing(t *testing.T) {
	t.Parallel()

	page := New(Options{}).Search(SearchOptions{})

	assert.Empty(t, page.Entries)
	assert.Zero(t, page.Total)
}

// The whole point: the page and its total come from ONE locked walk, so they
// always describe the same ring even while it is being written. Under -race
// this also proves the read itself is safe against concurrent Handle calls.
func TestSearchIsConsistentUnderConcurrentWrites(t *testing.T) {
	h := New(Options{})
	logger := slog.New(h)

	var wg sync.WaitGroup

	stop := make(chan struct{})

	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
				logger.Info("churn")
			}
		}
	})

	for range 200 {
		page := h.Search(SearchOptions{Limit: 5})

		// Total counts every match the walk saw; Entries is a window into
		// exactly those. The page can never claim more than the total.
		assert.LessOrEqual(t, len(page.Entries), page.Total)
	}

	close(stop)
	wg.Wait()
}
