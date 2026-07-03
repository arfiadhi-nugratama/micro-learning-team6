package httpapi

import (
	"context"
	"encoding/json"
	"log"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/dojo-product/team6/db"
	grpcclient "github.com/dojo-product/team6/grpc"
	"github.com/dojo-product/team6/llm"
	"github.com/dojo-product/team6/srs"
	"github.com/uptrace/bun"
	"github.com/uptrace/bunrouter"
)

// loggingMiddleware logs method, path, status, and duration for every request.
func loggingMiddleware(next bunrouter.HandlerFunc) bunrouter.HandlerFunc {
	return func(w http.ResponseWriter, req bunrouter.Request) error {
		start := time.Now()
		rw := &statusWriter{ResponseWriter: w}
		err := next(rw, req)
		slog.InfoContext(req.Context(), "http request",
			"method", req.Method,
			"path", req.URL.Path,
			"status", rw.status,
			"duration_ms", time.Since(start).Milliseconds(),
		)
		return err
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.status = code
	sw.ResponseWriter.WriteHeader(code)
}

func (sw *statusWriter) Write(b []byte) (int, error) {
	if sw.status == 0 {
		sw.status = http.StatusOK
	}
	return sw.ResponseWriter.Write(b)
}

// errorMiddleware catches any error returned by a handler, logs it, and writes
// a 500 response. Client errors (already written via http.Error) return nil so
// they never reach here.
func errorMiddleware(next bunrouter.HandlerFunc) bunrouter.HandlerFunc {
	return func(w http.ResponseWriter, req bunrouter.Request) error {
		err := next(w, req)
		if err != nil {
			log.Printf("ERROR %s %s: %v", req.Method, req.URL.Path, err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}
		return nil
	}
}

func RegisterRoutes(router *bunrouter.Router, database *bun.DB, grpcClient *grpcclient.Client, apiKey string) {
	g := router.Use(loggingMiddleware, errorMiddleware)
	// --- system deck endpoints ---

	g.POST("/modules/:moduleID/deck", func(w http.ResponseWriter, req bunrouter.Request) error {
		moduleID := req.Param("moduleID")
		var body struct {
			Title string `json:"title"`
		}
		// body is optional — ignore decode error (empty body = empty title)
		json.NewDecoder(req.Body).Decode(&body) //nolint

		t0 := time.Now()
		moduleContent, err := grpcClient.GetModuleContent(req.Context(), moduleID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return nil
		}
		slog.InfoContext(req.Context(), "fetched module content",
			"module_id", moduleID,
			"unit_count", len(moduleContent.Units),
			"duration_ms", time.Since(t0).Milliseconds(),
		)

		type result struct {
			cards []llm.CardData
			err   error
		}
		results := make([]result, len(moduleContent.Units))
		var wg sync.WaitGroup
		t1 := time.Now()
		slog.InfoContext(req.Context(), "starting llm generation",
			"module_id", moduleID,
			"unit_count", len(moduleContent.Units),
		)
		for i, u := range moduleContent.Units {
			wg.Add(1)
			go func(idx int, unitTitle, content string) {
				defer wg.Done()
				ts := time.Now()
				slog.InfoContext(req.Context(), "llm generate start", "unit", unitTitle, "content_len", len(content))
				c, e := llm.Generate(req.Context(), llm.Prompt, content)
				slog.InfoContext(req.Context(), "llm generate",
					"unit", unitTitle,
					"content_len", len(content),
					"card_count", len(c),
					"duration_ms", time.Since(ts).Milliseconds(),
				)
				results[idx] = result{cards: c, err: e}
			}(i, u.UnitTitle, u.Content)
		}
		wg.Wait()
		slog.InfoContext(req.Context(), "all units generated",
			"module_id", moduleID,
			"duration_ms", time.Since(t1).Milliseconds(),
		)

		var combined []llm.CardData
		for _, r := range results {
			if r.err != nil {
				return r.err
			}
			combined = append(combined, r.cards...)
		}

		t2 := time.Now()
		slog.InfoContext(req.Context(), "starting deduplication",
			"module_id", moduleID,
			"combined_card_count", len(combined),
		)
		cards, err := llm.Deduplicate(req.Context(), combined)
		if err != nil {
			return err
		}
		slog.InfoContext(req.Context(), "deduplication done",
			"module_id", moduleID,
			"card_count", len(cards),
			"duration_ms", time.Since(t2).Milliseconds(),
		)

		title := moduleContent.TitleEN
		if body.Title != "" {
			title = body.Title
		}
		deck := &db.Deck{
			ModuleID:  moduleID,
			Title:     title,
			TitleJa:   moduleContent.TitleJA,
			DeckType:  db.DeckTypeSystem,
			CreatedAt: time.Now(),
		}
		if _, err := database.NewInsert().Model(deck).Returning("*").Exec(req.Context()); err != nil {
			return err
		}

		dbCards := make([]*db.Card, 0, len(cards))
		for _, c := range cards {
			dbCards = append(dbCards, &db.Card{
				DeckID:             deck.ID,
				Question:           c.Question,
				CorrectAnswer:      c.CorrectAnswer,
				Distractors:        c.Distractors,
				QuestionJa:         c.QuestionJa,
				CorrectAnswerJa:    c.CorrectAnswerJa,
				DistractorsJa:      c.DistractorsJa,
				SourceConceptID:    c.SourceConceptID,
				SourceConceptTitle: c.SourceConceptTitle,
				CreatedAt:          time.Now(),
			})
		}

		if len(dbCards) > 0 {
			if _, err := database.NewInsert().Model(&dbCards).Returning("*").Exec(req.Context()); err != nil {
				return err
			}
			deckCards := make([]*db.DeckCard, 0, len(dbCards))
			for _, c := range dbCards {
				deckCards = append(deckCards, &db.DeckCard{DeckID: deck.ID, CardID: c.ID})
			}
			if _, err := database.NewInsert().Model(&deckCards).Exec(req.Context()); err != nil {
				return err
			}
		}

		deck.Cards = dbCards
		truncateDeck(deck)

		w.Header().Set("Content-Type", "application/json")
		return json.NewEncoder(w).Encode(deck)
	})

	g.GET("/modules/:moduleID/deck", func(w http.ResponseWriter, req bunrouter.Request) error {
		moduleID := req.Param("moduleID")

		var deck db.Deck
		err := database.NewSelect().Model(&deck).
			Where("deck.module_id = ? AND deck.deck_type = ? AND deck.deleted_at IS NULL", moduleID, db.DeckTypeSystem).
			OrderExpr("deck.created_at DESC").
			Limit(1).
			Scan(req.Context())
		if err != nil {
			http.Error(w, "deck not found", http.StatusNotFound)
			return nil
		}

		if err := loadDeckCards(req.Context(), database, &deck); err != nil {
			return err
		}
		truncateDeck(&deck)

		w.Header().Set("Content-Type", "application/json")
		return json.NewEncoder(w).Encode(deck)
	})

	g.GET("/decks", func(w http.ResponseWriter, req bunrouter.Request) error {
		type deckSummary struct {
			db.Deck
			CardCount int `bun:"card_count" json:"card_count"`
		}

		var rows []deckSummary
		if err := database.NewSelect().
			TableExpr("decks AS deck").
			ColumnExpr("deck.*").
			ColumnExpr("COUNT(dc.id) AS card_count").
			Join("LEFT JOIN deck_cards dc ON dc.deck_id = deck.id").
			Where("deck.deck_type = ? AND deck.deleted_at IS NULL", db.DeckTypeSystem).
			GroupExpr("deck.id").
			OrderExpr("deck.created_at DESC").
			Scan(req.Context(), &rows); err != nil {
			return err
		}

		w.Header().Set("Content-Type", "application/json")
		return json.NewEncoder(w).Encode(rows)
	})

	g.GET("/decks/:deckID", func(w http.ResponseWriter, req bunrouter.Request) error {
		deckID, err := parseDeckID(req)
		if err != nil {
			http.Error(w, "invalid deckID", http.StatusBadRequest)
			return nil
		}

		var deck db.Deck
		if err := database.NewSelect().Model(&deck).
			Where("id = ? AND deck_type = ? AND deleted_at IS NULL", deckID, db.DeckTypeSystem).
			Scan(req.Context()); err != nil {
			http.Error(w, "deck not found", http.StatusNotFound)
			return nil
		}

		if err := loadDeckCards(req.Context(), database, &deck); err != nil {
			return err
		}
		truncateDeck(&deck)

		w.Header().Set("Content-Type", "application/json")
		return json.NewEncoder(w).Encode(deck)
	})

	g.PUT("/decks/:deckID/cards/:cardID", func(w http.ResponseWriter, req bunrouter.Request) error {
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
		if err := database.NewSelect().Model(&deck).Where("id = ? AND deck_type = ? AND deleted_at IS NULL", deckID, db.DeckTypeSystem).Scan(req.Context()); err != nil {
			http.Error(w, "system deck not found", http.StatusNotFound)
			return nil
		}

		var body struct {
			Question           string   `json:"question"`
			CorrectAnswer      string   `json:"correct_answer"`
			Distractors        []string `json:"distractors"`
			QuestionJa         string   `json:"question_ja"`
			CorrectAnswerJa    string   `json:"correct_answer_ja"`
			DistractorsJa      []string `json:"distractors_ja"`
			SourceConceptID    string   `json:"source_concept_id"`
			SourceConceptTitle string   `json:"source_concept_title"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return nil
		}

		var card db.Card
		if err := database.NewSelect().Model(&card).
			Join("JOIN deck_cards dc ON dc.card_id = card.id").
			Where("dc.deck_id = ? AND card.id = ? AND card.deleted_at IS NULL", deckID, cardID).
			Scan(req.Context()); err != nil {
			http.Error(w, "card not found in deck", http.StatusNotFound)
			return nil
		}

		card.Question = body.Question
		card.CorrectAnswer = body.CorrectAnswer
		card.Distractors = body.Distractors
		card.QuestionJa = body.QuestionJa
		card.CorrectAnswerJa = body.CorrectAnswerJa
		card.DistractorsJa = body.DistractorsJa
		if body.SourceConceptID != "" {
			card.SourceConceptID = body.SourceConceptID
		}
		if body.SourceConceptTitle != "" {
			card.SourceConceptTitle = body.SourceConceptTitle
		}

		if _, err := database.NewUpdate().Model(&card).WherePK().Exec(req.Context()); err != nil {
			return err
		}

		prepareCard(&card)

		w.Header().Set("Content-Type", "application/json")
		return json.NewEncoder(w).Encode(card)
	})

	g.POST("/decks/:deckID/cards", func(w http.ResponseWriter, req bunrouter.Request) error {
		deckID, err := parseDeckID(req)
		if err != nil {
			http.Error(w, "invalid deckID", http.StatusBadRequest)
			return nil
		}

		var deck db.Deck
		if err := database.NewSelect().Model(&deck).Where("id = ? AND deck_type = ? AND deleted_at IS NULL", deckID, db.DeckTypeSystem).Scan(req.Context()); err != nil {
			http.Error(w, "system deck not found", http.StatusNotFound)
			return nil
		}

		var body struct {
			Question           string   `json:"question"`
			CorrectAnswer      string   `json:"correct_answer"`
			Distractors        []string `json:"distractors"`
			QuestionJa         string   `json:"question_ja"`
			CorrectAnswerJa    string   `json:"correct_answer_ja"`
			DistractorsJa      []string `json:"distractors_ja"`
			SourceConceptID    string   `json:"source_concept_id"`
			SourceConceptTitle string   `json:"source_concept_title"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return nil
		}

		card := &db.Card{
			DeckID:             deckID,
			Question:           body.Question,
			CorrectAnswer:      body.CorrectAnswer,
			Distractors:        body.Distractors,
			QuestionJa:         body.QuestionJa,
			CorrectAnswerJa:    body.CorrectAnswerJa,
			DistractorsJa:      body.DistractorsJa,
			SourceConceptID:    body.SourceConceptID,
			SourceConceptTitle: body.SourceConceptTitle,
			CreatedAt:          time.Now(),
		}
		if _, err := database.NewInsert().Model(card).Returning("*").Exec(req.Context()); err != nil {
			return err
		}
		if _, err := database.NewInsert().Model(&db.DeckCard{DeckID: deckID, CardID: card.ID}).Exec(req.Context()); err != nil {
			return err
		}

		prepareCard(card)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		return json.NewEncoder(w).Encode(card)
	})

	g.DELETE("/decks/:deckID/cards/:cardID", func(w http.ResponseWriter, req bunrouter.Request) error {
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

		now := time.Now()
		res, err := database.NewUpdate().Model((*db.Card)(nil)).
			Set("deleted_at = ?", now).
			Where("id = ? AND deck_id = ? AND deleted_at IS NULL", cardID, deckID).
			Exec(req.Context())
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			http.Error(w, "card not found in deck", http.StatusNotFound)
			return nil
		}

		w.WriteHeader(http.StatusNoContent)
		return nil
	})

	g.DELETE("/decks/:deckID", func(w http.ResponseWriter, req bunrouter.Request) error {
		deckID, err := parseDeckID(req)
		if err != nil {
			http.Error(w, "invalid deckID", http.StatusBadRequest)
			return nil
		}

		now := time.Now()
		res, err := database.NewUpdate().Model((*db.Deck)(nil)).
			Set("deleted_at = ?", now).
			Where("id = ? AND deleted_at IS NULL", deckID).
			Exec(req.Context())
		if err != nil {
			return err
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

	g.POST("/decks/:deckID/copy", func(w http.ResponseWriter, req bunrouter.Request) error {
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
		if err := database.NewSelect().Model(&src).Where("id = ? AND deleted_at IS NULL", sourceDeckID).Scan(req.Context()); err != nil {
			http.Error(w, "source deck not found", http.StatusNotFound)
			return nil
		}

		userDeck := &db.Deck{
			ModuleID:     src.ModuleID,
			Title:        src.Title,
			TitleJa:      src.TitleJa,
			DeckType:     db.DeckTypeUser,
			LearnerID:    body.LearnerID,
			SourceDeckID: &sourceDeckID,
			CreatedAt:    time.Now(),
		}
		if _, err := database.NewInsert().Model(userDeck).Returning("*").Exec(req.Context()); err != nil {
			return err
		}

		// shallow copy: duplicate deck_cards rows pointing to same card IDs
		if _, err := database.NewRaw(`
			INSERT INTO deck_cards (deck_id, card_id)
			SELECT ?, card_id FROM deck_cards WHERE deck_id = ?
		`, userDeck.ID, sourceDeckID).Exec(req.Context()); err != nil {
			return err
		}

		if err := loadDeckCards(req.Context(), database, userDeck); err != nil {
			return err
		}
		truncateDeck(userDeck)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		return json.NewEncoder(w).Encode(userDeck)
	})

	g.GET("/learners/:learnerID/decks", func(w http.ResponseWriter, req bunrouter.Request) error {
		learnerID := req.Param("learnerID")

		var decks []db.Deck
		if err := database.NewSelect().Model(&decks).
			Where("learner_id = ? AND deck_type = ? AND deleted_at IS NULL", learnerID, db.DeckTypeUser).
			OrderExpr("created_at DESC").
			Scan(req.Context()); err != nil {
			return err
		}

		for i := range decks {
			if err := loadDeckCards(req.Context(), database, &decks[i]); err != nil {
				return err
			}
			truncateDeck(&decks[i])
		}

		w.Header().Set("Content-Type", "application/json")
		return json.NewEncoder(w).Encode(decks)
	})

	g.PUT("/user-decks/:deckID/cards/:cardID", func(w http.ResponseWriter, req bunrouter.Request) error {
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
		if err := database.NewSelect().Model(&deck).Where("id = ? AND deck_type = ? AND deleted_at IS NULL", deckID, db.DeckTypeUser).Scan(req.Context()); err != nil {
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
		if err := database.NewSelect().Model(&existing).Where("id = ? AND deleted_at IS NULL", cardID).Scan(req.Context()); err != nil {
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
				return err
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
				return err
			}
			// swap junction row
			if _, err := database.NewUpdate().Model((*db.DeckCard)(nil)).
				Set("card_id = ?", newCard.ID).
				Where("deck_id = ? AND card_id = ?", deckID, cardID).
				Exec(req.Context()); err != nil {
				return err
			}
			newCardID = newCard.ID
		}

		var result db.Card
		if err := database.NewSelect().Model(&result).Where("id = ?", newCardID).Scan(req.Context()); err != nil {
			return err
		}
		prepareCard(&result)

		w.Header().Set("Content-Type", "application/json")
		return json.NewEncoder(w).Encode(result)
	})

	g.POST("/user-decks/:deckID/cards", func(w http.ResponseWriter, req bunrouter.Request) error {
		deckID, err := parseDeckID(req)
		if err != nil {
			http.Error(w, "invalid deckID", http.StatusBadRequest)
			return nil
		}

		var deck db.Deck
		if err := database.NewSelect().Model(&deck).Where("id = ? AND deck_type = ? AND deleted_at IS NULL", deckID, db.DeckTypeUser).Scan(req.Context()); err != nil {
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
			return err
		}
		if _, err := database.NewInsert().Model(&db.DeckCard{DeckID: deckID, CardID: card.ID}).Exec(req.Context()); err != nil {
			return err
		}

		prepareCard(card)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		return json.NewEncoder(w).Encode(card)
	})

	g.DELETE("/user-decks/:deckID/cards/:cardID", func(w http.ResponseWriter, req bunrouter.Request) error {
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

		// soft-delete card if owned by this user deck; otherwise just remove junction row
		now := time.Now()
		ur, err := database.NewUpdate().Model((*db.Card)(nil)).
			Set("deleted_at = ?", now).
			Where("id = ? AND deck_id = ? AND deleted_at IS NULL", cardID, deckID).
			Exec(req.Context())
		if err != nil {
			return err
		}
		affected, _ := ur.RowsAffected()
		if affected == 0 {
			// card owned by system deck — just remove junction row
			res, err := database.NewDelete().Model((*db.DeckCard)(nil)).
				Where("deck_id = ? AND card_id = ?", deckID, cardID).
				Exec(req.Context())
			if err != nil {
				return err
			}
			n, _ := res.RowsAffected()
			if n == 0 {
				http.Error(w, "card not in deck", http.StatusNotFound)
				return nil
			}
		}

		w.WriteHeader(http.StatusNoContent)
		return nil
	})

	g.DELETE("/user-decks/:deckID", func(w http.ResponseWriter, req bunrouter.Request) error {
		deckID, err := parseDeckID(req)
		if err != nil {
			http.Error(w, "invalid deckID", http.StatusBadRequest)
			return nil
		}

		now := time.Now()
		// soft-delete cards owned by this user deck (best-effort — deck delete proceeds regardless)
		if _, err := database.NewUpdate().Model((*db.Card)(nil)).
			Set("deleted_at = ?", now).
			Where("deck_id = ? AND deleted_at IS NULL", deckID).
			Exec(req.Context()); err != nil {
			log.Printf("WARN soft-delete cards for deck %d: %v", deckID, err)
		}

		res, err := database.NewUpdate().Model((*db.Deck)(nil)).
			Set("deleted_at = ?", now).
			Where("id = ? AND deleted_at IS NULL", deckID).
			Exec(req.Context())
		if err != nil {
			return err
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

	g.GET("/decks/:deckID/review", func(w http.ResponseWriter, req bunrouter.Request) error {
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

		var deck db.Deck
		if err := database.NewSelect().Model(&deck).
			Where("id = ? AND deleted_at IS NULL", deckID).
			Scan(req.Context()); err != nil {
			http.Error(w, "deck not found", http.StatusNotFound)
			return nil
		}

		var cards []*db.Card
		if err := database.NewSelect().Model(&cards).
			Join("JOIN deck_cards dc ON dc.card_id = card.id").
			Where("dc.deck_id = ? AND card.deleted_at IS NULL", deckID).
			Scan(req.Context()); err != nil {
			return err
		}

		now := time.Now()
		var dueCards []*db.Card
		for _, card := range cards {
			var srsCard db.SRSCard
			err := database.NewSelect().Model(&srsCard).
				Where("card_id = ? AND learner_id = ?", card.ID, learnerID).
				Scan(req.Context())
			if err != nil {
				prepareCard(card)
				dueCards = append(dueCards, card)
				continue
			}
			if !srsCard.Due.After(now) {
				prepareCard(card)
				dueCards = append(dueCards, card)
			}
		}

		if dueCards == nil {
			dueCards = []*db.Card{}
		}

		w.Header().Set("Content-Type", "application/json")
		return json.NewEncoder(w).Encode(struct {
			Deck  db.Deck    `json:"deck"`
			Cards []*db.Card `json:"cards"`
		}{Deck: deck, Cards: dueCards})
	})

	// --- feedback endpoints ---

	g.POST("/cards/:cardID/feedback", func(w http.ResponseWriter, req bunrouter.Request) error {
		cardID, err := parseID(req.Param("cardID"))
		if err != nil {
			http.Error(w, "invalid cardID", http.StatusBadRequest)
			return nil
		}

		var body struct {
			LearnerID   string `json:"learner_id"`
			Category    string `json:"category"`
			Description string `json:"description"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return nil
		}
		if body.LearnerID == "" {
			http.Error(w, "learner_id required", http.StatusBadRequest)
			return nil
		}
		if !db.ValidFeedbackCategories[body.Category] {
			http.Error(w, "invalid category", http.StatusBadRequest)
			return nil
		}

		feedback := &db.CardFeedback{
			CardID:      cardID,
			LearnerID:   body.LearnerID,
			Category:    body.Category,
			Description: body.Description,
			CreatedAt:   time.Now(),
		}
		if _, err := database.NewInsert().Model(feedback).Returning("*").Exec(req.Context()); err != nil {
			return err
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		return json.NewEncoder(w).Encode(feedback)
	})

	g.GET("/cards/:cardID/feedback", func(w http.ResponseWriter, req bunrouter.Request) error {
		cardID, err := parseID(req.Param("cardID"))
		if err != nil {
			http.Error(w, "invalid cardID", http.StatusBadRequest)
			return nil
		}

		var rows []db.CardFeedback
		if err := database.NewSelect().Model(&rows).
			Where("card_id = ?", cardID).
			OrderExpr("created_at DESC").
			Scan(req.Context()); err != nil {
			return err
		}

		w.Header().Set("Content-Type", "application/json")
		return json.NewEncoder(w).Encode(rows)
	})

	g.GET("/decks/:deckID/feedback", func(w http.ResponseWriter, req bunrouter.Request) error {
		deckID, err := parseDeckID(req)
		if err != nil {
			http.Error(w, "invalid deckID", http.StatusBadRequest)
			return nil
		}

		var rows []db.CardFeedback
		if err := database.NewSelect().Model(&rows).
			Join("JOIN cards c ON c.id = card_feedback.card_id").
			Where("c.deck_id = ?", deckID).
			OrderExpr("card_feedback.created_at DESC").
			Scan(req.Context()); err != nil {
			return err
		}

		w.Header().Set("Content-Type", "application/json")
		return json.NewEncoder(w).Encode(rows)
	})

	g.POST("/cards/:cardID/review", func(w http.ResponseWriter, req bunrouter.Request) error {
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
				return err
			}
		} else {
			if _, err := database.NewUpdate().Model(&srsCard).WherePK().Exec(req.Context()); err != nil {
				return err
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
		Where("dc.deck_id = ? AND card.deleted_at IS NULL", deck.ID).
		Scan(ctx)
	if err != nil {
		return err
	}
	deck.Cards = cards
	return nil
}

func truncateDeck(deck *db.Deck) {
	for _, c := range deck.Cards {
		prepareCard(c)
	}
}

func prepareCard(c *db.Card) {
	c.Distractors = truncate(c.Distractors, 3)
	c.DistractorsJa = truncate(c.DistractorsJa, 3)
	opts := make([]string, len(c.Distractors)+1)
	copy(opts, c.Distractors)
	opts[len(c.Distractors)] = c.CorrectAnswer
	c.Options = shuffled(opts)
	optsJa := make([]string, len(c.DistractorsJa)+1)
	copy(optsJa, c.DistractorsJa)
	optsJa[len(c.DistractorsJa)] = c.CorrectAnswerJa
	c.OptionsJa = shuffled(optsJa)
}

func shuffled(s []string) []string {
	out := make([]string, len(s))
	copy(out, s)
	rand.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
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
