# App Directory Consolidation — Design

**Date:** 2026-05-22
**Status:** Approved (design)

## Problem

Starshp's per-user files are scattered, and a recently added `CONFIG_PATH`
knob made it worse:

- `app.db`, `rag.db`, and `library/` live in `%APPDATA%\starshp_app`
  (`os.UserConfigDir()/starshp_app`), set by `main.go`'s `dataDir()`.
- `.env` is loaded from a working-directory-relative path
  (`config.Load(".env")`), so it resolves differently under `wails dev`
  (repo root) than under a packaged build (wherever the `.exe` launched).
- `models.yaml` / `textbooks.yaml` resolve via `TEXTBOOKS_CONFIG` /
  `MODELS_CONFIG`, which default to bare relative names — also
  working-directory-dependent.
- `CONFIG_PATH` (added to point those YAMLs at an explicit directory)
  currently names an empty directory, so the app cannot find `models.yaml`.

The result: config is split across the repo tree and two user-profile
directories, and a packaged build cannot reliably locate `.env` or the YAMLs.

## Goals

- One directory holds every per-user file: `.env`, `models.yaml`,
  `textbooks.yaml`, textbook chapter content, `app.db`, `rag.db`, `library/`.
- That directory is *computed*, not configured, and is independent of the
  process working directory — identical behavior under `wails dev` and a
  packaged build.
- Cross-platform by construction.
- No migration of existing data.

## Non-goals

- Moving `app.db` / `rag.db` / `library/` — they already live in the target
  directory.
- Auto-creating or templating a `.env` on first run.
- An in-app "open config folder" affordance (possible later; out of scope).

## Design

### The app directory

`os.UserConfigDir()/starshp_app` is the single app directory:

| OS | Path |
| --- | --- |
| Windows | `%APPDATA%\starshp_app` |
| Linux | `$XDG_CONFIG_HOME/starshp_app` or `~/.config/starshp_app` |
| macOS | `~/Library/Application Support/starshp_app` |

A new function `config.AppDir() (string, error)`:

1. If `STARSHP_HOME` is set, use it verbatim (provide an absolute path).
2. Otherwise, `os.UserConfigDir()/starshp_app`.
3. `os.MkdirAll` the result, then return it.

`main.go`'s `dataDir()` is deleted; callers use `config.AppDir()`.

### Bootstrap flow (`main.go`)

```go
appDir, err := config.AppDir()
cfg, err := config.Load(filepath.Join(appDir, ".env"))
if cfg.AppDBPath == ""  { cfg.AppDBPath  = filepath.Join(appDir, "app.db") }
if cfg.RAGDBPath == ""  { cfg.RAGDBPath  = filepath.Join(appDir, "rag.db") }
if cfg.LibraryDir == "" { cfg.LibraryDir = filepath.Join(appDir, "library") }
```

The DB / library fallback logic is unchanged — only the base directory
changes (from `dataDir()` to `config.AppDir()`).

### Config-file resolution (`config.Load`)

The `CONFIG_PATH` block added earlier is removed. In its place: when
`TextbooksConfig` / `ModelsConfig` are relative, resolve them against
`filepath.Dir(envPath)` — the app directory. Absolute values pass through
unchanged.

```go
base := filepath.Dir(envPath)
if !filepath.IsAbs(c.TextbooksConfig) {
    c.TextbooksConfig = filepath.Join(base, c.TextbooksConfig)
}
if !filepath.IsAbs(c.ModelsConfig) {
    c.ModelsConfig = filepath.Join(base, c.ModelsConfig)
}
```

`config.Load` keeps its `Load(envPath string)` signature — `main.go` passes
the app-directory `.env` path; tests pass temp paths. Edge case: when
`envPath` is `""`, `filepath.Dir("")` is `"."` and `filepath.Join(".", name)`
cleans back to `name`, so an empty `envPath` leaves bare config names bare —
preserving current behavior for tests that call `Load("")`.

### Removals

- `CONFIG_PATH`: the env var, the resolution block in `config.go`, the
  `.env.example` entry, and the README references.
- `dataDir()` in `main.go`.
- The three `CONFIG_PATH` tests in `config_test.go` (replaced — see Testing).

### Error handling and first run

- `config.AppDir()` `MkdirAll`s the directory, so a fresh machine works with
  no manual setup.
- Missing `.env`: `config.Load` already `os.Stat`-guards and skips loading
  silently; the app runs on OS environment + defaults, and
  `appapi.ValidateStartup` surfaces missing keys / `models.yaml` as a setup
  notice in the first message bubble. Unchanged.
- `STARSHP_HOME` pointing somewhere unwritable surfaces as an ordinary
  `MkdirAll` error, returned from `AppDir()` and fatal at startup
  (`log.Fatalf`), consistent with current `config` / `store` failure handling.

### One-time cleanup (developer machine)

Not code — a manual step performed once:

- Move the real `.env` from the repo root to `%APPDATA%\starshp_app\.env`,
  and delete its now-defunct `CONFIG_PATH` line.
- `app.db`, `rag.db`, `library/`, and `models.yaml` already reside in
  `%APPDATA%\starshp_app` — untouched.

### Documentation

- `.env.example`: remove `CONFIG_PATH`; add a header comment stating the file
  belongs in the app directory (`%APPDATA%\starshp_app` on Windows) and
  noting `STARSHP_HOME` as the override.
- README "Config files and textbooks": rewrite around the single computed
  directory; document `STARSHP_HOME`; drop `CONFIG_PATH` from the
  configuration table and add a `STARSHP_HOME` row.

## Testing (TDD)

New / updated tests in `internal/config`:

- `AppDir()` returns `STARSHP_HOME` when it is set.
- `AppDir()` falls back to `os.UserConfigDir()/starshp_app` when
  `STARSHP_HOME` is unset.
- `AppDir()` creates the directory.
- `Load()` resolves relative `TextbooksConfig` / `ModelsConfig` against the
  `.env` directory.
- `Load()` leaves absolute `TextbooksConfig` / `ModelsConfig` unchanged.
- The earlier `CONFIG_PATH` tests are removed.

Gate: `go build ./...` clean; `go test ./...` green.

## Risks

- **Low.** No data moves; the change is bootstrap wiring plus deletions. The
  one behavioral change — `.env` now loaded from a fixed computed path
  instead of a CWD-relative one — is the intended fix.
- A stale `.env` left at the repo root will no longer load. Mitigated by the
  README / `.env.example` updates; `.env` is already gitignored.

## Decisions

- The override env var is named `STARSHP_HOME`.
- `CONFIG_PATH` is removed entirely; it is not retained as a secondary
  override.
- No `.env` is auto-created on first run.
