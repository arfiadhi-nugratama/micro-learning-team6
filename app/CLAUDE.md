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

- `distractors` stored as Postgres native `TEXT[]` (`bun:",array"`); same for `distractors_ja`
- `cards.source_concept_id` + `cards.source_concept_title` — LLM-attributed source concept for each card; populated from `Concept-ID`/`Concept-Title` markers in content passed to LLM
- All cards have both EN and JA fields (`question_ja`, `correct_answer_ja`, `distractors_ja`) — generated in one LLM call; prompt is always English
- API responses truncate distractors to 3; DB stores all LLM-generated distractors
- Migrations use `bun/migrate` with embedded SQL files (`db/*.sql`, named `YYYYMMDDHHMMSS_name.up.sql`). No down migrations. `Migrate()` calls `m.Init()` then `m.Migrate()` at startup.
- `moduleID` is always a path parameter, never fetched from a list
- `decks.deck_type`: `system` (LLM-generated) or `user` (copied from system or scratch-created by learner). System decks are immutable.
- `decks.visibility`: `private` (default) or `link` (shareable via `share_token`). Only `user` decks can be shared.
- `decks.share_token`: 32-char hex UUID, generated once on first share, stable. Used at `GET /shared/:shareToken`.
- `deck_cards` junction table owns deck membership. `cards.deck_id` is the owning deck (system or user).
- Copy-on-write: editing a user deck card clones the card row (owned by user deck) and swaps the `deck_cards` junction row. System card untouched.
- FSRS ratings: 1=Again, 2=Hard, 3=Good, 4=Easy
- Always keep README.md, CLAUDE.md, and openapi.yaml up to date
- Soft delete: all delete endpoints set `deleted_at = now()` instead of hard deleting. All queries filter `deleted_at IS NULL`.
- Error handling: DO NOT swallow or warn on core logic errors (gRPC calls, Contentful fetches, LLM calls, DB operations). Always bubble up with `fmt.Errorf("context: %w", err)`. No `log.Warn`, no silent ignore, no fallback empty values. Empty content body (`body == ""`) from Contentful is also an error — return it as such. Let the middleware handle HTTP error responses.
