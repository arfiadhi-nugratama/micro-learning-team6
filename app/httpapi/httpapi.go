package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/dojo-product/team6/db"
	grpcclient "github.com/dojo-product/team6/grpc"
	"github.com/dojo-product/team6/llm"
	"github.com/dojo-product/team6/srs"
	"github.com/uptrace/bun"
	"github.com/uptrace/bunrouter"
)

func RegisterRoutes(router *bunrouter.Router, database *bun.DB, grpcClient *grpcclient.Client, apiKey string) {
	router.POST("/modules/:moduleID/deck", func(w http.ResponseWriter, req bunrouter.Request) error {
		moduleID := req.Param("moduleID")
		locale := req.URL.Query().Get("locale")
		if locale == "" {
			locale = "en"
		}

		content, err := grpcClient.GetModuleContent(req.Context(), moduleID, locale)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return nil
		}

		cards, err := llm.Generate(req.Context(), llm.Prompt, content)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return nil
		}

		deck := &db.Deck{
			ModuleID:  moduleID,
			CreatedAt: time.Now(),
		}
		if _, err := database.NewInsert().Model(deck).Returning("*").Exec(req.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return nil
		}

		dbCards := make([]*db.Card, 0, len(cards))
		for _, c := range cards {
			dbCards = append(dbCards, &db.Card{
				DeckID:        deck.ID,
				Question:      c.Question,
				CorrectAnswer: c.CorrectAnswer,
				Distractors:   c.Distractors,
				CreatedAt:     time.Now(),
			})
		}

		if len(dbCards) > 0 {
			if _, err := database.NewInsert().Model(&dbCards).Returning("*").Exec(req.Context()); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return nil
			}
		}

		deck.Cards = dbCards
		for _, c := range deck.Cards {
			c.Distractors = truncate(c.Distractors, 3)
		}

		w.Header().Set("Content-Type", "application/json")
		return json.NewEncoder(w).Encode(deck)
	})

	router.GET("/modules/:moduleID/deck", func(w http.ResponseWriter, req bunrouter.Request) error {
		moduleID := req.Param("moduleID")

		var deck db.Deck
		err := database.NewSelect().Model(&deck).
			Where("deck.module_id = ?", moduleID).
			OrderExpr("deck.created_at DESC").
			Limit(1).
			Relation("Cards").
			Scan(req.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return nil
		}

		for _, c := range deck.Cards {
			c.Distractors = truncate(c.Distractors, 3)
		}

		w.Header().Set("Content-Type", "application/json")
		return json.NewEncoder(w).Encode(deck)
	})

	router.DELETE("/decks/:deckID", func(w http.ResponseWriter, req bunrouter.Request) error {
		deckIDStr := req.Param("deckID")
		deckID, err := strconv.ParseInt(deckIDStr, 10, 64)
		if err != nil {
			http.Error(w, "invalid deckID", http.StatusBadRequest)
			return nil
		}

		res, err := database.NewDelete().Model((*db.Deck)(nil)).Where("id = ?", deckID).Exec(req.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return nil
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			http.Error(w, "deck not found", http.StatusNotFound)
			return nil
		}

		w.WriteHeader(http.StatusNoContent)
		return nil
	})

	router.GET("/decks/:deckID/review", func(w http.ResponseWriter, req bunrouter.Request) error {
		deckIDStr := req.Param("deckID")
		deckID, err := strconv.ParseInt(deckIDStr, 10, 64)
		if err != nil {
			http.Error(w, "invalid deckID", http.StatusBadRequest)
			return nil
		}

		learnerID := req.URL.Query().Get("learner_id")
		if learnerID == "" {
			http.Error(w, "learner_id required", http.StatusBadRequest)
			return nil
		}

		var cards []*db.Card
		if err := database.NewSelect().Model(&cards).Where("deck_id = ?", deckID).Scan(req.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return nil
		}

		now := time.Now()
		var result []*db.Card
		for _, card := range cards {
			var srsCard db.SRSCard
			err := database.NewSelect().Model(&srsCard).
				Where("card_id = ? AND learner_id = ?", card.ID, learnerID).
				Scan(req.Context())
			if err != nil {
				// no SRS record — include as due
				card.Distractors = truncate(card.Distractors, 3)
				result = append(result, card)
				continue
			}
			if !srsCard.Due.After(now) {
				card.Distractors = truncate(card.Distractors, 3)
				result = append(result, card)
			}
		}

		w.Header().Set("Content-Type", "application/json")
		return json.NewEncoder(w).Encode(result)
	})

	router.POST("/cards/:cardID/review", func(w http.ResponseWriter, req bunrouter.Request) error {
		cardIDStr := req.Param("cardID")
		cardID, err := strconv.ParseInt(cardIDStr, 10, 64)
		if err != nil {
			http.Error(w, "invalid cardID", http.StatusBadRequest)
			return nil
		}

		var body struct {
			LearnerID string `json:"learner_id"`
			Rating    int    `json:"rating"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return nil
		}
		if body.LearnerID == "" {
			http.Error(w, "learner_id required", http.StatusBadRequest)
			return nil
		}

		var srsCard db.SRSCard
		err = database.NewSelect().Model(&srsCard).
			Where("card_id = ? AND learner_id = ?", cardID, body.LearnerID).
			Scan(req.Context())
		if err != nil {
			srsCard = *srs.InitCard(cardID, body.LearnerID)
		}

		if err := srs.ReviewCard(&srsCard, body.Rating); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return nil
		}

		if srsCard.ID == 0 {
			if _, err := database.NewInsert().Model(&srsCard).Returning("*").Exec(req.Context()); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return nil
			}
		} else {
			if _, err := database.NewUpdate().Model(&srsCard).WherePK().Exec(req.Context()); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return nil
			}
		}

		w.Header().Set("Content-Type", "application/json")
		return json.NewEncoder(w).Encode(srsCard)
	})
}

func truncate(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
