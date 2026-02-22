# AGENTS.md

Operational guide for AI coding agents working in this repository.

## Project Overview

MyScrollr — platform aggregating financial market data, sports scores, RSS feeds, and Yahoo Fantasy Sports. React frontend, browser extension, Go gateway API, and independent channel services. Infrastructure: PostgreSQL, Redis, Logto (auth), Sequin (CDC). Deployed on Coolify.

## Repository Layout

Monorepo with independently deployable components:

- `api/` — Core gateway API (Go 1.21, Fiber v2)
- `myscrollr.com/` — Frontend (React 19, Vite 7, TanStack Router, Tailwind v4)
- `extension/` — Browser extension (WXT v0.20, React 19, Tailwind v4)
- `channels/{finance,sports,rss}/api/` — Channel Go APIs (independent Go modules)
- `channels/{finance,sports,rss}/service/` — Rust ingestion services (independent crates, edition 2024)
- `channels/fantasy/api/` — Fantasy Go API
- `channels/fantasy/service/` — Fantasy ingestion service (**Python** — FastAPI + uvicorn + asyncpg)
- `channels/*/web/` — Frontend dashboard tab components
- `channels/*/extension/` — Extension feed tab components

## Build & Run Commands

### Frontend (`myscrollr.com/`)

```sh
npm install
npm run dev          # Vite dev server on port 3000
npm run build        # vite build && tsc
npm run lint         # eslint
npm run format       # prettier
npm run check        # prettier --write . && eslint --fix (use this before committing)
```

### Extension (`extension/`)

```sh
npm install
npm run dev          # Dev mode (Chrome)
npm run dev:firefox  # Dev mode (Firefox)
npm run build        # Build Chrome MV3
npm run build:firefox
npm run compile      # tsc --noEmit (type-check only)
npm run zip          # Package for store submission
```

### Go APIs (`api/` and `channels/{name}/api/`)

```sh
go build -o scrollr_api && ./scrollr_api        # Core: port 8080
go build -o {name}_api && ./{name}_api           # finance=8081, sports=8082, rss=8083, fantasy=8084
```

### Rust Services (`channels/{finance,sports,rss}/service/`)

```sh
cargo build --release && cargo run    # finance=3001, sports=3002, rss=3004
```

### Fantasy Python Service (`channels/fantasy/service/`)

```sh
python -m venv .venv && source .venv/bin/activate && pip install -r requirements.txt
uvicorn main:app --port 3003
```

### Tests

No test infrastructure exists yet. When adding tests:

- **Frontend/Extension**: Vitest. Single file: `npx vitest run path/to/file.test.ts`. Single test: `npx vitest run -t "test name"`
- **Go**: `go test ./...`. Single test: `go test -run TestName ./path/to/pkg`
- **Rust**: `cargo test`. Single test: `cargo test test_name`
- **Python**: pytest. Single test: `pytest path/to/test.py -k "test_name"`

## Code Style — TypeScript (Frontend: `myscrollr.com/`)

**Formatting** (Prettier via `prettier.config.js`): No semicolons, single quotes, trailing commas.

**Linting**: `@tanstack/eslint-config` flat config. Run `npm run check` to auto-fix both formatting and lint.

**TypeScript**: Strict mode, target ES2022, `verbatimModuleSyntax: true` — always use `import type` for type-only imports. `noUnusedLocals`, `noUnusedParameters`, `noFallthroughCasesInSwitch`, `noUncheckedSideEffectImports` all enabled.

**Path aliases**: `@/` -> `./src/`, `@scrollr/` -> `../channels/`. Configured in both `tsconfig.json` and `vite.config.ts`.

**Imports**: Named exports only. No barrel files. Use `import type { ... }` for types. Channel discovery uses `import.meta.glob` — never manually register channels.

