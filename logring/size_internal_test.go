package logring

import (
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSizeAndLenOnAnEmptyRing(t *testing.T) {
	t.Parallel()

	h := New(Options{})

	assert.Zero(t, h.Size())
	assert.Zero(t, h.Len())
}

// Size and Len must agree with the same values Stats reports — they read the
// same fields, and a divergence would mean one of them is looking at something
// else.
func TestSizeAndLenAgreeWithStats(t *testing.T) {
	t.Parallel()

	h := New(Options{})
	logger := slog.New(h)

	for i := range 5 {
		logger.Info("record", "i", i)
	}

	entries, bytes, _ := h.Stats()

	assert.Equal(t, entries, h.Len())
	assert.Equal(t, bytes, h.Size())
	assert.Equal(t, 5, h.Len())
	assert.Positive(t, h.Size())
}

// Size counts everything an entry retains, not just the formatted line — the
// budget exists to bound memory, so a bigger record has to move it further.
func TestSizeGrowsWithWhatWasLogged(t *testing.T) {
	t.Parallel()

	small := New(Options{})
	slog.New(small).Info("x")

	large := New(Options{})
	slog.New(large).Info("x", "k", strings.Repeat("v", 500))

	assert.Greater(t, large.Size(), small.Size()+500,
		"the attribute has to count toward the size, not just the line")
}

// Size is what the ring bounds itself by, so it must stay under MaxBytes even
// as records keep arriving and older ones get evicted.
func TestSizeStaysUnderMaxBytes(t *testing.T) {
	t.Parallel()

	const maxBytes = 2000

	h := New(Options{MaxBytes: maxBytes})
	logger := slog.New(h)

	for i := range 200 {
		logger.Info("filling the ring", "i", i)

		require.LessOrEqual(t, h.Size(), maxBytes,
			"the ring must never exceed its own budget")
	}

	assert.Positive(t, h.Len(), "eviction must not empty the ring")
}

// Clear releases the bytes as well as the entries — a size that survived a
// Clear would report memory the ring is no longer holding.
func TestClearResetsSizeAndLen(t *testing.T) {
	t.Parallel()

	h := New(Options{})
	slog.New(h).Info("something")

	require.Positive(t, h.Size())
	require.Positive(t, h.Len())

	h.Clear()

	assert.Zero(t, h.Size())
	assert.Zero(t, h.Len())
}

// A record refused for exceeding the per-record cap must not be counted — it
// was never retained, so charging the budget for it would drift Size away from
// what the ring actually holds.
func TestDroppedRecordDoesNotCountTowardSize(t *testing.T) {
	t.Parallel()

	h := New(Options{MaxRecordBytes: 200})
	logger := slog.New(h)

	logger.Info("small")

	sizeBefore := h.Size()
	lenBefore := h.Len()

	logger.Info(strings.Repeat("x", 1000))

	_, _, dropped := h.Stats()
	require.Equal(t, uint64(1), dropped, "the big record must have been refused")

	assert.Equal(t, sizeBefore, h.Size())
	assert.Equal(t, lenBefore, h.Len())
}

// Both are plain locked reads, so they must be safe against concurrent writes.
// Run under -race.
func TestSizeAndLenAreSafeUnderConcurrentWrites(t *testing.T) {
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

	for range 500 {
		assert.GreaterOrEqual(t, h.Size(), 0)
		assert.GreaterOrEqual(t, h.Len(), 0)
	}

	close(stop)
	wg.Wait()
}
