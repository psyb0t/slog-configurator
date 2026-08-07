# slog-configurator

[![Go Reference](https://pkg.go.dev/badge/github.com/psyb0t/slog-configurator.svg)](https://pkg.go.dev/github.com/psyb0t/slog-configurator)
[![CI](https://github.com/psyb0t/slog-configurator/actions/workflows/pipeline.yml/badge.svg?branch=main)](https://github.com/psyb0t/slog-configurator/actions/workflows/pipeline.yml)
[![coverage](https://raw.githubusercontent.com/psyb0t/slog-configurator/badges/coverage.svg)](https://github.com/psyb0t/slog-configurator/actions/workflows/pipeline.yml)
[![version](https://raw.githubusercontent.com/psyb0t/slog-configurator/badges/version.svg)](https://github.com/psyb0t/slog-configurator/tags)
[![license](https://raw.githubusercontent.com/psyb0t/slog-configurator/badges/license.svg)](LICENSE)

Welcome to `slog-configurator`, the badass sidekick for your logging adventures with Go's stdlib `log/slog`! This is the spiritual successor to [`logrus-configurator`](https://github.com/psyb0t/logrus-configurator), upgraded to use the standard library's structured logging package.

## Contents

- [What's This Shit About?](#whats-this-shit-about)
- [Features](#features)
- [Usage Example](#usage-example)
- [Name The Env Vars Yourself](#name-the-env-vars-yourself)
- [In-Memory Log Ring](#in-memory-log-ring)
  - [Searching The Ring](#searching-the-ring)
- [Advanced: Handler Management](#advanced-handler-management)
- [Testing & Quality](#testing--quality)
- [Contribute](#contribute)
- [License](#license)

## What's This Shit About?

`slog-configurator` is a Go package that whips your `slog` logger into shape without you breaking a sweat. Want to set the log level? Bam! Prefer JSON over plain text? Wham! Want to know who called the logger? Boom! It's got you covered.

It also handles **stdout/stderr separation** automatically - info and debug go to stdout, warnings and errors go to stderr. Because that's how proper logging works.

## Features

- **No-nonsense log level setting** (debug, info, warn, error - the slog way).
- **Formatting logs like a boss** with JSON or text formats - keep it structured or keep it simple.
- **Source reporting** for when you need to backtrack who messed up. It's like `CSI` for your code.
- **Automated configuration** using environment variables, because who has time for manual setup?
- **stdout/stderr separation** - errors and warnings go to stderr, everything else goes to stdout.
- **Custom handler support** for when you need to take full control of your logging pipeline.
- **Handler stacking** via `AddHandler()` - add extra handlers without nuking the existing setup, just like logrus hooks.
- **Your own env var names** via `Init(Options{...})` - `LOG_LEVEL` and friends are the defaults, not the law.
- **In-memory log ring** (`logring`) - keep the last N megabytes of records around and search them without leaving the process - by substring, regexp, level, time window, or structured attribute.

## Usage Example

Ready to rock with `slog-configurator`? Check this out.

### main.go

```go
package main

import (
	"log/slog"

	_ "github.com/psyb0t/slog-configurator"
)

func main() {
	slog.Debug("this is a debug message", "key", "value", "number", 42)
	slog.Info("this is an info message", "user", "psyb0t", "action", "testing")
	slog.Warn("this is a warning message", "warning_code", "W001")
	slog.Error("this is an error message", "error_code", "E001", "details", "something went wrong")
}
```

### Crank It Up

Get your environment dialed in:

```bash
export LOG_LEVEL="debug"        # Choose the verbosity level.
export LOG_FORMAT="text"        # Pick your poison: json or text.
export LOG_ADD_SOURCE="true"    # Decide if you want to see source location in logs.
```

Unleash the beast with:

```bash
go run main.go
```

And let the good times roll with the output:

```plaintext
time=2026-03-13T20:34:53.184Z level=DEBUG source=main.go:10 msg="this is a debug message" key=value number=42
time=2026-03-13T20:34:53.184Z level=INFO source=main.go:11 msg="this is an info message" user=psyb0t action=testing
time=2026-03-13T20:34:53.184Z level=WARN source=main.go:12 msg="this is a warning message" warning_code=W001
time=2026-03-13T20:34:53.184Z level=ERROR source=main.go:13 msg="this is an error message" error_code=E001 details="something went wrong"
```

Wanna switch it up? Change the environment variables to mix the brew.

```bash
export LOG_LEVEL="warn"
export LOG_FORMAT="json"
export LOG_ADD_SOURCE="false"
```

Then let it simmer with:

```bash
go run main.go
```

And enjoy the sweet sound of (almost) silence:

```plaintext
{"time":"2026-03-13T20:34:53.296Z","level":"WARN","msg":"this is a warning message","warning_code":"W001"}
{"time":"2026-03-13T20:34:53.296Z","level":"ERROR","msg":"this is an error message","error_code":"E001","details":"something went wrong"}
```

Whether you're in for a riot or a silent disco, `slog-configurator` is your ticket. (check out all of the supported levels in [`level.go`](level.go))

## Name The Env Vars Yourself

`LOG_LEVEL` / `LOG_FORMAT` / `LOG_ADD_SOURCE` are the defaults, not the law. If your app already namespaces its config — or those names are taken — hand `Init` the ones you want:

```go
import slogconfigurator "github.com/psyb0t/slog-configurator"

func main() {
	if err := slogconfigurator.Init(slogconfigurator.Options{
		LevelEnvVar:     "MYAPP_LOG_LEVEL",
		FormatEnvVar:    "MYAPP_LOG_FORMAT",
		AddSourceEnvVar: "MYAPP_LOG_ADD_SOURCE",
	}); err != nil {
		panic(err)
	}
}
```

Only the variables you name get read — a stray `LOG_LEVEL` in the environment won't sneak in behind yours.

You can move the fallbacks too, for when a sane default beats making every deployment set it:

```go
slogconfigurator.Init(slogconfigurator.Options{
	DefaultLevel:     "info",
	DefaultFormat:    "json",   // instead of text
	DefaultAddSource: true,
})
```

The zero `Options{}` is exactly what the blank import already does, so `Init(Options{})` re-runs the same configuration rather than a different one.

**Call it early.** The blank import configures logging at import time and `Init` replaces slog's default logger — so any logger you already derived with `slog.Logger.With` keeps pointing at the old chain. Same caveat as `AddHandler` below.

## In-Memory Log Ring

`logring` keeps the most recent records in a bounded ring so a process can search its own logs without leaving the process — handy for a `/debug/logs` endpoint or an incident hook that wants the last few hundred lines.

```go
import (
	"log/slog"

	slogconfigurator "github.com/psyb0t/slog-configurator"
	"github.com/psyb0t/slog-configurator/logring"
)

ring := logring.New(logring.Options{})
slogconfigurator.AddHandler(ring)

// later — newest first
entries := ring.Search(logring.SearchOptions{
	Contains: "timeout",
	MinLevel: slog.LevelWarn,
	Limit:    50,
})
```

It's bounded **by bytes, not record count** (100 MiB default), so one pathological 100 KB line can't evict a hundred useful ones, and any single record too big for the ring gets dropped rather than allowed to eat it — `Stats()` reports how often that's happened. It retains INFO and above by default, because a service logging every query at DEBUG fills any ring with traces in seconds and buries what you were looking for.

Each record is its own `Entry` — the ring captures per handler call, it never splits a stream on a delimiter. A message with newlines in it stays one entry, and nothing has to guess where one record ends and the next begins.

### Searching The Ring

`Search` returns newest-first, capped at 200 unless you say otherwise. Every filter is optional and they all compose:

```go
ring.Search(logring.SearchOptions{
	Contains: "timeout",                       // substring, case-insensitive
	Exclude:  "healthcheck",                   // ...but not this one
	Match:    regexp.MustCompile(`user=\d+`),  // or bring a regexp
	Attrs:    map[string]string{"request_id": "abc123"},
	MinLevel: slog.LevelWarn,                  // floor
	Levels:   []slog.Level{slog.LevelError},   // or an exact set
	Since:    start,
	Until:    end,
	Limit:    50,
	Offset:   100,                             // paging
})
```

**`Attrs` doesn't parse the stored line, so it doesn't give a shit what format you're in.** Attributes are captured off the record itself when it's handled, so field search works the same in text mode as in JSON — and it picks up the ones you bound way earlier with `logger.With(...)`, which never appear on the record at all. Grouped attrs get dotted keys, so a logger with `WithGroup("http")` logging `status` is searchable as `http.status`. `Entry.Attr("http.status")` pulls one back out.

Three more reads, for what `Search` handles badly:

```go
n := ring.Count(logring.SearchOptions{MinLevel: slog.LevelError})  // how many, without hauling them out
recent := ring.Tail(50)                                            // last 50, oldest-first, unfiltered
ring.Clear()                                                       // dump it
```

**It's a debugging aid, not a log store.** Bounded, per-process, and it dies with the process — the moment you most want the logs is the moment they're gone. Ship them somewhere durable too.

## Advanced: Handler Management

The default handler is always a `FanOutHandler` that dispatches to all registered handlers. On init, it contains a single `MultiWriterHandler` (the stdout/stderr splitter). You can stack more handlers on top or replace them all.

**A handler that shits itself doesn't take the others down with it.** Every handler gets the record regardless of what the ones before it did, and the failures come back joined. This matters because slog throws away whatever `Handle` returns — so if a fan-out bailed on the first error, an unreachable Loki server would silently kill your stdout logging too, and nothing anywhere would tell you why. Your logs go dark exactly when you need them.

```go
import slogconfigurator "github.com/psyb0t/slog-configurator"

// Add a handler without fucking up the existing setup (like logrus AddHook)
slogconfigurator.AddHandler(myDBHandler)

// Replace ALL handlers - go full nuclear (like logrus SetHooks)
slogconfigurator.SetHandlers(myHandler1, myHandler2)
```

### Adding Extra Handlers

`AddHandler` stacks a new handler on top of the existing ones. Every log record gets dispatched to all handlers. Call it multiple times and they all stack up:

**Call it early.** `slog.Logger.With` and `WithGroup` snapshot the handler chain at the moment they're called, so a logger you derived *before* the `AddHandler` never carries the new handler and silently drops those records. Add handlers before you derive anything.

It returns `true` when the default logger was this package's fan-out. `false` means something else had replaced slog's default and your handler got stacked onto that instead — still added, but you've lost the stdout/stderr split, which is worth noticing rather than discovering through missing logs:

```go
import slogconfigurator "github.com/psyb0t/slog-configurator"

// These all fire for every log record alongside the default stdout/stderr handler
slogconfigurator.AddHandler(myMetricsHandler)
slogconfigurator.AddHandler(mySlackAlertHandler)
slogconfigurator.AddHandler(myDBHandler)
```

### Replacing All Handlers

`SetHandlers` replaces everything in the fan-out, including the default stdout/stderr handler:

```go
import slogconfigurator "github.com/psyb0t/slog-configurator"

// Only these handlers will fire from now on
slogconfigurator.SetHandlers(myCustomHandler1, myCustomHandler2)
```

### Custom MultiWriterHandler

Need custom stdout/stderr writers? Create your own and add it or set it:

```go
import slogconfigurator "github.com/psyb0t/slog-configurator"

handler, err := slogconfigurator.NewMultiWriterHandler(
	"json",
	&slog.HandlerOptions{Level: slog.LevelDebug, AddSource: true},
	myStdoutWriter,
	myStderrWriter,
)

slogconfigurator.SetHandlers(handler)
```

### FanOutHandler Directly

Build complex pipelines by composing handlers:

```go
import slogconfigurator "github.com/psyb0t/slog-configurator"

fanOut := slogconfigurator.NewFanOutHandler(handler1, handler2, handler3)
slogconfigurator.SetHandlers(fanOut)
```

Perfect for when you need to:
- Send errors to external monitoring systems
- Log to databases or message queues
- Route different log levels to different destinations
- Build complex logging pipelines

The `MultiWriterHandler` handles stderr/stdout separation automatically, so your custom setup plays nice with the standard streams.

## Testing & Quality

This package is tested harder than a Nokia 3310. This shit's got **90%+ test coverage** because nobody fucks around with quality here.

### Running Tests

```bash
# Run all tests
make test

# Run tests with coverage check (fails if below 90%)
make test-coverage

# Run linting
make lint

# Run everything (dependencies, linting, tests)
make all
```

### What Gets Tested

**Core Shit:**
- Environment variable parsing and configuration
- All log levels (debug, info, warn, error)
- JSON and text formatting
- MultiWriterHandler stdout/stderr routing
- Error handling for fucked up configurations

**Advanced Shit:**
- `SetHandlers()`, `AddHandler()`, and `NewMultiWriterHandler()` API functions
- `FanOutHandler` dispatching and handler stacking
- Handler `WithAttrs()` and `WithGroup()` propagation
- Custom writers and level filtering
- Configuration edge cases and error scenarios

**Testing Philosophy:**
- Table-driven tests for consistency and maintainability
- Proper `require` vs `assert` patterns for better debugging
- 90% minimum coverage enforced in CI/CD
- Comprehensive edge case coverage

And that's damn it. You've just pimped your logger with military-grade reliability!

## Contribute

Got an idea? Throw in a PR! Found a bug? Raise an issue! Let's make `slog-configurator` as tight as your favorite jeans.

## License

It's MIT. Free as in 'do whatever the hell you want with it', just don't blame me if shit hits the fan.
