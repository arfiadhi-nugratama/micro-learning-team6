ALTER TABLE cards DROP CONSTRAINT cards_deck_id_fkey;
ALTER TABLE cards ADD CONSTRAINT cards_deck_id_fkey FOREIGN KEY (deck_id) REFERENCES decks(id) ON DELETE CASCADE;

ALTER TABLE srs_cards DROP CONSTRAINT srs_cards_card_id_fkey;
ALTER TABLE srs_cards ADD CONSTRAINT srs_cards_card_id_fkey FOREIGN KEY (card_id) REFERENCES cards(id) ON DELETE CASCADE;
