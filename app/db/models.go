package db

import (
	"time"

	"github.com/uptrace/bun"
)

const DeckTypeSystem = "system"
const DeckTypeUser = "user"

type Deck struct {
	bun.BaseModel `bun:"table:decks"`
	ID           int64     `bun:"id,pk,autoincrement" json:"id"`
	ModuleID     string    `bun:"module_id,notnull" json:"module_id"`
	DeckType     string    `bun:"deck_type,notnull" json:"deck_type"`
	LearnerID    string    `bun:"learner_id" json:"learner_id,omitempty"`
	SourceDeckID *int64    `bun:"source_deck_id" json:"source_deck_id,omitempty"`
	CreatedAt    time.Time `bun:"created_at,notnull,default:current_timestamp" json:"created_at"`
	Cards        []*Card   `bun:"-" json:"cards,omitempty"`
}

type Card struct {
	bun.BaseModel        `bun:"table:cards"`
	ID                   int64     `bun:"id,pk,autoincrement" json:"id"`
	DeckID               int64     `bun:"deck_id,notnull" json:"deck_id"`
	Question             string    `bun:"question,notnull" json:"question"`
	CorrectAnswer        string    `bun:"correct_answer,notnull" json:"correct_answer"`
	Distractors          []string  `bun:"distractors,array,notnull" json:"distractors"`
	QuestionJa           string    `bun:"question_ja,notnull" json:"question_ja"`
	CorrectAnswerJa      string    `bun:"correct_answer_ja,notnull" json:"correct_answer_ja"`
	DistractorsJa        []string  `bun:"distractors_ja,array,notnull" json:"distractors_ja"`
	SourceConceptID      string    `bun:"source_concept_id,notnull" json:"source_concept_id,omitempty"`
	SourceConceptTitle   string    `bun:"source_concept_title,notnull" json:"source_concept_title,omitempty"`
	CreatedAt            time.Time `bun:"created_at,notnull,default:current_timestamp" json:"created_at"`
}

type DeckCard struct {
	bun.BaseModel `bun:"table:deck_cards"`
	ID     int64 `bun:"id,pk,autoincrement" json:"id"`
	DeckID int64 `bun:"deck_id,notnull" json:"deck_id"`
	CardID int64 `bun:"card_id,notnull" json:"card_id"`
}

type SRSCard struct {
	bun.BaseModel `bun:"table:srs_cards"`
	ID         int64     `bun:"id,pk,autoincrement" json:"id"`
	CardID     int64     `bun:"card_id,notnull" json:"card_id"`
	LearnerID  string    `bun:"learner_id,notnull" json:"learner_id"`
	Due        time.Time `bun:"due,notnull" json:"due"`
	Stability  float64   `bun:"stability,notnull,default:0" json:"stability"`
	Difficulty float64   `bun:"difficulty,notnull,default:0" json:"difficulty"`
	Reps       int       `bun:"reps,notnull,default:0" json:"reps"`
	Lapses     int       `bun:"lapses,notnull,default:0" json:"lapses"`
	State      int       `bun:"state,notnull,default:0" json:"state"`
	LastReview time.Time `bun:"last_review" json:"last_review"`
}
