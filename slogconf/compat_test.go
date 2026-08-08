package slogconf_test

import (
	"log/slog"
	"os"
	"testing"

	"github.com/psyb0t/slogging/slogconf"
)

// AddHandler grew a bool return. Go lets a call statement discard results, so
// that is source-compatible — but "compatible in theory" is how breakages ship.
// This compiles the exact call shapes the downstream repos use, so a future
// signature change that DOES break them fails here instead of in nine other
// repos.
func TestExistingCallShapesStillCompile(t *testing.T) {
	handler := slog.NewJSONHandler(os.Stdout, nil)

	// chatz: internal/pkg/logging/file_sink.go — result discarded.
	slogconf.AddHandler(handler)

	// The same call when a caller does want the answer.
	_ = slogconf.AddHandler(handler)

	// gofindimpl: main.go — variadic, result-less.
	slogconf.SetHandlers(handler)

	// The blank-import path leaves a fan-out installed; everything above
	// depends on that staying true.
	if _, ok := slog.Default().Handler().(*slogconf.FanOutHandler); !ok {
		t.Fatal("default handler is not a FanOutHandler")
	}
}

// The exported constants are part of the contract now: a consumer naming its
// own variables still wants to reference the defaults.
func TestDefaultEnvVarNamesAreExported(t *testing.T) {
	for _, name := range []string{
		slogconf.EnvVarNameLevel,
		slogconf.EnvVarNameFormat,
		slogconf.EnvVarNameAddSource,
	} {
		if name == "" {
			t.Fatal("env var name constant is empty")
		}
	}
}
