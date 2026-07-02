ALTER TABLE decks
    ADD COLUMN deck_type TEXT NOT NULL DEFAULT 'system',
    ADD COLUMN learner_id TEXT,
    ADD COLUMN source_deck_id BIGINT REFERENCES decks(id) ON DELETE SET NULL;

CREATE TABLE deck_cards (
    id      BIGSERIAL PRIMARY KEY,
    deck_id BIGINT NOT NULL REFERENCES decks(id) ON DELETE CASCADE,
    card_id BIGINT NOT NULL REFERENCES cards(id),
    UNIQUE (deck_id, card_id)
);

-- backfill existing cards into deck_cards
INSERT INTO deck_cards (deck_id, card_id)
SELECT deck_id, id FROM cards;
