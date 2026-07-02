package db

import (
	"time"

	"github.com/uptrace/bun"
)

type Deck struct {
	bun.BaseModel `bun:"table:decks"`
	ID        int64     `bun:"id,pk,autoincrement" json:"id"`
	ModuleID  string    `bun:"module_id,notnull" json:"module_id"`
	CreatedAt time.Time `bun:"created_at,notnull,default:current_timestamp" json:"created_at"`
	Cards     []*Card   `bun:"rel:has-many,join:id=deck_id" json:"cards,omitempty"`
}

type Card struct {
	bun.BaseModel `bun:"table:cards"`
	ID            int64     `bun:"id,pk,autoincrement" json:"id"`
	DeckID        int64     `bun:"deck_id,notnull" json:"deck_id"`
	Question      string    `bun:"question,notnull" json:"question"`
	CorrectAnswer string    `bun:"correct_answer,notnull" json:"correct_answer"`
	Distractors   []string  `bun:"distractors,array,notnull" json:"distractors"`
	CreatedAt     time.Time `bun:"created_at,notnull,default:current_timestamp" json:"created_at"`
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
