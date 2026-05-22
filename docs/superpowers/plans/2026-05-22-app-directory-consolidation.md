# App Directory Consolidation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Consolidate every per-user file into one computed app directory and load `.env` from there, so behavior no longer depends on the process working directory.

**Architecture:** Add `config.AppDir()` returning `os.UserConfigDir()/starshp_app` (overridable via the `STARSHP_HOME` env var). `main.go` loads `.env` from that directory and resolves the databases and `library/` under it. `config.Load` resolves relative `TextbooksConfig`/`ModelsConfig` against the `.env` directory. The `CONFIG_PATH` env var and `main.go`'s `dataDir()` are removed.

**Tech Stack:** Go 1.25, `github.com/joho/godotenv`, Wails v2.

**Spec:** `docs/superpowers/specs/2026-05-22-app-directory-consolidation-design.md`

---

## File Structure

- `internal/config/config.go` — Modify: add `AppDir()`; replace the `CONFIG_PATH` resolution block with `.env`-directory resolution.
- `internal/config/config_test.go` — Modify: add `AppDir` tests; replace the three `CONFIG_PATH` tests with `.env`-directory resolution tests.
- `main.go` — Modify: remove `dataDir()`, use `config.AppDir()`, load `.env` from the app directory, drop the now-unused `os` import.
- `.env.example` — Modify: remove `CONFIG_PATH`, add an app-directory header comment.
- `README.md` — Modify: rewrite the config sections around the computed app directory.

Tasks 1–2 are pure TDD. Task 3 is a build-verified wiring change (`main.go` has no unit tests). Task 4 is documentation. Task 5 is one-time machine cleanup plus end-to-end verification.

---

### Task 1: Add `config.AppDir()`

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/config/config_test.go`:

```go
func TestAppDirHonorsStarshpHome(t *testing.T) {
	want := filepath.Join(t.TempDir(), "custom-home")
	t.Setenv("STARSHP_HOME", want)
	got, err := AppDir()
	if err != nil {
		t.Fatalf("AppDir: %v", err)
	}
	if got != want {
		t.Errorf("AppDir() = %q, want %q", got, want)
	}
	if fi, statErr := os.Stat(got); statErr != nil || !fi.IsDir() {
		t.Errorf("AppDir did not create the directory: stat err=%v", statErr)
	}
}

func TestAppDirFallsBackToUserConfigDir(t *testing.T) {
	t.Setenv("STARSHP_HOME", "")
	got, err := AppDir()
	if err != nil {
		t.Fatalf("AppDir: %v", err)
	}
	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("UserConfigDir: %v", err)
	}
	if want := filepath.Join(base, "starshp_app"); got != want {
		t.Errorf("AppDir() = %q, want %q", got, want)
	}
}
```

Note: `TestAppDirFallsBackToUserConfigDir` calls `AppDir()` with no override, which creates the real `os.UserConfigDir()/starshp_app` directory. This is idempotent and is exactly where the app stores its files, so the side effect is harmless.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/config/ -run TestAppDir -v`
Expected: FAIL — compile error `undefined: AppDir`.

- [ ] **Step 3: Add the `AppDir` implementation**

In `internal/config/config.go`, add this function immediately after the `Config` struct definition (before `Load`):

```go
// AppDir returns the per-user application directory, creating it if needed.
// STARSHP_HOME overrides the location (use an absolute path); otherwise it is
// os.UserConfigDir()/starshp_app — %APPDATA%\starshp_app on Windows,
// ~/.config/starshp_app on Linux, ~/Library/Application Support/starshp_app
// on macOS.
func AppDir() (string, error) {
	dir := strings.TrimSpace(os.Getenv("STARSHP_HOME"))
	if dir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(base, "starshp_app")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}
```

The `os`, `path/filepath`, and `strings` packages are already imported by `config.go` — no import changes needed.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/config/ -run TestAppDir -v`
Expected: PASS — both `TestAppDirHonorsStarshpHome` and `TestAppDirFallsBackToUserConfigDir`.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat: add config.AppDir for the consolidated app directory"
```

