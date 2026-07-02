# Spec / PRD: Flashcards (Retention Layer) — v1

> Status: Draft for review · Owner: Yuri · Scope tag: `SALX-` · Date: 2026-07-02
> This is a living document. Update it when scope or decisions change, then implement.

## 1. Objective

### Problem

The platform has Courses today and is adding Tutorials, Webinars, and Cheatsheets. These are strong learning _experiences_ but nothing is focused on **long-term memory retention** of what was learned. Time-poor learners want to keep learning and _retaining_ without committing to course-length content.

### What we're building

A **retention layer** on top of existing content: a Flashcards feature driven by a **Spaced Repetition System (SRS)**. Learners study short, AI-generated multiple-choice cards; the system schedules reviews so knowledge moves into long-term memory.

References: **Anki/FSRS** (flexible, per-card scheduling) and **WaniKani** (staged, opinionated SRS).

### Who is the user

A learner who wants durable recall of concepts without dedicating course-length blocks of time.

### What success looks like

- Learners can discover a system deck, duplicate it, and complete a study session end-to-end.
- Cards are scheduled by FSRS and reappear at appropriate intervals.
- Learners return across days and see review counts they can clear.
- (Metrics in §10.)

---

## 2. Scope

### In scope (v1)

1. **System Decks** — AI-generated, one per course (generation-time seeding only, no persistent hard link). Browsable, read-only originals.
2. **Decks Hub** — discover/browse system decks; **official** decks tagged and filterable; **duplicate** a deck into a Personal Deck.
3. **Personal Decks** — a learner-owned **copy** of a system deck with **fresh FSRS progress**. Multiple copies of the same source allowed. Card content is **read-only** in v1 (copy-for-progress, not copy-for-editing).
4. **Study loop** — unified study session serving due cards (new + review mixed) under one CTA. **MCQ cards, system-graded.** New cards capped at **5 at a time**.
5. **Scheduling** — **FSRS scheduler**, recall rating **derived from MCQ result** (see §7). No user self-rating UI.
6. **Dashboard** — top-nav Flashcards item → overall progress, to-learn / to-review counts, SRS stage breakdown, per-deck summary + per-deck detail.
7. **Card feedback** — capture-only "report card" button recording `{cardId, reason, userId}` for later review.

### Nice-to-have (P2, if time permits)

- Split the unified study session into distinct **Learn** (5 new) and **Review** (all due) entrypoints on the homepage.

### Out of scope (deferred to v2+)

- Authoring cards from scratch / personal-deck card CRUD editing.
- Public / published personal decks and their discoverability.
- Upstream sync (regenerated system deck → existing copies unaffected in v1).
- Notifications / email ("N reviews due").
- Per-student card suspend/skip/reset.
- AI card **correctness validation** workflow: course-manager "edit deck" menu + feedback triage UI.

---

## 3. Personas & User Stories

- **As a time-poor learner**, I want to duplicate a deck for a topic I care about and study a few cards a day, so I retain what I learned.
- **As a learner**, I want the app to decide what to review and when, so I don't have to plan my study schedule.
- **As a learner**, I want to see how much is due and my progress per deck, so I feel momentum.
- **As a learner**, I want to flag a wrong/confusing card, so it can be fixed later.

---

## 4. User Flows

### 4.1 Discover & duplicate

1. Learner opens **Decks Hub** (from Flashcards nav or homepage).
2. Browses/filters system decks (official tag filter available).
3. Clicks **Duplicate** → a Personal Deck copy is created with fresh FSRS state → confirmation + entry into "My Decks".
4. Duplicating an already-duplicated deck creates **another independent copy** (allowed).

### 4.2 Study session (core, unified)

1. Learner clicks **Study** (homepage section or dashboard).
2. App assembles the queue: all **due reviews**.
3. For each card: present question + options → learner selects → system grades → correct/incorrect feedback + screen time → FSRS updates schedule (rating derived).
4. Session ends when queue is exhausted; summary shown.

### 4.3 Dashboard (Nice to have)

- Top-nav **Flashcards** → Dashboard: overall progress, to-review totals, SRS stage distribution, and a side summary split **by deck**.
- Per-deck detail: to-learn, due reviews, stage distribution, count not-yet-assimilated.

### 4.4 Report a card

- During study, a **report** affordance records `{cardId, reason, userId}`. No immediate effect on the queue in v1.

---

## 5. Data Model (sketch)

> Backend contracts TBD; this is the frontend's working mental model. `sourceCourseId` is provenance metadata only — no hard FK dependency.

```
SystemDeck
  id
  title, description
  isOfficial: boolean         # drives tag + filter
  sourceCourseId?: string     # provenance only, generation-time link
  cardCount
  cards: Card[]               # read-only originals

Card
  id
  deckId
  question: string
  correctAnswer: string
  distractors: string[]       # generated WITH the card (self-contained), N options
  # NOTE: distractors are NOT drawn from sibling cards

PersonalDeck                  # a learner-owned copy of a SystemDeck
  id
  ownerUserId
  sourceSystemDeckId
  title, description          # copied at duplication time
  createdAt
  # multiple PersonalDecks may share the same sourceSystemDeckId

CardProgress                  # per user, per personal-deck card — FSRS state
  id
  personalDeckId
  cardId
  fsrsState (stability, difficulty, due, reps, lapses, ...)
  stage                       # derived label for UI (new / learning / review / assimilated)
  status: new | learning | review | assimilated

CardFeedback                  # capture-only
  cardId, userId, reason, createdAt
```

