package slogconf

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func unsetEnvs(t *testing.T) {
	t.Helper()

	require.NoError(t, os.Unsetenv(envVarNameLevel), "Unexpected error")
	require.NoError(t, os.Unsetenv(envVarNameFormat), "Unexpected error")
	require.NoError(t, os.Unsetenv(envVarNameAddSource), "Unexpected error")
}
