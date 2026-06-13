# AGENTS.md — Implementation Guide for go-genkit-memory

This document is the authoritative specification for all implementation agents
working on this repository.  Read it completely before writing any code.

---

## 1. Runtime & Tooling

### 1.1 mise (environment, tasks, tool versions)

All local tooling is managed through **[mise](https://mise.jdx.dev/)**.  
Configuration lives in `mise.toml` at the repository root.

```
mise install          # install all declared tool versions
mise run doctor       # verify all runtime dependencies (see §1.4)
mise run dev          # start the app via Overmind in dev mode
mise run test         # run unit tests only (no external deps)
mise run test:int     # run integration tests (no external deps)
mise run build        # compile the binary
```

### 1.2 Local process management — Overmind + Procfile

- **No Docker**.  All services run as native OS processes.
- Use **[Overmind](https://github.com/DarthSim/overmind)** (tmux-based Procfile
  runner) to manage multiple processes.
- The `Procfile` at the repository root declares every service.
- Overmind is started by `mise run dev`.

### 1.3 Hot-reload — air

- **[air](https://github.com/air-verse/air)** is used for automatic binary
  recompilation and restart in development.
- Configuration lives in `.air.toml`.
- The `app` entry in `Procfile` launches `air` so that source changes cause an
  immediate restart without manual intervention.

### 1.4 Doctor task (`mise run doctor`)

The doctor task (`mise.toml` task `doctor`) checks every local dependency and
prints a clear message if something is missing or misconfigured.

Checks performed (in order):

| Dependency | Minimum version | Install hint |
|---|---|---|
| `go` | 1.26 | `mise use go@latest` |
| `air` | latest | `go install github.com/air-verse/air@latest` |
| `overmind` | latest | `brew install overmind` / `go install github.com/DarthSim/overmind/v2@latest` |
| `tmux` | any | system package manager |
| OpenAI-compatible endpoint | reachable at `$OPENAI_BASE_URL` | start Ollama or compatible server |

The doctor task exits **0** only when all checks pass.  Each failing check
prints a one-line remediation hint (never just an error code).

---

## 2. Environment Variables

All environment configuration is declared in `mise.toml` under `[env]`.  
**Never hard-code URLs, model names, or credentials in Go source.**

| Variable | Default in mise.toml | Purpose |
|---|---|---|
| `OPENAI_BASE_URL` | `http://aibox:11435/v1` | OpenAI-compatible API base URL |
| `OPENAI_API_KEY` | `local` | API key (arbitrary value for local servers) |
| `OPENAI_MODEL` | `gpt-oss` | Default model name |
| `APP_ENV` | `development` | Runtime environment tag |
| `LOG_LEVEL` | `debug` | Structured log verbosity |

Override any variable in a `.env` file (`.gitignore`d) or via `mise.local.toml`
for machine-specific values.

---

## 3. Go Language Standard

### 3.1 Version

- Go **1.26**.  The `go` directive in `go.mod` must reflect this.

### 3.2 Standard library first

Use stdlib wherever it is sufficient before reaching for third-party packages.

| Task | Preferred approach |
|---|---|
| HTTP routing | `net/http.ServeMux` with pattern syntax (`GET /path/{id}`) |
| Structured logging | `log/slog` with `slog.New(slog.NewJSONHandler(...))` |
| JSON | `encoding/json` |
| Context propagation | `context.Context` threaded through every function |
| Generics | Use when the abstraction genuinely eliminates duplication across types |

### 3.3 Generics guideline

- Use type parameters for store/container types that must work with multiple
  concrete state types (e.g., `Store[S any]`).
- Do **not** use generics purely for aesthetics; prefer simple interfaces when
  they suffice.

### 3.4 Structured logging (`slog`)

- Every service entry point creates a root `*slog.Logger` and passes it via
  `context` or as an explicit argument.
- Log at `DEBUG` for development verbosity; use `INFO`/`WARN`/`ERROR` for
  production-relevant events.
- Use `slog.Group` for structured attributes; never concatenate strings into
  message fields.

---

## 4. Testing Strategy

### 4.1 Principles

- **No external dependencies in tests.**  Unit and integration tests must pass
  on a machine with no network access, no running database servers, and no
  running AI endpoint.
- **No mocking frameworks.**  Tests use Go's built-in anonymous function
  replacement pattern (described in §4.2).
- Unit tests live alongside source files (`*_test.go` in the same package or a
  `_test` package sibling).
- Integration tests live in the `integration/` directory.
- Tests must be runnable with a single `go test ./...` invocation.

### 4.2 Anonymous function replacement (dependency injection for tests)

Instead of a mocking library, package-level or struct-level function variables
are used as seams.  In production the variable holds the real implementation; in
tests it is replaced with a lightweight stub.

Pattern:

```go
// production code — defined at package level or on a struct
var callLLM = func(ctx context.Context, prompt string) (string, error) {
    // real implementation using OPENAI_BASE_URL
}

// test — replace before the test, restore after
func TestSomething(t *testing.T) {
    orig := callLLM
    callLLM = func(_ context.Context, _ string) (string, error) {
        return "stubbed response", nil
    }
    t.Cleanup(func() { callLLM = orig })

    // exercise code under test...
}
```

Rules:
- Every function variable used as a seam must be exported if the test resides
  in a separate `_test` package, or unexported if the test is in the same
  package.
- Restore the original value with `t.Cleanup`, never with a deferred call that
  might be skipped.
- Keep stubs minimal — return only what the test needs.

### 4.3 Integration tests

- The `integration/` package tag must not require a live AI endpoint.
- Tests that exercise the full genkit session lifecycle use the in-memory store
  (or file-backed stores with `t.TempDir()`).
- Any test that genuinely needs a network service must be guarded with:

```go
if os.Getenv("INTEGRATION_LIVE") != "1" {
    t.Skip("set INTEGRATION_LIVE=1 to run live integration tests")
}
```

### 4.4 Running tests

```
mise run test        # go test ./session/... -race -count=1
mise run test:int    # go test ./integration/... -race -count=1 -timeout 120s
```

---

## 5. Project Layout

```
.
├── AGENTS.md            ← this file
├── Procfile             ← Overmind process definitions
├── mise.toml            ← tool versions, env vars, tasks
├── .air.toml            ← air hot-reload config
├── go.mod / go.sum
├── README.md
├── session/
│   ├── bbolt/           ← BBolt-backed session store
│   └── sqlite/          ← SQLite-backed session store
└── integration/         ← cross-store integration tests
```

New packages must follow this layout.  Do not create top-level `cmd/` directories
unless the repository ships a runnable binary; in that case use `cmd/<name>/main.go`.

---

## 6. Code Style & Conventions

- `gofmt` and `goimports` — always applied before committing.
- Error wrapping: `fmt.Errorf("...: %w", err)` — never `errors.New` on a
  wrapped error.
- Return errors, never panic in library code.  Panics are only acceptable in
  `main()` for unrecoverable startup failures.
- Unexported types for internal implementation details; export only what callers
  need.
- Comment every exported symbol (`// Symbol does ...`).

---

## 7. Dependency Policy

- Prefer stdlib; add a third-party dependency only when:
  1. The problem cannot be solved reasonably with stdlib.
  2. The dependency is well-maintained (active commits in the last 12 months).
  3. It does not pull in CGO (prefer pure-Go alternatives).
- Update `go.mod` minimum Go directive to `1.26` before any new import.
- After adding a dependency, run `go mod tidy`.

---

## 8. AI / LLM Integration

- The genkit `ai.DefineModel` or `openai.New` call must read base URL and model
  from `os.Getenv("OPENAI_BASE_URL")` and `os.Getenv("OPENAI_MODEL")`.
- The client must set a `timeout` (30 s default) on the underlying
  `*http.Client` — never use the default zero-timeout client.
- Log every outbound LLM call at `DEBUG` level with model, prompt token count
  (if available), and latency.

---

## 9. Checklist for every PR

- [ ] `mise run doctor` exits 0 on a clean machine.
- [ ] `go test ./... -race` passes.
- [ ] No new external dependencies without justification in the PR description.
- [ ] New code uses `slog` for logging, not `fmt.Print*` or `log.Print*`.
- [ ] Every new exported symbol has a Go doc comment.
- [ ] `.env` / secrets are not committed (scan with `mise run secret-scan`).