---

### Task 2: Resolve config files against the `.env` directory

This replaces the `CONFIG_PATH`-based resolution (added earlier this session) with resolution against the directory that contains `.env`.

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

- [ ] **Step 1: Replace the `CONFIG_PATH` tests with `.env`-directory tests**

In `internal/config/config_test.go`, delete these three functions in their entirety:
- `TestLoadResolvesRelativeConfigsAgainstConfigPath`
- `TestLoadKeepsAbsoluteConfigsWhenConfigPathSet`
- `TestLoadWithoutConfigPathKeepsBareConfigNames`

Add these two functions in their place:

```go
func TestLoadResolvesRelativeConfigsAgainstEnvDir(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "ok")
	dir := t.TempDir()
	c, err := Load(filepath.Join(dir, ".env"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if want := filepath.Join(dir, "textbooks.yaml"); c.TextbooksConfig != want {
		t.Errorf("TextbooksConfig = %q, want %q", c.TextbooksConfig, want)
	}
	if want := filepath.Join(dir, "models.yaml"); c.ModelsConfig != want {
		t.Errorf("ModelsConfig = %q, want %q", c.ModelsConfig, want)
	}
}

func TestLoadKeepsAbsoluteConfigPaths(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "ok")
	absModels := filepath.Join(t.TempDir(), "elsewhere", "models.yaml")
	t.Setenv("MODELS_CONFIG", absModels)
	c, err := Load(filepath.Join(t.TempDir(), ".env"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.ModelsConfig != absModels {
		t.Errorf("ModelsConfig = %q, want %q (absolute path must be unchanged)", c.ModelsConfig, absModels)
	}
}
```

- [ ] **Step 2: Run the tests to verify the new behavior fails**

Run: `go test ./internal/config/ -run TestLoad -v`
Expected: FAIL — `TestLoadResolvesRelativeConfigsAgainstEnvDir` fails because `TextbooksConfig`/`ModelsConfig` are still the bare names `textbooks.yaml`/`models.yaml` (the current code only joins them when `CONFIG_PATH` is set). `TestLoadKeepsAbsoluteConfigPaths` and the other existing `TestLoad*` tests pass.

- [ ] **Step 3: Replace the `CONFIG_PATH` block in `Load`**

In `internal/config/config.go`, find this block inside `Load` (just before `return c, nil`):

```go
	// When CONFIG_PATH is set, resolve relative config-file paths against it so
	// they no longer depend on the process working directory (which differs
	// between `wails dev` and a packaged build). Absolute paths are left as-is.
	if base := strings.TrimSpace(os.Getenv("CONFIG_PATH")); base != "" {
		if !filepath.IsAbs(c.TextbooksConfig) {
			c.TextbooksConfig = filepath.Join(base, c.TextbooksConfig)
		}
		if !filepath.IsAbs(c.ModelsConfig) {
			c.ModelsConfig = filepath.Join(base, c.ModelsConfig)
		}
	}
```

Replace it with:

```go
	// Resolve relative config-file paths against the directory that contains
	// .env (the app directory), so they do not depend on the process working
	// directory. Absolute paths are left as-is. When envPath is "", base is "."
	// and filepath.Join cleans the result back to the bare name.
	base := filepath.Dir(envPath)
	if !filepath.IsAbs(c.TextbooksConfig) {
		c.TextbooksConfig = filepath.Join(base, c.TextbooksConfig)
	}
	if !filepath.IsAbs(c.ModelsConfig) {
		c.ModelsConfig = filepath.Join(base, c.ModelsConfig)
	}
```

- [ ] **Step 4: Run the full config suite to verify it passes**

