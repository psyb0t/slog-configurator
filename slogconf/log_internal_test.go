package slogconf

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetDefaults(t *testing.T) {
	unsetEnvs(t)
	require.NoError(t, configure(), "Unexpected error")
}

func TestConfigure(t *testing.T) {
	testCases := []struct {
		name        string
		logLevel    string
		logFormat   string
		expectError bool
	}{
		{
			name:        "Valid level and format json",
			logLevel:    "info",
			logFormat:   "json",
			expectError: false,
		},
		{
			name:        "Valid level and format text",
			logLevel:    "debug",
			logFormat:   "text",
			expectError: false,
		},
		{
			name:        "Invalid level",
			logLevel:    "invalid",
			logFormat:   "json",
			expectError: true,
		},
		{
			name:        "Invalid format",
			logLevel:    "info",
			logFormat:   "invalid",
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envVarNameLevel, tc.logLevel)
			t.Setenv(envVarNameFormat, tc.logFormat)

			err := configure()

			if tc.expectError {
				require.Error(t, err, "Expected error")

				return
			}

			require.NoError(t, err, "Unexpected error")
		})
	}
}

func TestConfigLog(t *testing.T) {
	testCases := []struct {
		name      string
		level     level
		format    format
		addSource bool
	}{
		{
			name:      "Debug config with JSON format and source reporting",
			level:     levelDebug,
			format:    formatJSON,
			addSource: true,
		},
		{
			name:      "Info config with text format and no source reporting",
			level:     levelInfo,
			format:    formatText,
			addSource: false,
		},
		{
			name:      "Error config with JSON format and source reporting",
			level:     levelError,
			format:    formatJSON,
			addSource: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			c := config{
				Level:     tc.level,
				Format:    tc.format,
				AddSource: tc.addSource,
			}

			assert.NotPanics(t, func() {
				c.log()
			}, "config.log() should not panic")
		})
	}
}

func TestConfigureErrorHandling(t *testing.T) {
	testCases := []struct {
		name         string
		logLevel     string
		logFormat    string
		logAddSource string
		expectError  bool
		errorMessage string
	}{
		{
			name:         "Invalid log level",
			logLevel:     "invalid_level",
			logFormat:    "json",
			logAddSource: "false",
			expectError:  true,
			errorMessage: "failed to get log level",
		},
		{
			name:         "Invalid log format",
			logLevel:     "info",
			logFormat:    "invalid_format",
			logAddSource: "false",
			expectError:  true,
			errorMessage: "failed to create log handler",
		},
		{
			name:         "Multiple invalid values",
			logLevel:     "invalid_level",
			logFormat:    "invalid_format",
			logAddSource: "false",
			expectError:  true,
			errorMessage: "failed to get log level",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envVarNameLevel, tc.logLevel)
			t.Setenv(envVarNameFormat, tc.logFormat)
			t.Setenv(envVarNameAddSource, tc.logAddSource)

			err := configure()

			if tc.expectError {
				require.Error(t, err, "Expected error for test case: %s", tc.name)
				assert.Contains(t, err.Error(), tc.errorMessage, "Error message should contain expected text")

				return
			}

			require.NoError(t, err, "Unexpected error for test case: %s", tc.name)
		})
	}
}

func TestConfigureSetsFanOutHandler(t *testing.T) {
	t.Setenv(envVarNameLevel, "info")
	t.Setenv(envVarNameFormat, "text")

	err := configure()
	require.NoError(t, err)

	current := slog.Default().Handler()
	_, ok := current.(*FanOutHandler)
	assert.True(t, ok, "configure should set a FanOutHandler as default")
}

func TestSetHandlersReplacesExisting(t *testing.T) {
	originalHandler := slog.Default().Handler()
	defer slog.SetDefault(slog.New(originalHandler))

	oldBuf := &bytes.Buffer{}
	newBuf := &bytes.Buffer{}

	SetHandlers(slog.NewTextHandler(oldBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	slog.Info("old handler message")
	assert.Contains(t, oldBuf.String(), "old handler message")

	oldBuf.Reset()

	SetHandlers(slog.NewTextHandler(newBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	slog.Info("new handler message")
	assert.Empty(t, oldBuf.String(), "old handler should not receive messages after SetHandlers")
	assert.Contains(t, newBuf.String(), "new handler message")
}
