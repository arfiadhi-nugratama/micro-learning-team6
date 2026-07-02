# CLAUDE.md

## Project

Go HTTP API — flashcard deck generation + FSRS spaced repetition.
Module: `github.com/dojo-product/team6`, Go 1.24.

## Key Packages

| Package | Role |
|---|---|
| `httpapi` | HTTP handlers via `bunrouter` |
| `db` | bun ORM models + startup migrations |
| `grpc` | gRPC client for `CmsBffV1Service` |
| `llm` | OpenAI `gpt-4o-mini` flashcard generation |
| `srs` | FSRS spaced repetition logic |

## Dependencies

- `github.com/uptrace/bun` + `pgdialect` + `pgdriver` — ORM + Postgres
- `github.com/uptrace/bunrouter` — HTTP router
- `github.com/openai/openai-go` — OpenAI SDK (not raw HTTP)
- `github.com/open-spaced-repetition/go-fsrs/v3` — FSRS algorithm
- `google.golang.org/grpc` — gRPC client
- Proto: `github.com/Woven-dojo/ms1-proto/sdk-go/cmsbff/api` (local replace → `~/woven/ms1-proto/sdk-go/cmsbff/api`)
- Contentful GraphQL: optional `CONTENTFUL_SPACE_ID` + `CONTENTFUL_ENVIRONMENT` + `CONTENTFUL_ACCESS_TOKEN` — when all set, `GetModuleContent` fetches full concept body via direct Contentful GraphQL (`GetConcept` query) in addition to nav tree titles. Gracefully skipped if env vars absent.

## Conventions

- `distractors` stored as Postgres native `TEXT[]` (`bun:",array"`)
- API responses truncate distractors to 3; DB stores all LLM-generated distractors
- Migrations use `bun/migrate` with embedded SQL files (`db/*.sql`, named `YYYYMMDDHHMMSS_name.up.sql`). No down migrations. `Migrate()` calls `m.Init()` then `m.Migrate()` at startup.
- `moduleID` is always a path parameter, never fetched from a list
- FSRS ratings: 1=Again, 2=Hard, 3=Good, 4=Easy
- Always keep README.md and CLAUDE.md up to date