**Components**: Function components with named exports. Routes use TanStack Router file-based convention (`export const Route = createFileRoute(...)`). Hooks are named exports (`export function useRealtime(...)`). Never edit `src/routeTree.gen.ts` — it is auto-generated.

**External channels**: Custom Vite plugin `resolveExternalChannels` resolves bare imports from `channels/*/web/` to `myscrollr.com/node_modules`. Never duplicate dependencies in channel web dirs.

## Code Style — TypeScript (Extension: `extension/`)

**Formatting**: Uses semicolons (no Prettier configured). Single quotes.

**Path aliases**: `~/` -> srcDir (WXT default), `@scrollr/` -> `../channels/`.

**WXT conventions**: Entrypoints in `entrypoints/` with `defineBackground()`, `defineContentScript()`, etc. Runtime code inside the main function or define callback — never at module top level. Content script UI uses Shadow Root (`createShadowRootUi`). PostCSS converts `rem` to `px` via `postcss-rem-to-responsive-pixel`.

**Auto-imports**: WXT auto-imports from `utils/`, `hooks/`, `components/`. Files in `channels/` are NOT auto-imported — use explicit imports.

## Code Style — Go

**Formatting**: `gofmt`. No custom linter config.

**Module isolation**: Each Go API is fully independent. No shared packages between channels or core. Code duplication is intentional — do not extract shared libraries.

**Naming**: PascalCase exports, camelCase unexported, short receiver names (`s *Server`, `a *App`), snake_case JSON tags (`json:"channel_type"`), constants grouped with section comment banners.

**Error handling**: `if err != nil` returns. `log.Printf` for non-fatal, `log.Fatalf` for startup failures. Wrap with `fmt.Errorf("context: %w", err)`. HTTP errors return `ErrorResponse` struct.

**Logging**: Bracketed category prefixes: `log.Printf("[Auth] message: %v", err)`.

**Structure**: `App` struct holds shared deps (`db *pgxpool.Pool`, `rdb *redis.Client`). Graceful shutdown via `os.Signal` channels. Channel self-registration in Redis with 30s TTL, 20s heartbeat.

## Code Style — Rust

**Edition**: 2024. Default `rustfmt` formatting.

**Error handling**: `anyhow` exclusively (`anyhow::{Context, Result}`). No custom error types. Use `.context("message")?`. Avoid `unwrap()` and `panic!` except for truly unrecoverable init failures.

**Async**: Tokio (full features), HTTP via Axum, database via SQLx (Postgres). Each feed/poll task spawned with `tokio::task::spawn`.

**Logging**: `log` crate macros (`info!`, `error!`, `warn!`). Each service has a custom async file logger (`log.rs`) writing to `./logs/`.

**Known duplication**: `database.rs` and `log.rs` are copy-pasted across all 3 Rust services. Do not extract a shared crate.

## Architecture Rules

1. **Core API has zero channel-specific code.** Discovers channels via Redis, proxies routes dynamically.
2. **Channel isolation is absolute.** Each channel owns its Go API, ingestion service, frontend/extension components, configs, and Docker Compose.
3. **HTTP-only contract.** No shared Go interfaces or types. Core calls `POST /internal/cdc`, channel returns `{ "users": [...] }`.
4. **Route proxying**: Core proxies `/{name}/*` to channel APIs with `X-User-Sub` header. Channels never validate JWTs.
5. **Convention-based UI discovery**: Frontend and extension use `import.meta.glob` to discover channel components at build time.
6. **No migration framework.** Tables created programmatically via `CREATE TABLE IF NOT EXISTS` on service startup.

## Git Workflow

Branch off `staging`: `git checkout -b <prefix>/short-description`. Open PR back into `staging`. Squash merge. Commit trivial one-off fixes directly to `staging`.

**Branch prefixes**: `feature/`, `fix/`, `refactor/`, `chore/`.

## Environment

Copy `.env.example` to `.env`. Frontend env in `myscrollr.com/.env` (`VITE_API_URL`). Never commit `.env` files.
