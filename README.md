# slogging

[![Go Reference](https://pkg.go.dev/badge/github.com/psyb0t/slogging.svg)](https://pkg.go.dev/github.com/psyb0t/slogging)
[![CI](https://github.com/psyb0t/slogging/actions/workflows/pipeline.yml/badge.svg?branch=main)](https://github.com/psyb0t/slogging/actions/workflows/pipeline.yml)
[![coverage](https://raw.githubusercontent.com/psyb0t/slogging/badges/coverage.svg)](https://github.com/psyb0t/slogging/actions/workflows/pipeline.yml)
[![version](https://raw.githubusercontent.com/psyb0t/slogging/badges/version.svg)](https://github.com/psyb0t/slogging/tags)
[![license](https://raw.githubusercontent.com/psyb0t/slogging/badges/license.svg)](LICENSE)

Everything for Go's stdlib `log/slog` in one place: configure it from the environment, and stack handlers onto it that ship logs to Loki or keep them searchable in memory.

Spiritual successor to [`logrus-configurator`](https://github.com/psyb0t/logrus-configurator), and the continuation of `slog-configurator` — see [Migrating](#migrating-from-slog-configurator).

## Contents

- [Layout](#layout)
- [Quick Start](#quick-start)
- [slogconf](#slogconf)
- [handlers/logring](#handlerslogring)
- [handlers/loki](#handlersloki)
- [Migrating From slog-configurator](#migrating-from-slog-configurator)
- [Dev Workflow](#dev-workflow)
- [License](#license)

## Layout

```
slogconf/              configure slog from the environment
handlers/logring/      bounded in-memory ring you can search
handlers/loki/         push records to Loki's HTTP API
```

Runtime dependencies are `ctxerrors` and nothing else — no config loader, no HTTP framework. Every handler here talks to `log/slog` and the standard library.

## Quick Start

```bash
go get github.com/psyb0t/slogging
```

```go
package main

import (
	"log/slog"

	_ "github.com/psyb0t/slogging/slogconf" // configures slog at init time
)

func main() {
	slog.Info("this is an info message", "user", "psyb0t", "action", "testing")
	slog.Error("this is an error message", "error_code", "E001")
}
```

```bash
export LOG_LEVEL="debug"      # debug / info / warn / error
export LOG_FORMAT="json"      # json / text
export LOG_ADD_SOURCE="true"  # include source file/line/function
go run main.go
```

```json
{"time":"2026-08-08T20:34:53.296Z","level":"INFO","msg":"this is an info message","user":"psyb0t","action":"testing"}
```

**stdout/stderr are split automatically** — info and debug to stdout, warnings and errors to stderr. Container log collectors capture both and tag them, so error noise stays separate from happy-path noise.

## slogconf

The blank import does everything above. Reach for the API when you need more.

**Name the env vars yourself.** `LOG_LEVEL` and friends are the defaults, not the law:

```go
import "github.com/psyb0t/slogging/slogconf"

if err := slogconf.Init(slogconf.Options{
	LevelEnvVar:  "MYAPP_LOG_LEVEL",
	FormatEnvVar: "MYAPP_LOG_FORMAT",
}); err != nil {
	panic(err)
}
```

Only the variables you name get read, so a stray `LOG_LEVEL` can't sneak in behind yours. `DefaultLevel` / `DefaultFormat` / `DefaultAddSource` move the fallbacks. The zero `Options{}` is exactly what the blank import does.

**Call it early.** `slog.Logger.With` snapshots the handler chain when called, so a logger derived before `Init` keeps pointing at the old one.

**Stacking handlers.** The default is a `FanOutHandler` dispatching to everything registered:

```go
slogconf.AddHandler(myHandler)             // stack alongside, like a logrus hook
slogconf.SetHandlers(handlerA, handlerB)   // replace everything
```

A handler that fails doesn't take the others down with it — every handler gets the record regardless, and failures come back joined. slog discards whatever `Handle` returns, so a fan-out that bailed on the first error would let an unreachable Loki silently kill stdout logging with nothing to say why.

`AddHandler` returns `false` when something else had already replaced slog's default: still added, but the stdout/stderr split is gone, which is worth noticing rather than discovering through missing logs.

## handlers/logring

A bounded in-memory ring, so a process can answer "what just happened" without leaving the process.

```go
import (
	"github.com/psyb0t/slogging/handlers/logring"
	"github.com/psyb0t/slogging/slogconf"
)

ring := logring.New(logring.Options{})
slogconf.AddHandler(ring) // the ring IS the slog.Handler

page := ring.Search(logring.SearchOptions{
	Attrs:    map[string]string{"request_id": "abc123"},
	MinLevel: slog.LevelWarn,
	Limit:    50,
})

fmt.Printf("showing %d of %d\n", len(page.Entries), page.Total)
```

`Search` returns the entries plus `Total` — the match count *before* `Limit` and `Offset`, counted in the same locked walk. Without it, a full page and the last page are indistinguishable.

**`Attrs` matches structured attributes captured off the record**, not a substring of the line — so it behaves the same in text or JSON mode, and finds attributes bound upstream via `logger.With(...)` that never appear on the record at all. Grouped attrs use dotted keys: `WithGroup("http")` logging `status` matches `http.status`.

Also filters on `Contains`, `Exclude`, `Match` (a compiled `*regexp.Regexp`), `Levels`, `Since`, `Until`, `Offset`, `Ascending`.

How full it is:

```go
bytes := ring.Size()            // what the ring bounds itself by
count := ring.Len()             // how many records that is
n, b, dropped := ring.Stats()   // all three under one lock
recent := ring.Tail(50)         // newest 50, oldest-first, unfiltered
```

Bounded **by bytes, not record count** (100 MiB default), so one pathological 100 KB line can't evict a hundred useful ones. `Size()` is the number to watch — it decides when older records start disappearing. Nonzero `dropped` means records were refused for exceeding the per-record cap, so a search is running over an incomplete picture.

**It's a debugging aid, not a log store** — per-process, bounded, and gone when the process dies. Ship logs somewhere durable too.

## handlers/loki

```go
import (
	"github.com/psyb0t/slogging/handlers/loki"
	"github.com/psyb0t/slogging/slogconf"
)

client, err := loki.NewClient() // reads SLOGGING_LOKI_URL
if err != nil {
	panic(err)
}

handler, err := loki.NewHandler( // reads SLOGGING_LOKI_APPNAME
	client,
	slog.LevelInfo,
	map[string]bool{"tenant": true}, // these attrs become Loki LABELS
)
if err != nil {
	panic(err)
}

slogconf.AddHandler(handler)
```

`NewClientWithConfig` / `NewHandlerWithConfig` take the same settings directly when you'd rather not use the environment.

**Choose `LabelKeys` carefully.** Loki indexes by label and every distinct value creates a new stream, so a high-cardinality key like `request_id` means one stream per request. Attributes not named there go into the log line instead. `app`, `level` and `service` are always labels.

**Pushes are best-effort and never block.** An unreachable Loki, a malformed payload, a 500 — all dropped with a `Debug` line. slog discards whatever `Handle` returns, so surfacing an error achieves nothing, and retrying would let a dead Loki stall the application that's only trying to log.

## Migrating From slog-configurator

This module was `github.com/psyb0t/slog-configurator` through v1.5.0. Those versions keep resolving under the old path, so nothing breaks until you move.

| before | after |
|---|---|
| `_ "github.com/psyb0t/slog-configurator"` | `_ "github.com/psyb0t/slogging/slogconf"` |
| `slogconfigurator.Init(...)` | `slogconf.Init(...)` |
| `github.com/psyb0t/slog-configurator/logring` | `github.com/psyb0t/slogging/handlers/logring` |
| `github.com/psyb0t/common-go/slogging/loki` | `github.com/psyb0t/slogging/handlers/loki` |

Every exported name is unchanged — only import paths and the package name move.

The Loki handler additionally drops its `gonfiguration` and `common-go` dependencies. `NewClient` and `NewHandler` read the same `SLOGGING_LOKI_URL` / `SLOGGING_LOKI_APPNAME` variables as before, just via the standard library.

## Dev Workflow

```bash
make test           # all tests with -race
make test-coverage  # coverage gate (fails under 90%)
make lint           # go fix + golangci-lint
make lint-fix       # same, with --fix
```

See `make help` for the full list.

## License

MIT. See [LICENSE](LICENSE).

See [CHANGELOG.md](CHANGELOG.md) for release notes.
