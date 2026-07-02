package srs

import (
	"fmt"
	"time"

	"github.com/dojo-product/team6/db"
	fsrs "github.com/open-spaced-repetition/go-fsrs/v3"
)

var f = fsrs.NewFSRS(fsrs.DefaultParam())

func InitCard(cardID int64, learnerID string) *db.SRSCard {
	return &db.SRSCard{
		CardID:    cardID,
		LearnerID: learnerID,
		Due:       time.Now(),
		State:     int(fsrs.New),
	}
}

func ReviewCard(srsCard *db.SRSCard, rating int) error {
	if rating < 1 || rating > 4 {
		return fmt.Errorf("rating must be 1-4, got %d", rating)
	}

	card := fsrs.Card{
		Due:        srsCard.Due,
		Stability:  srsCard.Stability,
		Difficulty: srsCard.Difficulty,
		ElapsedDays: 0,
		ScheduledDays: 0,
		Reps:       uint64(srsCard.Reps),
		Lapses:     uint64(srsCard.Lapses),
		State:      fsrs.State(srsCard.State),
		LastReview: srsCard.LastReview,
	}

	now := time.Now()
	schedulingCards := f.Repeat(card, now)

	var r fsrs.Rating
	switch rating {
	case 1:
		r = fsrs.Again
	case 2:
		r = fsrs.Hard
	case 3:
		r = fsrs.Good
	case 4:
		r = fsrs.Easy
	}

	updated := schedulingCards[r].Card

	srsCard.Due = updated.Due
	srsCard.Stability = updated.Stability
	srsCard.Difficulty = updated.Difficulty
	srsCard.Reps = int(updated.Reps)
	srsCard.Lapses = int(updated.Lapses)
	srsCard.State = int(updated.State)
	srsCard.LastReview = now

	return nil
}
