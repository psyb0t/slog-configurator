# Changelog

All notable changes per release. Versions follow [semver](https://semver.org).

## v1.4.0 — 2026-08-08

`Search` now returns the page **and** the total it was drawn from, both counted
in one locked walk.

### Changed

- **`logring.Handler.Search` returns `Page` instead of `[]Entry`.**

  ```go
  page := ring.Search(logring.SearchOptions{Contains: "timeout", Limit: 50})
  fmt.Printf("showing %d of %d\n", len(page.Entries), page.Total)
  ```

  `Page` carries `Entries`, `Total` — the matches counted before `Limit` and
  `Offset` were applied — and the `Offset` echoed back. Existing call sites
  append `.Entries` and behave exactly as before.

  **Why the total lives here rather than in a second method.** Paging needs
  both numbers, and taking them from `Search` plus `Count` means two separate
  locked reads: on a ring that is still being written, records arrive or get
  evicted in between, so the total ends up describing a ring the page did not
  come from, and paging on top of that can skip or repeat records. Only the
  ring can hold one lock across both reads, so only the ring can make them
  agree — a caller cannot fix it from the outside, which is exactly why it is
  not left to one.

  Without the total, a full page and the last page are indistinguishable and a
  reader cannot tell whether they have seen everything. It is not an extra: it
  is what makes `Limit` and `Offset` usable at all.

  The cost is a full walk. The total is unknowable without visiting every
  entry, so the search no longer stops early once the page is full — for a
  bounded in-process debug ring, not a meaningful price.

  `Count`, `Tail`, `Clear` and `Stats` are unchanged.

## v1.3.0 — 2026-08-07

`logring` learns to search properly, and stops silently eating the ring when a
record is bigger than the ring itself.

Everything here is additive — `Entry` gains fields, `SearchOptions` gains
filters, and existing calls keep compiling and behaving the same.

### Fixed

- **A record larger than `MaxBytes` wiped the entire ring and reported nothing.**
  `MaxRecordBytes` defaults to 1 MiB regardless of `MaxBytes`, so with a ring
  smaller than that, an oversized record passed the per-record check, got
  appended, and was then evicted by the loop that enforces the byte cap —
  taking every older entry with it on the way through, and incrementing
  neither the stored count nor the dropped count. `Stats()` reported a clean,
  empty ring. `MaxRecordBytes` is now clamped to `MaxBytes` in `New`, so an
  oversized record is refused up front and counted as a drop.

- **Every stored line carried a trailing newline.** Both stdlib handlers
  terminate a record with one, and an `Entry` is already the record boundary —
  nothing concatenates lines — so it delimited nothing. It cost a byte of the
  budget per record and left every caller a character to strip before printing
  or parsing. `Entry.Line` is now stored without it.

- **The byte budget under-counted what it was bounding.** It summed only the
  formatted line, while an entry also retains its message and its attributes.
  The budget exists to bound memory, so everything the entry holds counts
  against it.

### Added

- **Attribute search that does not assume JSON.** `Entry.Attrs` carries the
  record's attributes as flat key/value pairs, captured from the `slog.Record`
  at handle time rather than parsed back out of the formatted line — so it
  behaves identically whether the ring is storing JSON or text:

  ```go
  ring.Search(logring.SearchOptions{
      Attrs: map[string]string{"request_id": "abc123"},
  })
  ```

  Attributes bound earlier through `slog.Logger.With` are included. They live
  on the inner handler and never appear on the `slog.Record`, so reading the
  record alone would have missed the single most useful thing to search by.
  Grouped attributes get dotted keys — a logger with `WithGroup("http")`
  logging `status` matches under `http.status`. `Entry.Attr(key)` looks one up.

- **More `SearchOptions` filters**: `Exclude` (a substring that disqualifies a
  line), `Match` (a compiled `*regexp.Regexp`, so a bad pattern is a
  caller-side error rather than something `Search` has to swallow), `Until`
  (upper time bound, pairing with `Since`), `Levels` (an exact set, for
  "warnings and errors but not info" — which a floor cannot express), `Offset`
  (paging), and `Ascending` (oldest first).

- **`Count(SearchOptions)`** reports how many entries match without
  materialising them — the total a paged `Search` would walk.

- **`Tail(n)`** returns the newest n entries in chronological order,
  unfiltered: the "show me what just happened" read.

- **`Clear()`** discards every retained entry. The dropped counter is a
  lifetime total and survives, so `Stats()` keeps reporting oversized records
  the ring refused.

- **`Entry.Msg`** carries the record's message on its own, so a caller can
  display or match it without pulling it back out of the formatted line.

## v1.2.1 — 2026-08-07

Documentation only. No library code changed.

- The feature list now mentions the two things v1.2.0 actually added —
  caller-named environment variables via `Init(Options{...})`, and the
  `logring` in-memory ring. They were documented in their own sections but
  missing from the list a reader skims first, so the headline features of the
  previous release were invisible unless you scrolled.
- Added a table of contents. The README had grown past the point where
  anything below the fold is findable.

## v1.2.0 — 2026-08-07

The environment variable names are yours now, and the in-memory log ring that
was living in a downstream copy of this package comes home.

Everything here is additive. The blank import behaves exactly as before, and
`AddHandler` / `SetHandlers` keep compiling unchanged at every existing call
site.

### Added

- **`Init(Options)` — configure which environment variables get read.**
  `LOG_LEVEL` / `LOG_FORMAT` / `LOG_ADD_SOURCE` remain the defaults, but a
  caller can now name its own:

  ```go
  slogconfigurator.Init(slogconfigurator.Options{
      LevelEnvVar:  "MYAPP_LOG_LEVEL",
      FormatEnvVar: "MYAPP_LOG_FORMAT",
  })
  ```

  Only the named variables are read, so a stray `LOG_LEVEL` in the environment
  cannot override the caller's own. `Options` also moves the fallbacks
  (`DefaultLevel`, `DefaultFormat`, `DefaultAddSource`) for when a sane default
  beats making every deployment set one. The zero `Options{}` reproduces the
  historical configuration exactly.

  This was previously impossible: the names lived in struct tags, which are
  fixed at compile time, so the only way to change them was to copy the whole
  package — which is what at least one consumer ended up doing.

- **`logring` — a bounded in-memory ring of recent records**, as an
  `slog.Handler` that stacks onto the fan-out:

  ```go
  ring := logring.New(logring.Options{})
  slogconfigurator.AddHandler(ring)
  entries := ring.Search(logring.SearchOptions{Contains: "timeout"})
  ```

  Bounded by BYTES rather than record count (100 MiB default), so a single
  pathological line cannot evict a hundred useful ones; records over 1 MiB are
  dropped rather than allowed to eat the ring, and `Stats()` reports how often
  that happened. Retains INFO and above by default, because a service logging
  every query at DEBUG fills any ring with traces in seconds. Handlers derived
  through `WithAttrs` / `WithGroup` share one ring.

  It is a debugging aid, not a log store — bounded, per process, and gone when
  the process dies.

- **`FanOutHandler.Len()`** reports how many handlers the fan-out dispatches to,
  so a caller can assert `AddHandler` stacked onto the existing set rather than
  replacing it. That difference is invisible from outside until logs go missing.

- **`EnvVarNameLevel` / `EnvVarNameFormat` / `EnvVarNameAddSource`** are exported,
  so a caller naming its own variables can still reference the defaults.

### Changed

- **`AddHandler` now returns `bool`** — `true` when the default logger was this
  package's fan-out. `false` means something else had replaced slog's default
  and the handler was stacked onto that instead: still added, but the
  stdout/stderr split is gone, which is worth noticing rather than discovering
  through absent logs. Discarding a result is legal Go, so every existing
  `AddHandler(h)` call site continues to compile untouched.

- **An empty environment variable now counts as unset.** An exported but empty
  `LOG_LEVEL=` in a shell profile means "I did not set this", not "configure me
  with the empty string" — the latter failed validation and panicked the
  process at import time.

- **An unparseable `LOG_ADD_SOURCE` is now a clear error** (`ErrInvalidLogAddSource`)
  naming the variable and the value, instead of silently reading as `false`.

### Removed

- **The `gonfiguration` dependency.** The three settings are read from the
  environment directly. That loader resolves names from struct tags, which is
  precisely what made them unconfigurable, and it was the package's only use of
  it. Direct dependencies are now `ctxerrors` and `testify` — worth keeping
  short for something this widely blank-imported.

## v1.1.2 — 2026-08-01

CI and repo plumbing only. No library code changed, no dependency moved.

- The repo is now mirrored to **Codeberg** as well as GitLab on every branch
  and tag push, and archived to the Wayback Machine and Software Heritage —
  both from a single `mirror-and-archive.yml`. The archive runs only for the
  default branch, tags, the monthly cron and manual dispatch, since Save Page
  Now is rate-limited; it goes through the authenticated Save Page Now API.
- Issues opened on the Codeberg and GitLab mirrors are pulled back into the
  GitHub issue tracker on a six-hourly schedule. The scheduled run jitters to
  avoid hammering both mirrors at once; a manual dispatch runs immediately.
- `.telemetry/` is ignored by git and Docker.

## v1.1.1 — 2026-07-31

CI only, no library change.

- Restores a green pipeline and the GitHub Release artifact. The shared Go
  workflow had gained a codegen-drift gate that defaulted to running
  `make generate` and failing if the tree moved afterwards. This repo generates
  nothing and has no such target, so that job failed on `v1.1.0` and the release
  step, which depends on it, was skipped along with it. The gate is now opt-in
  upstream and stays off here. The `v1.1.0` tag itself is fine and `go get`
  resolves it normally.

## v1.1.0 — 2026-07-31

A failing handler no longer silences the ones after it.

- **`FanOutHandler.Handle` now dispatches to every handler even when one
  fails**, and joins the failures instead of returning at the first. It
  previously returned early, and since slog discards whatever `Handle` returns,
  a single broken sink silently took every later sink down with it — an
  unreachable Loki server ordered before stdout would kill stdout logging with
  nothing anywhere to say why. The README already promised "every log record
  gets dispatched to all handlers"; now that is actually true.
- Errors are built with [ctxerrors](https://github.com/psyb0t/ctxerrors)
  instead of `fmt.Errorf` throughout, so a handler failure carries the file,
  line and function it came from. The two sentinels in `errors.go` are
  unchanged and still declared with `errors.New`, so `errors.Is` matching is
  unaffected.
- `github.com/psyb0t/ctxerrors` v0.4.0 is a new direct dependency, for the
  `Join` used to combine handler failures.

## v1.0.1 — 2026-07-27

Self-hosted README badges + `go fix` lint tooling.

- **Coverage / version / license badges** are self-rendered SVGs served from
  `raw.githubusercontent.com/psyb0t/slog-configurator/badges/*.svg` — no
  third-party render service. `make test-coverage` writes the coverage
  percentage to `coverage-percent.txt`, the pipeline uploads it, and a `badges`
  job bakes it into the SVG. CI status uses GitHub's native badge.
- **Go 1.26:** bumped the `go` directive to 1.26 (`go.mod` + CI).
- **Lint tooling:** `make lint` / `make lint-fix` now use Go 1.26's built-in
  `go fix` instead of the `modernize` analyzer, and the `modernize` tool
  directive is dropped from `go.mod`. No library code changed.

## v1.0.0 — 2025

Initial release — configure the stdlib `log/slog` logger from environment
variables (level, format, source), with stdout/stderr split, custom handlers,
and handler stacking. See the git tags for the pre-CHANGELOG release history.