Run: `go test ./internal/config/ -v`
Expected: PASS — all tests: `TestLoadDefaults`, `TestLoadReadsEnvFile`, `TestLoadLibraryDir`, `TestLoadResolvesRelativeConfigsAgainstEnvDir`, `TestLoadKeepsAbsoluteConfigPaths`, `TestAppDirHonorsStarshpHome`, `TestAppDirFallsBackToUserConfigDir`.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "refactor: resolve config files against the .env directory, drop CONFIG_PATH"
```

---

### Task 3: Wire `main.go` to the app directory

`main.go` has no unit tests; this task is verified by a clean build and vet.

**Files:**
- Modify: `main.go`

- [ ] **Step 1: Remove `dataDir()` and rewire `main()`**

In `main.go`, delete the entire `dataDir` function:

```go
func dataDir() string {
	d, err := os.UserConfigDir()
	if err != nil {
		d, _ = os.Getwd()
	}
	p := filepath.Join(d, "starshp_app")
	os.MkdirAll(p, 0o755)
	return p
}
```

Replace the opening of `main()` — from `cfg, err := config.Load(".env")` through the `LibraryDir` fallback — with:

```go
	appDir, err := config.AppDir()
	if err != nil {
		log.Fatalf("app dir: %v", err)
	}
	cfg, err := config.Load(filepath.Join(appDir, ".env"))
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if cfg.AppDBPath == "" {
		cfg.AppDBPath = filepath.Join(appDir, "app.db")
	}
	if cfg.RAGDBPath == "" {
		cfg.RAGDBPath = filepath.Join(appDir, "rag.db")
	}
	if cfg.LibraryDir == "" {
		cfg.LibraryDir = filepath.Join(appDir, "library")
	}
```

- [ ] **Step 2: Drop the now-unused `os` import**

Removing `dataDir()` leaves `os` unused in `main.go`. In the import block, delete the `"os"` line. The remaining imports (`embed`, `log`, `path/filepath`, the internal packages, and the Wails packages) are all still used.

- [ ] **Step 3: Verify the build and vet are clean**

Run: `go build ./... && go vet ./...`
Expected: no output, exit 0. (A failure here means a leftover `os.` reference or a missing import — fix it before continuing.)

- [ ] **Step 4: Commit**

```bash
git add main.go
git commit -m "feat: load .env and data from config.AppDir, remove dataDir"
```

---

### Task 4: Update documentation

**Files:**
- Modify: `.env.example`
- Modify: `README.md`

- [ ] **Step 1: Rewrite `.env.example`**

Replace the entire contents of `.env.example` with:

```
# Starshp configuration. This file belongs in the app directory:
#   Windows: %APPDATA%\starshp_app\.env
#   Linux:   ~/.config/starshp_app/.env
#   macOS:   ~/Library/Application Support/starshp_app/.env
# Set the STARSHP_HOME environment variable (an absolute path) to override
# that directory. STARSHP_HOME must be a real environment variable -- it is
# read before .env, so it cannot be set inside this file.

OPENAI_API_KEY=sk-your-key-here
ANTHROPIC_API_KEY=sk-ant-your-key-here
EMBEDDING_MODEL=text-embedding-3-small
APP_DB_PATH=
RAG_DB_PATH=
TEXTBOOKS_CONFIG=textbooks.yaml
MODELS_CONFIG=models.yaml
CONTEXT_TOKEN_BUDGET=2500
RAG_TOP_K=8
```

- [ ] **Step 2: Update the `## Setup` section of `README.md`**

Replace the entire `## Setup` section (from the `## Setup` heading up to, but not including, the `## Config files and textbooks` heading) with:

```
## Setup

Starshp reads its configuration from a per-user **app directory**, created
automatically on first launch. Copy the three committed templates
(`.env.example`, `models.example.yaml`, `textbooks.example.yaml`) into that
directory, drop the `.example` from each name, and fill in your API keys.

See [Config files and textbooks](#config-files-and-textbooks) for where the
app directory is, the copy commands, and the YAML formats.

```

- [ ] **Step 3: Replace the `## Config files and textbooks` section of `README.md`**

Replace the entire `## Config files and textbooks` section (from that heading up to, but not including, the `## Running` heading) with:

````
## Config files and textbooks

Starshp keeps every per-user file in one **app directory**:

