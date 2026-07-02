package httpapi

import (
	"context"
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
	// --- system deck endpoints ---

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
			DeckType:  db.DeckTypeSystem,
			CreatedAt: time.Now(),
		}
		if _, err := database.NewInsert().Model(deck).Returning("*").Exec(req.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return nil
		}

		dbCards := make([]*db.Card, 0, len(cards))
		for _, c := range cards {
			dbCards = append(dbCards, &db.Card{
				DeckID:          deck.ID,
				Question:        c.Question,
				CorrectAnswer:   c.CorrectAnswer,
				Distractors:     c.Distractors,
				QuestionJa:      c.QuestionJa,
				CorrectAnswerJa: c.CorrectAnswerJa,
				DistractorsJa:   c.DistractorsJa,
				CreatedAt:       time.Now(),
			})
		}

		if len(dbCards) > 0 {
			if _, err := database.NewInsert().Model(&dbCards).Returning("*").Exec(req.Context()); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return nil
			}
			deckCards := make([]*db.DeckCard, 0, len(dbCards))
			for _, c := range dbCards {
				deckCards = append(deckCards, &db.DeckCard{DeckID: deck.ID, CardID: c.ID})
			}
			if _, err := database.NewInsert().Model(&deckCards).Exec(req.Context()); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return nil
			}
		}

		deck.Cards = dbCards
		truncateDeck(deck)

		w.Header().Set("Content-Type", "application/json")
		return json.NewEncoder(w).Encode(deck)
	})

	router.GET("/modules/:moduleID/deck", func(w http.ResponseWriter, req bunrouter.Request) error {
		moduleID := req.Param("moduleID")

		var deck db.Deck
		err := database.NewSelect().Model(&deck).
			Where("deck.module_id = ? AND deck.deck_type = ?", moduleID, db.DeckTypeSystem).
			OrderExpr("deck.created_at DESC").
			Limit(1).
			Scan(req.Context())
		if err != nil {
			http.Error(w, "deck not found", http.StatusNotFound)
			return nil
		}

		if err := loadDeckCards(req.Context(), database, &deck); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return nil
		}
		truncateDeck(&deck)

		w.Header().Set("Content-Type", "application/json")
		return json.NewEncoder(w).Encode(deck)
	})

	router.DELETE("/decks/:deckID", func(w http.ResponseWriter, req bunrouter.Request) error {
		deckID, err := parseDeckID(req)
		if err != nil {
			http.Error(w, "invalid deckID", http.StatusBadRequest)
			return nil
		}

		// delete cards owned by this deck, then the deck (deck_cards cascade)
		if _, err := database.NewDelete().Model((*db.Card)(nil)).Where("deck_id = ?", deckID).Exec(req.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
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

	// --- user deck endpoints ---

	router.POST("/decks/:deckID/copy", func(w http.ResponseWriter, req bunrouter.Request) error {
		sourceDeckID, err := parseDeckID(req)
		if err != nil {
			http.Error(w, "invalid deckID", http.StatusBadRequest)
			return nil
		}

		var body struct {
			LearnerID string `json:"learner_id"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.LearnerID == "" {
			http.Error(w, "learner_id required", http.StatusBadRequest)
			return nil
		}

		// verify source deck exists
		var src db.Deck
		if err := database.NewSelect().Model(&src).Where("id = ?", sourceDeckID).Scan(req.Context()); err != nil {
			http.Error(w, "source deck not found", http.StatusNotFound)
			return nil
		}

		userDeck := &db.Deck{
			ModuleID:     src.ModuleID,
			DeckType:     db.DeckTypeUser,
			LearnerID:    body.LearnerID,
			SourceDeckID: &sourceDeckID,
			CreatedAt:    time.Now(),
		}
		if _, err := database.NewInsert().Model(userDeck).Returning("*").Exec(req.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return nil
		}

		// shallow copy: duplicate deck_cards rows pointing to same card IDs
		if _, err := database.NewRaw(`
			INSERT INTO deck_cards (deck_id, card_id)
			SELECT ?, card_id FROM deck_cards WHERE deck_id = ?
		`, userDeck.ID, sourceDeckID).Exec(req.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return nil
		}

		if err := loadDeckCards(req.Context(), database, userDeck); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return nil
		}
		truncateDeck(userDeck)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		return json.NewEncoder(w).Encode(userDeck)
	})

	router.GET("/learners/:learnerID/decks", func(w http.ResponseWriter, req bunrouter.Request) error {
		learnerID := req.Param("learnerID")

		var decks []db.Deck
		if err := database.NewSelect().Model(&decks).
			Where("learner_id = ? AND deck_type = ?", learnerID, db.DeckTypeUser).
			OrderExpr("created_at DESC").
			Scan(req.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return nil
		}

		for i := range decks {
			if err := loadDeckCards(req.Context(), database, &decks[i]); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return nil
			}
			truncateDeck(&decks[i])
		}

		w.Header().Set("Content-Type", "application/json")
		return json.NewEncoder(w).Encode(decks)
	})

	router.PUT("/user-decks/:deckID/cards/:cardID", func(w http.ResponseWriter, req bunrouter.Request) error {
		deckID, err := parseDeckID(req)
		if err != nil {
			http.Error(w, "invalid deckID", http.StatusBadRequest)
			return nil
		}
		cardID, err := parseID(req.Param("cardID"))
		if err != nil {
			http.Error(w, "invalid cardID", http.StatusBadRequest)
			return nil
		}

		var deck db.Deck
		if err := database.NewSelect().Model(&deck).Where("id = ? AND deck_type = ?", deckID, db.DeckTypeUser).Scan(req.Context()); err != nil {
			http.Error(w, "user deck not found", http.StatusNotFound)
			return nil
		}

		var body struct {
			Question        string   `json:"question"`
			CorrectAnswer   string   `json:"correct_answer"`
			Distractors     []string `json:"distractors"`
			QuestionJa      string   `json:"question_ja"`
			CorrectAnswerJa string   `json:"correct_answer_ja"`
			DistractorsJa   []string `json:"distractors_ja"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return nil
		}

		// check current card ownership — COW only if card belongs to a different deck
		var existing db.Card
		if err := database.NewSelect().Model(&existing).Where("id = ?", cardID).Scan(req.Context()); err != nil {
			http.Error(w, "card not found", http.StatusNotFound)
			return nil
		}

		var newCardID int64
		if existing.DeckID == deckID {
			// already owned by this user deck — update in place
			existing.Question = body.Question
			existing.CorrectAnswer = body.CorrectAnswer
			existing.Distractors = body.Distractors
			existing.QuestionJa = body.QuestionJa
			existing.CorrectAnswerJa = body.CorrectAnswerJa
			existing.DistractorsJa = body.DistractorsJa
			if _, err := database.NewUpdate().Model(&existing).WherePK().Exec(req.Context()); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return nil
			}
			newCardID = existing.ID
		} else {
			// COW: clone card owned by user deck
			newCard := &db.Card{
				DeckID:          deckID,
				Question:        body.Question,
				CorrectAnswer:   body.CorrectAnswer,
				Distractors:     body.Distractors,
				QuestionJa:      body.QuestionJa,
				CorrectAnswerJa: body.CorrectAnswerJa,
				DistractorsJa:   body.DistractorsJa,
				CreatedAt:       time.Now(),
			}
			if _, err := database.NewInsert().Model(newCard).Returning("*").Exec(req.Context()); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return nil
			}
			// swap junction row
			if _, err := database.NewUpdate().Model((*db.DeckCard)(nil)).
				Set("card_id = ?", newCard.ID).
				Where("deck_id = ? AND card_id = ?", deckID, cardID).
				Exec(req.Context()); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return nil
			}
			newCardID = newCard.ID
		}

		var result db.Card
		if err := database.NewSelect().Model(&result).Where("id = ?", newCardID).Scan(req.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return nil
		}
		result.Distractors = truncate(result.Distractors, 3)
		result.DistractorsJa = truncate(result.DistractorsJa, 3)

		w.Header().Set("Content-Type", "application/json")
		return json.NewEncoder(w).Encode(result)
	})

	router.POST("/user-decks/:deckID/cards", func(w http.ResponseWriter, req bunrouter.Request) error {
		deckID, err := parseDeckID(req)
		if err != nil {
			http.Error(w, "invalid deckID", http.StatusBadRequest)
			return nil
		}

		var deck db.Deck
		if err := database.NewSelect().Model(&deck).Where("id = ? AND deck_type = ?", deckID, db.DeckTypeUser).Scan(req.Context()); err != nil {
			http.Error(w, "user deck not found", http.StatusNotFound)
			return nil
		}

		var body struct {
			Question        string   `json:"question"`
			CorrectAnswer   string   `json:"correct_answer"`
			Distractors     []string `json:"distractors"`
			QuestionJa      string   `json:"question_ja"`
			CorrectAnswerJa string   `json:"correct_answer_ja"`
			DistractorsJa   []string `json:"distractors_ja"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return nil
		}

		card := &db.Card{
			DeckID:          deckID,
			Question:        body.Question,
			CorrectAnswer:   body.CorrectAnswer,
			Distractors:     body.Distractors,
			QuestionJa:      body.QuestionJa,
			CorrectAnswerJa: body.CorrectAnswerJa,
			DistractorsJa:   body.DistractorsJa,
			CreatedAt:       time.Now(),
		}
		if _, err := database.NewInsert().Model(card).Returning("*").Exec(req.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return nil
		}
		if _, err := database.NewInsert().Model(&db.DeckCard{DeckID: deckID, CardID: card.ID}).Exec(req.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return nil
		}

		card.Distractors = truncate(card.Distractors, 3)
		card.DistractorsJa = truncate(card.DistractorsJa, 3)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		return json.NewEncoder(w).Encode(card)
	})

	router.DELETE("/user-decks/:deckID/cards/:cardID", func(w http.ResponseWriter, req bunrouter.Request) error {
		deckID, err := parseDeckID(req)
		if err != nil {
			http.Error(w, "invalid deckID", http.StatusBadRequest)
			return nil
		}
		cardID, err := parseID(req.Param("cardID"))
		if err != nil {
			http.Error(w, "invalid cardID", http.StatusBadRequest)
			return nil
		}

		var deck db.Deck
		if err := database.NewSelect().Model(&deck).Where("id = ? AND deck_type = ?", deckID, db.DeckTypeUser).Scan(req.Context()); err != nil {
			http.Error(w, "user deck not found", http.StatusNotFound)
			return nil
		}

		// remove junction row only; if card is owned by this deck, also delete it
		res, err := database.NewDelete().Model((*db.DeckCard)(nil)).
			Where("deck_id = ? AND card_id = ?", deckID, cardID).
			Exec(req.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return nil
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			http.Error(w, "card not in deck", http.StatusNotFound)
			return nil
		}

		// clean up card if owned by this user deck
		database.NewDelete().Model((*db.Card)(nil)).Where("id = ? AND deck_id = ?", cardID, deckID).Exec(req.Context()) //nolint

		w.WriteHeader(http.StatusNoContent)
		return nil
	})

	router.DELETE("/user-decks/:deckID", func(w http.ResponseWriter, req bunrouter.Request) error {
		deckID, err := parseDeckID(req)
		if err != nil {
			http.Error(w, "invalid deckID", http.StatusBadRequest)
			return nil
		}

		var deck db.Deck
		if err := database.NewSelect().Model(&deck).Where("id = ? AND deck_type = ?", deckID, db.DeckTypeUser).Scan(req.Context()); err != nil {
			http.Error(w, "user deck not found", http.StatusNotFound)
			return nil
		}

		// delete cards owned by this user deck, then deck (deck_cards cascade)
		database.NewDelete().Model((*db.Card)(nil)).Where("deck_id = ?", deckID).Exec(req.Context()) //nolint
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

	// --- review endpoints ---

	router.GET("/decks/:deckID/review", func(w http.ResponseWriter, req bunrouter.Request) error {
		deckID, err := parseDeckID(req)
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
		if err := database.NewSelect().Model(&cards).
			Join("JOIN deck_cards dc ON dc.card_id = card.id").
			Where("dc.deck_id = ?", deckID).
			Scan(req.Context()); err != nil {
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
				card.Distractors = truncate(card.Distractors, 3)
				card.DistractorsJa = truncate(card.DistractorsJa, 3)
				result = append(result, card)
				continue
			}
			if !srsCard.Due.After(now) {
				card.Distractors = truncate(card.Distractors, 3)
				card.DistractorsJa = truncate(card.DistractorsJa, 3)
				result = append(result, card)
			}
		}

		w.Header().Set("Content-Type", "application/json")
		return json.NewEncoder(w).Encode(result)
	})

	router.POST("/cards/:cardID/review", func(w http.ResponseWriter, req bunrouter.Request) error {
		cardID, err := parseID(req.Param("cardID"))
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

func loadDeckCards(ctx context.Context, database *bun.DB, deck *db.Deck) error {
	var cards []*db.Card
	err := database.NewSelect().Model(&cards).
		Join("JOIN deck_cards dc ON dc.card_id = card.id").
		Where("dc.deck_id = ?", deck.ID).
		Scan(ctx)
	if err != nil {
		return err
	}
	deck.Cards = cards
	return nil
}

func truncateDeck(deck *db.Deck) {
	for _, c := range deck.Cards {
		c.Distractors = truncate(c.Distractors, 3)
		c.DistractorsJa = truncate(c.DistractorsJa, 3)
	}
}

func parseDeckID(req bunrouter.Request) (int64, error) {
	return parseID(req.Param("deckID"))
}

func parseID(s string) (int64, error) {
	return strconv.ParseInt(s, 10, 64)
}

func truncate(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
