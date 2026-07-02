package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/shared"
)

type CardData struct {
	Question           string   `json:"question"`
	CorrectAnswer      string   `json:"correct_answer"`
	Distractors        []string `json:"distractors"`
	QuestionJa         string   `json:"question_ja"`
	CorrectAnswerJa    string   `json:"correct_answer_ja"`
	DistractorsJa      []string `json:"distractors_ja"`
	SourceConceptID    string   `json:"source_concept_id"`
	SourceConceptTitle string   `json:"source_concept_title"`
}

const deduplicatePrompt = `You are given a JSON object with a "cards" array, each item having an "i" (index) and "q" (question). Identify duplicate or near-duplicate questions (same concept tested in essentially the same way). Keep the best version when duplicates exist. Return a JSON object with a single "keep" key containing an array of the indices to keep.`

const formatInstruction = `Return a JSON object with a single "cards" key containing an array. Each item in the array must have ALL of these fields:
{
  "question": "question in English",
  "correct_answer": "correct answer in English",
  "distractors": ["at least 7 wrong answers in English"],
  "question_ja": "question in Japanese",
  "correct_answer_ja": "correct answer in Japanese",
  "distractors_ja": ["same distractors in Japanese, matching order"],
  "source_concept_id": "the Concept-ID value from the source concept block this card is based on",
  "source_concept_title": "the Concept-Title-EN value from the source concept block this card is based on"
}
Each card must have at least 7 distractors in both languages. Generate as many cards as the content warrants. Each question must be unique — do not generate duplicate or near-duplicate questions across the entire card set.`

const systemPrompt = `Instructions:
- Each flashcard should test one clear idea.
- Write the front as a question or prompt.
- Write the back as a short, accurate answer.
- Prioritize key vocabulary, main ideas, steps, examples, and common misconceptions.
- Do not add outside information.
- If the content is unclear or missing context, make the best possible flashcards from what is provided.
- It should have multiple possible answers both correct and incorrect, then show a set of 10 incorrect 1 correct
- Avoid ambiguous questions
- Keep explanations concise, clear, and easy to memorize.
- Focus on the main learning objectives and any critical details mentioned in the activity.
- Use the MS1 tone of the voice
- Each question should be unique and not repeated across the entire card set.
- Use MS1 terminology`

const Prompt = systemPrompt + "\n" + formatInstruction

func newOpenAIClient() openai.Client {
	opts := []option.RequestOption{option.WithAPIKey(os.Getenv("OPENAI_API_KEY"))}
	if baseURL := os.Getenv("OPENAI_BASE_URL"); baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	return openai.NewClient(opts...)
}

func Generate(ctx context.Context, systemPrompt, content string) ([]CardData, error) {
	client := newOpenAIClient()

	fullPrompt := systemPrompt + "\n" + formatInstruction

	resp, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model: "gpt-4.1-mini",
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(fullPrompt),
			openai.UserMessage(content),
		},
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONObject: &shared.ResponseFormatJSONObjectParam{Type: "json_object"},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("openai request: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}

	var envelope struct {
		Cards []CardData `json:"cards"`
	}
	if err := json.Unmarshal([]byte(resp.Choices[0].Message.Content), &envelope); err != nil {
		return nil, fmt.Errorf("parse cards json: %w", err)
	}

	return envelope.Cards, nil
}

// Deduplicate calls the LLM with only the questions (+ index) and filters locally.
func Deduplicate(ctx context.Context, cards []CardData) ([]CardData, error) {
	type stub struct {
		I int    `json:"i"`
		Q string `json:"q"`
	}
	stubs := make([]stub, len(cards))
	for i, c := range cards {
		stubs[i] = stub{I: i, Q: c.Question}
	}
	raw, err := json.Marshal(struct {
		Cards []stub `json:"cards"`
	}{Cards: stubs})
	if err != nil {
		return nil, fmt.Errorf("marshal cards for dedup: %w", err)
	}

	client := newOpenAIClient()
	t := time.Now()
	slog.InfoContext(ctx, "openai deduplicate start", "input_card_count", len(cards))
	resp, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model: "gpt-4.1-mini",
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(deduplicatePrompt),
			openai.UserMessage(string(raw)),
		},
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONObject: &shared.ResponseFormatJSONObjectParam{Type: "json_object"},
		},
	})
	slog.InfoContext(ctx, "openai deduplicate done", "duration_ms", time.Since(t).Milliseconds())
	if err != nil {
		return nil, fmt.Errorf("openai dedup request: %w", err)
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in dedup response")
	}

	var envelope struct {
		Keep []int `json:"keep"`
	}
	if err := json.Unmarshal([]byte(resp.Choices[0].Message.Content), &envelope); err != nil {
		return nil, fmt.Errorf("parse dedup response json: %w", err)
	}

	result := make([]CardData, 0, len(envelope.Keep))
	for _, i := range envelope.Keep {
		if i >= 0 && i < len(cards) {
			result = append(result, cards[i])
		}
	}
	return result, nil
}