| OS | App directory |
| --- | --- |
| Windows | `%APPDATA%\starshp_app` |
| Linux | `~/.config/starshp_app` |
| macOS | `~/Library/Application Support/starshp_app` |

It holds `.env`, `models.yaml`, `textbooks.yaml`, your textbook chapter
folders, and the runtime data (`app.db`, `rag.db`, `library/`). The directory
is created automatically on first launch. Set the `STARSHP_HOME` environment
variable (an absolute path) to override its location — handy for tests or a
portable install.

Three templates ship in the repo. Copy each into the app directory and edit:

```bash
cp .env.example           <app-dir>/.env
cp models.example.yaml    <app-dir>/models.yaml
cp textbooks.example.yaml <app-dir>/textbooks.yaml
```

Edit `.env` to fill in your API keys. None of these files require a recompile.

Typical app-directory layout:

```
%APPDATA%\starshp_app\
├── .env
├── models.yaml
├── textbooks.yaml
├── app.db                    (created at runtime)
├── rag.db                    (created at runtime)
├── library/                  (created at runtime)
└── intermediate-accounting/  (your textbook chapter folders)
    ├── chapter-01.md
    └── ...
```

### `textbooks.yaml`

Each entry names a book and points at its directory of chapter markdown:

```yaml
textbooks:
  - name: intermediate-accounting
    chapter_dir: ./intermediate-accounting
  - name: financial-accounting
    chapter_dir: /absolute/path/to/financial-accounting
```

- `chapter_dir` is resolved **relative to the directory containing
  `textbooks.yaml`** (the app directory) — not the working directory. Keep it
  `./<book>` and store the folders alongside the file, or give an absolute
  path.
- A chapter folder holds files named `chapter-1.md`, `chapter-2.md`, … —
  leading zeros optional (`chapter-01.md` is equivalent). Files that do not
  match that pattern are ignored.
- `name` is the label in the per-conversation textbook picker and the key
  used to scope RAG retrieval.
- `textbooks.yaml` is optional: if absent, RAG is unavailable and chat still
  works. If present but a `chapter_dir` cannot be read, startup fails — fix
  the path and relaunch.

### `models.yaml`

The list of models offered in the per-message picker:

```yaml
models:
  - display: Claude Opus 4.7      # label shown in the UI
    id: claude-opus-4-7           # identifier sent to the provider
    provider: anthropic           # "anthropic" or "openai"
  - display: GPT-5.4
    id: gpt-5.4-2026-03-05
    provider: openai
```

Edit it freely as model IDs evolve — no recompile. A missing or unreadable
`models.yaml` produces a setup notice at launch and an empty model dropdown.

````

- [ ] **Step 4: Replace the `## Running` section of `README.md`**

Replace the entire `## Running` section (from that heading up to, but not including, the `## Configuration reference` heading) with:

````
## Running

```bash
wails dev      # hot-reload dev mode
wails build    # release binary at build/bin/starshp_app.exe
```

`app.db` (conversations, messages, presets, scope) and `rag.db` (textbook
chunks + embeddings) are created in the app directory on first launch. They
are independent — rebuilding the RAG index never endangers chat history.
Override either path via `APP_DB_PATH` / `RAG_DB_PATH` in `.env`.

````

- [ ] **Step 5: Update the configuration reference table in `README.md`**

In the `## Configuration reference` section, replace the intro line and the table. Replace this line:

```
All variables read from `.env` (and the environment); see `.env.example`.
```

with:

```
All variables below are read from `.env` (and the OS environment); see
`.env.example`. `STARSHP_HOME` is the exception — it must be a real
environment variable, since it determines where `.env` itself is found.
```

Then replace the entire Markdown table with:

