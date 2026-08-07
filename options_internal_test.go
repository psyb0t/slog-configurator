package slogconfigurator

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The point of Options: a caller can name the variables. Before this existed
// the names lived in struct tags, so the only way to change them was to copy
// the whole package — which is exactly what one consumer ended up doing.
func TestInitReadsTheEnvVarNamesTheCallerAsks(t *testing.T) {
	unsetEnvs(t)

	t.Setenv("MYAPP_LOGGING_LEVEL", "error")
	t.Setenv("MYAPP_LOGGING_FORMAT", "json")
	t.Setenv("MYAPP_LOGGING_ADDSOURCE", "true")

	opts := Options{
		LevelEnvVar:     "MYAPP_LOGGING_LEVEL",
		FormatEnvVar:    "MYAPP_LOGGING_FORMAT",
		AddSourceEnvVar: "MYAPP_LOGGING_ADDSOURCE",
	}

	c, err := readConfig(opts.withDefaults())
	require.NoError(t, err)

	assert.Equal(t, levelError, c.Level)
	assert.Equal(t, formatJSON, c.Format)
	assert.True(t, c.AddSource)

	require.NoError(t, Init(opts))
}

// The custom names must be the ONLY ones consulted: if the defaults were still
// read as a fallback, a stray LOG_LEVEL in the environment would quietly
// override the caller's own variable.
func TestInitIgnoresTheDefaultNamesWhenCustomOnesAreGiven(t *testing.T) {
	unsetEnvs(t)

	t.Setenv(EnvVarNameLevel, "error")
	t.Setenv("MYAPP_LOGGING_LEVEL", "debug")

	c, err := readConfig(Options{
		LevelEnvVar: "MYAPP_LOGGING_LEVEL",
	}.withDefaults())
	require.NoError(t, err)

	assert.Equal(
		t, levelDebug, c.Level,
		"the caller's variable must win over the package default name",
	)
}

// The zero Options has to reproduce the historical behaviour exactly, because
// the blank import depends on it and nine repos use that.
func TestZeroOptionsReproducesTheHistoricalDefaults(t *testing.T) {
	unsetEnvs(t)

	opts := Options{}.withDefaults()

	assert.Equal(t, EnvVarNameLevel, opts.LevelEnvVar)
	assert.Equal(t, EnvVarNameFormat, opts.FormatEnvVar)
	assert.Equal(t, EnvVarNameAddSource, opts.AddSourceEnvVar)

	c, err := readConfig(opts)
	require.NoError(t, err)

	assert.Equal(t, defaultLevel, c.Level)
	assert.Equal(t, defaultFormat, c.Format)
	assert.False(t, c.AddSource)
}

func TestOptionDefaultsApplyWhenTheVariableIsUnset(t *testing.T) {
	unsetEnvs(t)

	c, err := readConfig(Options{
		DefaultLevel:     "error",
		DefaultFormat:    "json",
		DefaultAddSource: true,
	}.withDefaults())
	require.NoError(t, err)

	assert.Equal(t, levelError, c.Level)
	assert.Equal(t, formatJSON, c.Format)
	assert.True(t, c.AddSource)
}

// An exported-but-empty variable means "not set", not "configure me with the
// empty string" — the latter fails validation and, at import time, panics the
// process.
func TestAnEmptyVariableFallsBackToTheDefault(t *testing.T) {
	unsetEnvs(t)

	t.Setenv(EnvVarNameLevel, "")
	t.Setenv(EnvVarNameFormat, "")
	t.Setenv(EnvVarNameAddSource, "")

	c, err := readConfig(Options{}.withDefaults())
	require.NoError(t, err)

	assert.Equal(t, defaultLevel, c.Level)
	assert.Equal(t, defaultFormat, c.Format)
	assert.False(t, c.AddSource)
}

func TestUnparseableAddSourceIsAnError(t *testing.T) {
	unsetEnvs(t)

	t.Setenv(EnvVarNameAddSource, "yes-please")

	_, err := readConfig(Options{}.withDefaults())

	require.ErrorIs(t, err, ErrInvalidLogAddSource)
}

func TestAddSourceAcceptsTheFormsStrconvDoes(t *testing.T) {
	testCases := []struct {
		name string
		raw  string
		want bool
	}{
		{"true", "true", true},
		{"1", "1", true},
		{"TRUE", "TRUE", true},
		{"false", "false", false},
		{"0", "0", false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			unsetEnvs(t)
			t.Setenv(EnvVarNameAddSource, tc.raw)

			c, err := readConfig(Options{}.withDefaults())
			require.NoError(t, err)

			assert.Equal(t, tc.want, c.AddSource)
		})
	}
}

func TestInitSurfacesAnInvalidLevel(t *testing.T) {
	unsetEnvs(t)

	t.Setenv("WEIRD_LEVEL", "not-a-level")

	err := Init(Options{LevelEnvVar: "WEIRD_LEVEL"})

	require.ErrorIs(t, err, ErrInvalidLogLevel)
}

// Init has to leave a working fan-out behind, or AddHandler silently stops
// stacking onto this package's handler.
func TestInitLeavesAFanOutInstalled(t *testing.T) {
	unsetEnvs(t)

	require.NoError(t, Init(Options{}))

	_, ok := slog.Default().Handler().(*FanOutHandler)
	assert.True(t, ok)
}