**Definitions**

- **Learned**: a card leaves the "new/learn" set and enters the review queue after its first successful Learn exposure.
- **Assimilated**: card reached the terminal FSRS interval (WaniKani "Burned" equivalent); no longer surfaced for review.

---

## 6. MCQ Card Mechanics

- Each card is a **self-contained unit**: `{ question, correctAnswer, distractors[] }`.
- Options presented = correctAnswer + distractors, shuffled.
- **System-graded**: selection is objectively correct or incorrect.
- AI generates question, answer, and N distractors per card at deck-generation time.
- **Open**: value of N (distractor count) — assume 3 (4 options total) unless specified.

---

## 7. FSRS Scheduling (derived rating)

FSRS scheduler runs per `CardProgress`. Since there is no self-rating UI, the recall grade is **derived from the MCQ result**:

| MCQ outcome                              | Derived FSRS rating                              |
| ---------------------------------------- | ------------------------------------------------ |
| Incorrect                                | Again                                            |
| Correct + slow (above latency threshold) | Hard                                             |
| Correct + normal                         | Good                                             |
| Correct + fast (below latency threshold) | Easy _(optional; may collapse into Good for v1)_ |

**Open questions:**

- Latency thresholds for Hard/Good/Easy — Hard: >5m, Good: >3 m, Easy: anything else
- Where does FSRS run — client or backend? Backend

---

## 8. Surfaces (frontend, main-app)

- **Top-nav item**: "Flashcards" → Dashboard.
- **Homepage section**: prominent Study entrypoint (P2: split Learn / Review).
- **Decks Hub**: browse + filter (official tag) + duplicate.
- **Dashboard**: overall + per-deck progress.
- Responsive web within existing app shell; **i18n en/ja**; consistent with `@ms1-frontend/design-system`.
- New i18n namespace TBD (e.g. a flashcards namespace) — confirm during planning.

---

## 9. Models

- **AI card generation**: OpenAI `gpt-5.4` via the OpenAI Go SDK (`app/llm/generate.go`)
- **Claude Code (assistant)**: `claude-sonnet-4-6`

---

## 10. Commands (from repo)

```bash
nx serve main-app                              # dev server :4200
pnpm run test                                  # all tests (vitest)
pnpm exec vitest run path/to/file.test.tsx     # single file
pnpm run lint                                  # eslint
pnpm run typecheck                             # tsc
pnpm run storybook                             # component dev
```

Commit format: `type(SALX-XXX): description`.

---

## 11. Success Criteria & Metrics

**Functional (testable):**

- [ ] A learner can duplicate a system deck; a Personal Deck with fresh FSRS state is created.
- [ ] Multiple copies of the same source deck can coexist independently.
- [ ] A study session serves due reviews, grades MCQ, and updates FSRS schedule.
- [ ] Official decks are tagged and filterable in the Decks Hub.
- [ ] Dashboard shows overall + per-deck to-learn / to-review / stage breakdown.
- [ ] Report-card button persists `{cardId, reason, userId}`.
- [ ] Cards not yet learned are capped/paced correctly; assimilated cards stop appearing.

**Product metrics (post-launch):**

- Deck duplication rate; study-session completion rate; D1/D7 return rate; reviews cleared per active learner; % cards reaching assimilated.

---

## 12. Boundaries

- **Always:** use `@ms1-frontend/design-system`; namespace-prefixed i18n keys (en+ja); `use18nQuery`/mutation patterns; run lint + typecheck + tests before commit.
- **Ask first:** backend contract/data-model shape; where FSRS executes; adding dependencies (FSRS lib?); new i18n namespace; new top-nav entry.
- **Never:** hard-couple deck ↔ course with a required FK; expose personal-deck card editing in v1; ship self-rating UI; commit secrets.

---

## 13. Open Questions

1. **FSRS execution** — backend
2. **FSRS lib** — reuse an existing FSRS implementation in backend
3. **Distractor count N** per card (assume 3 in UI, but at least 7 stored in DB).
4. **Unenroll/delete semantics** — deleting a Personal Deck: hard delete vs. archive progress? Archive progress
5. **New i18n namespace** name/number. Don't worry about i18n, use hard coded strings
6. **Backend readiness** — backend is ready, I have an ace finishing the BE
7. **Duplication limits** — any cap on number of copies to prevent abuse/clutter? 3 system duplicated decks per user

---

## 14. Next Phases (spec-driven workflow)

- **Plan** — component/data dependencies, build order, mocked-vs-real backend, verification checkpoints.
- **Tasks** — discrete, ≤5-file, independently verifiable units.
- **Implement** — incremental + test-driven.