```
| Variable | Default | Purpose |
| --- | --- | --- |
| `STARSHP_HOME` | OS app directory | Overrides the app directory. Real env var only (absolute path), not a `.env` entry. |
| `OPENAI_API_KEY` | — | OpenAI key (chat + embeddings). |
| `ANTHROPIC_API_KEY` | — | Anthropic key (chat only). |
| `EMBEDDING_MODEL` | `text-embedding-3-small` | OpenAI embedding model. |
| `APP_DB_PATH` | `<app-dir>/app.db` | Chat history DB. |
| `RAG_DB_PATH` | `<app-dir>/rag.db` | RAG index DB. |
| `TEXTBOOKS_CONFIG` | `textbooks.yaml` | Textbook manifest; a relative value resolves inside the app directory. |
| `MODELS_CONFIG` | `models.yaml` | Model registry; a relative value resolves inside the app directory. |
| `CONTEXT_TOKEN_BUDGET` | `2500` | Max textbook context tokens injected per turn. |
| `RAG_TOP_K` | `8` | Top-K passed to vector search (over-fetched ×6, then scope-filtered + budget-trimmed). |
```

- [ ] **Step 6: Commit**

```bash
git add .env.example README.md
git commit -m "docs: document the consolidated app directory and STARSHP_HOME"
```

---

### Task 5: One-time machine cleanup and end-to-end verification

This task moves the developer's real `.env` into the app directory. It changes only gitignored files, so it produces no commit — it is the final verification gate.

**Files:** none tracked by git.

- [ ] **Step 1: Copy `.env` into the app directory without the `CONFIG_PATH` line**

The current `.env` sits at the repo root. The app directory on this Windows machine is `%APPDATA%\starshp_app` (`C:\Users\weldo\AppData\Roaming\starshp_app`), which already exists.

Run (PowerShell):

```powershell
Get-Content .env | Where-Object { $_ -notmatch '^\s*CONFIG_PATH=' } |
  Set-Content "$env:APPDATA\starshp_app\.env"
```

Then open `%APPDATA%\starshp_app\.env` and confirm `OPENAI_API_KEY` and `ANTHROPIC_API_KEY` are present and `CONFIG_PATH` is gone.

- [ ] **Step 2: Run the full test suite**

Run: `go test ./...`
Expected: every package reports `ok` (or `[no test files]` for the root package).

- [ ] **Step 3: Manually verify the running app**

Run: `wails dev`
Confirm:
- The window opens with no "Setup" notice in the first message bubble about a missing `models.yaml`.
- The model dropdown is populated (Claude Opus/Sonnet/Haiku + GPT-5.4).

Close the app when done. If the model dropdown is empty, confirm `%APPDATA%\starshp_app\models.yaml` exists and is readable.

- [ ] **Step 4: Remove the stale repo-root `.env`**

Only after Step 3 passes, delete the now-unused repo-root `.env` (it is gitignored, so this changes nothing tracked):

```powershell
Remove-Item .env
```

The app now loads everything from `%APPDATA%\starshp_app`.

---

## Self-Review

**Spec coverage:**
- App directory at `os.UserConfigDir()/starshp_app` with `STARSHP_HOME` override → Task 1.
- `.env` loaded from the app directory; DBs/library under it → Task 3.
- Relative `TextbooksConfig`/`ModelsConfig` resolved against the `.env` directory → Task 2.
- `CONFIG_PATH` and `dataDir()` removed → Tasks 2 and 3.
- First-run: directory auto-created, missing `.env` tolerated → Task 1 (`MkdirAll`); existing `config.Load` `os.Stat` guard is unchanged.
- Docs (`.env.example`, README) → Task 4.
- One-time `.env` cleanup → Task 5.
- Tests for `AppDir` and `Load` resolution → Tasks 1 and 2.
All spec sections are covered.

**Placeholder scan:** No `TBD`/`TODO`/"handle edge cases" placeholders. Every code and doc step shows complete content.

**Type consistency:** `AppDir() (string, error)` is defined in Task 1 and called in Task 3 with the same signature. `config.Load(envPath string)` keeps its existing signature; Task 3 passes `filepath.Join(appDir, ".env")`; Task 2 reads `filepath.Dir(envPath)`. `STARSHP_HOME`, `TEXTBOOKS_CONFIG`, `MODELS_CONFIG`, `CONFIG_PATH` names are used consistently across tasks.
