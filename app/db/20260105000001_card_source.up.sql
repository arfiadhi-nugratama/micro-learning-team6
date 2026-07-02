ALTER TABLE cards
    ADD COLUMN source_concept_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN source_concept_title TEXT NOT NULL DEFAULT '';
