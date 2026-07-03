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

Migrations run automatically at startup.

## API

### System Decks

#### POST /modules/:moduleID/deck
Generate a new flashcard deck for a module. Fetches module structure via gRPC, sends to `gpt-4.1-mini`, persists deck + cards.

Response: deck object with cards. Each card has `distractors` truncated to 3.

#### GET /modules/:moduleID/deck
Fetch the most recent deck for a module (with cards).

#### GET /decks
List all system decks. Returns each deck with `card_count` — no card details.

#### GET /decks/:deckID
Fetch a system deck by ID with cards.

#### POST /decks/:deckID/cards
Add a new card to a system deck.

#### PUT /decks/:deckID/cards/:cardID
Edit a card in a system deck (in place).

#### DELETE /decks/:deckID/cards/:cardID
Soft-delete a card from a system deck.

#### DELETE /decks/:deckID
Soft-delete a system deck.

---

### Personal Decks

Personal decks are learner-owned. They can be copied from a system deck (shallow copy, copy-on-write on edit) or created from scratch.

#### POST /user-decks
Create a blank personal deck from scratch.

Body:
```json
{
  "learner_id": "string",
  "title": "string",
  "title_ja": "string"
}
```

#### POST /decks/:deckID/copy
Shallow-copy a system deck into a personal deck.

Body: `{"learner_id": "string"}`

#### GET /learners/:learnerID/decks
List all personal decks for a learner (with cards).

#### DELETE /user-decks/:deckID
Soft-delete a personal deck and any cards it owns.

#### POST /user-decks/:deckID/cards
Add a new card to a personal deck.

#### PUT /user-decks/:deckID/cards/:cardID
Edit a card. Triggers copy-on-write if the card is owned by a system deck.

#### DELETE /user-decks/:deckID/cards/:cardID
Remove a card from a personal deck.

---

### Sharing

#### POST /user-decks/:deckID/share
Set visibility and generate a share token.

Body:
```json
{
  "learner_id": "string",
  "visibility": "link"
}
```

`visibility`: `"private"` (default) or `"link"` (shareable). On first share, a stable `share_token` is generated. Re-sharing reuses the same token. Setting back to `"private"` revokes access without invalidating the token.

Response: deck object with `share_token`.

#### GET /shared/:shareToken
View a shared deck (no auth required). Returns 404 if deck is private.

#### POST /shared/:shareToken/copy
Copy a shared deck into the caller's collection.

Body: `{"learner_id": "string"}`

Response: new personal deck with cards, `source_deck_id` pointing to the shared deck.

---

### Reviews

#### GET /decks/:deckID/review
Get cards due for review for a learner.

Query params: `learner_id` — required

Returns cards with no SRS record or whose `due` time has passed.

#### POST /cards/:cardID/review
Submit a review for a card. Creates or updates the SRS record.

Body:
```json
{
  "learner_id": "string",
  "rating": 3,
  "answer": "optional — required for open_text cards"
}
```

Ratings: `1`=Again, `2`=Hard, `3`=Good, `4`=Easy.

Card type behaviour:
- `multiple_choice` / `self_assess` — send `rating`. No `answer` needed.
- `open_text` — send `answer`. LLM judges correctness; rating derived automatically (correct→3, wrong→1). Response includes `judge: {correct, feedback}`.

---

### Feedback

#### POST /cards/:cardID/feedback
Submit feedback on a card.

Body: `{"learner_id": "string", "category": "wrong_answer", "description": "optional"}`

Categories: `wrong_answer`, `wrong_distractor`, `unclear_question`, `bad_translation`, `other`

#### GET /cards/:cardID/feedback
List feedback for a card.

#### GET /decks/:deckID/feedback
List all feedback for cards in a deck.

---

## Card Types

| Type | Description |
|---|---|
| `multiple_choice` | Shuffled `options` / `options_ja` — correct answer mixed in |
| `self_assess` | Show question + answer; learner self-rates 1–4 |
| `open_text` | Learner types answer; LLM grades it |

---

## Schema

```
decks         id, module_id, title, title_ja, deck_type, learner_id, source_deck_id,
              visibility, share_token, created_at, deleted_at
deck_cards    id, deck_id, card_id
cards         id, deck_id, card_type, question, correct_answer, distractors TEXT[],
              question_ja, correct_answer_ja, distractors_ja TEXT[],
              source_concept_id, source_concept_title, created_at, deleted_at
srs_cards     id, card_id, learner_id, due, stability, difficulty, reps, lapses, state, last_review
card_feedback id, card_id, learner_id, category, description, created_at
```

All deletes are soft (`deleted_at = now()`). See `openapi.yaml` for full spec.
