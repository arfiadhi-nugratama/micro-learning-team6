ALTER TABLE cards
    ADD COLUMN question_ja TEXT NOT NULL DEFAULT '',
    ADD COLUMN correct_answer_ja TEXT NOT NULL DEFAULT '',
    ADD COLUMN distractors_ja TEXT[] NOT NULL DEFAULT '{}';
