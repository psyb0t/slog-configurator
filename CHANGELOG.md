# Changelog

All notable changes per release. Versions follow [semver](https://semver.org).

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
