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

const formatInstruction = `
Return a JSON object with a single "cards" key containing an array. Each item in the array must have ALL of these fields:
{
  "question": "question in English",
  "correct_answer": "correct answer in English",
  "distractors": ["exactly 10 wrong answers in English"],
  "question_ja": "question in Japanese",
  "correct_answer_ja": "correct answer in Japanese",
  "distractors_ja": ["exactly 10 wrong answers in Japanese, matching order"],
  "source_concept_id": "Concept-ID from the source block",
  "source_concept_title": "Concept-Title-EN from the source block"
}
`

const systemPrompt = `
You are an expert instructional designer creating multiple-choice flashcards from provided learning content.

## Task
Generate high-quality MCQ flashcards based ONLY on the provided content. Do not add outside information. If content is unclear or missing context, make the best possible cards from what is provided.

## Card Rules
- Each card tests ONE clear idea.
- The "question" is a clear, unambiguous prompt or question.
- The "correct_answer" is a short, accurate response drawn directly from the content.
- Prioritize key vocabulary, main ideas, steps, examples, and common misconceptions.
- Focus on the main learning objectives and critical details of the activity.
- Every question must be unique — no duplicate or near-duplicate questions across the entire set.
- Generate 1–3 cards per concept, as many as the content genuinely warrants.
- Do not ask questions about the course itself, the platform, or the user experience. Focus only on the learning content.

## Distractor Rules
- Each card must have EXACTLY 10 distractors (10 incorrect + 1 correct = 11 total options).
- Distractors must be plausible but clearly incorrect.
- Distractors must be similar in length, format, and style to the correct answer.
- Distractors must be mutually exclusive — no distractor may also be a correct answer.
- Distractors must not overlap in meaning with each other or the correct answer.

## Language & Tone
- Provide every field in both English and Japanese.
- Japanese distractors must match the English distractors in the SAME order.
- Use the MS1 tone of voice: supportive, encouraging, clear, plain-language, and second-person.
- Use MS1 terminology exactly as it appears in the source content.

## Traceability
- "source_concept_id" must be the Concept-ID value from the source concept block the card is based on.
- "source_concept_title" must be the Concept-Title-EN value from that same block.
`

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
