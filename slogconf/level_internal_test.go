package slogconf

import (
	"errors"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetSlogLevel(t *testing.T) {
	testCases := []struct {
		input       level
		expected    slog.Level
		expectError bool
	}{
		{levelDebug, slog.LevelDebug, false},
		{levelInfo, slog.LevelInfo, false},
		{levelWarn, slog.LevelWarn, false},
		{levelError, slog.LevelError, false},
		{"DEBUG", slog.LevelDebug, false},
		{"INFO", slog.LevelInfo, false},
		{"WARN", slog.LevelWarn, false},
		{"ERROR", slog.LevelError, false},
		{"invalid", 0, true},
	}

	for _, tc := range testCases {
		t.Run(string(tc.input), func(t *testing.T) {
			result, err := getSlogLevel(tc.input)
			if tc.expectError {
				require.Error(t, err)
				assert.True(t, errors.Is(err, ErrInvalidLogLevel), "error should wrap ErrInvalidLogLevel")

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.expected, result)
		})
	}
}
