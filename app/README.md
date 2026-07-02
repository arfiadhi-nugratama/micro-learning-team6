# ms1-hackathon-2026

Go HTTP API that generates flashcard decks from course module content using OpenAI, with spaced-repetition review tracking via FSRS.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `DATABASE_URL` | yes | Postgres DSN (e.g. `postgres://user:pass@host:5432/db`) |
| `CMS_GRPC_ADDR` | yes | CmsBffV1Service gRPC address (e.g. `localhost:9090`) |
| `OPENAI_API_KEY` | yes | OpenAI API key |
| `OPENAI_BASE_URL` | no | Override OpenAI API base URL (e.g. for proxies or compatible endpoints) |
| `CONTENTFUL_SPACE_ID` | no | Contentful space ID. Required with the other two for full concept body fetching. |
| `CONTENTFUL_ENVIRONMENT` | no | Contentful environment (e.g. `development`, `master`). |
| `CONTENTFUL_ACCESS_TOKEN` | no | Contentful Content Delivery API token. When all three Contentful vars are set, fetches full concept body text for richer flashcard generation. |
| `PORT` | no | HTTP listen port (default `8080`) |

## Running

Start Postgres:

```sh
docker compose up -d  # from repo root
```

DSN: `postgres://team6:team6@localhost:5432/team6?sslmode=disable`

Run the service:

```sh
DATABASE_URL=postgres://team6:team6@localhost:5432/team6?sslmode=disable \
  CMS_GRPC_ADDR=localhost:50051 \
  OPENAI_API_KEY=... \
  go run .
```

Migrations run automatically at startup (`CREATE TABLE IF NOT EXISTS`).

## API

### POST /modules/:moduleID/deck

Generate a new flashcard deck for a module. Fetches module structure via gRPC, sends to `gpt-4o-mini`, persists deck + cards.

Query params:
- `locale` — content locale (default `en`)

Response: deck object with cards. Each card has `distractors` truncated to 3.

### GET /modules/:moduleID/deck

Fetch the most recent deck for a module (with cards).

### DELETE /decks/:deckID

Delete a deck by ID. Returns `204 No Content`.

### GET /decks/:deckID/review

Get cards due for review for a learner.

Query params:
- `learner_id` — required

Returns cards with no SRS record, or whose `due` time has passed.

### POST /cards/:cardID/review

Submit a review rating for a card. Creates or updates the SRS record.

Body:
```json
{
  "learner_id": "string",
  "rating": 1
}
```

Ratings: `1` = Again, `2` = Hard, `3` = Good, `4` = Easy (FSRS scale).

## Schema

```
decks       id, module_id, created_at
cards       id, deck_id, question, correct_answer, distractors TEXT[], question_ja, correct_answer_ja, distractors_ja TEXT[], created_at
srs_cards   id, card_id, learner_id, due, stability, difficulty, reps, lapses, state, last_review
```
