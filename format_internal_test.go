package slogconfigurator

import (
	"bytes"
	"errors"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetSlogHandler(t *testing.T) {
	testCases := []struct {
		input       format
		expected    string
		expectError bool
	}{
		{formatJSON, "*slog.JSONHandler", false},
		{formatText, "*slog.TextHandler", false},
		{"invalid", "", true},
	}

	buf := &bytes.Buffer{}
	opts := &slog.HandlerOptions{}

	for _, tc := range testCases {
		t.Run(string(tc.input), func(t *testing.T) {
			result, err := getSlogHandler(tc.input, buf, opts)
			if tc.expectError {
				require.Error(t, err)
				assert.Nil(t, result)
				assert.True(t, errors.Is(err, ErrInvalidLogFormat), "error should wrap ErrInvalidLogFormat")

				return
			}

			require.NoError(t, err)
			require.NotNil(t, result)
		})
	}
}
