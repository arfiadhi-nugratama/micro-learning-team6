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
	CardType           string   `json:"card_type"`
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

const schemaPrompt = `
Return a JSON object with a single "cards" key containing an array. Each item must have ALL of these fields:
{
  "card_type": "multiple_choice" | "self_assess" | "open_text",
  "question": "question in English",
  "correct_answer": "correct answer in English",
  "distractors": [
    "7–10 wrong answers in English for multiple_choice cards",
    "empty array [] for self_assess and open_text"
  ],
  "question_ja": "question in Japanese",
  "correct_answer_ja": "correct answer in Japanese",
  "distractors_ja": [
    "wrong answers in Japanese matching the same order and count as distractors",
    "empty array [] for self_assess and open_text"
  ],
  "source_concept_id": "Concept-ID from the source block",
  "source_concept_title": "Concept-Title-EN from the source block"
}

Card types:
- "multiple_choice": provide 7–10 plausible but clearly wrong distractors. Distractors must be mutually exclusive, similar in length/format/style to the correct answer, and not overlap in meaning with each other or the answer.
- "self_assess": learner reflects then self-rates. Set distractors and distractors_ja to [].
- "open_text": learner types a short answer, graded by AI. Set distractors and distractors_ja to [].
`

const systemPrompt = `
You are an expert instructional designer creating flashcards from provided learning content.

## Task
Generate high-quality flashcards based ONLY on the provided content. Do not add outside information. If content is unclear or missing context, make the best possible cards from what is provided.

## Card Types
Choose the best card type for each card:

- "multiple_choice": Best for factual recall, definitions, and selecting the correct option from plausible alternatives.
- "self_assess": Best for open-ended concepts, explanations, processes, or anything where the learner benefits from reflecting before seeing the answer.
- "open_text": Best for fill-in-the-blank, short factual answers (names, numbers, terms) where the exact answer can be verified.

Aim for a natural mix: roughly 40% multiple_choice, 30% self_assess, 30% open_text — but let the content guide you.

## Card Rules
- Each card tests ONE clear idea.
- The "question" is a clear, unambiguous prompt or question.
- The "correct_answer" is a short, accurate response drawn directly from the content.
- Prioritize key vocabulary, main ideas, steps, examples, and common misconceptions.
- Focus on the main learning objectives and critical details of the activity.
- Every question must be unique — no duplicate or near-duplicate questions across the entire set.
- Generate 1–3 cards per concept, as many as the content genuinely warrants.
- Do not ask questions about the course itself, the platform, or the user experience. Focus only on the learning content.

## Language & Tone
- Provide every field in both English and Japanese.
- Japanese distractors must match the English distractors in the SAME order.
- Use the MS1 tone of voice: supportive, encouraging, clear, plain-language, and second-person.
- Use MS1 terminology exactly as it appears in the source content.

## Traceability
- "source_concept_id" must be the Concept-ID value from the source concept block the card is based on.
- "source_concept_title" must be the Concept-Title-EN value from that same block.
`

const Prompt = systemPrompt

func newOpenAIClient() openai.Client {
	opts := []option.RequestOption{option.WithAPIKey(os.Getenv("OPENAI_API_KEY"))}
	if baseURL := os.Getenv("OPENAI_BASE_URL"); baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	return openai.NewClient(opts...)
}

func Generate(ctx context.Context, systemPrompt, content string) ([]CardData, error) {
	client := newOpenAIClient()

	fullPrompt := systemPrompt + "\n" + schemaPrompt

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

type JudgeResult struct {
	Correct  bool   `json:"correct"`
	Feedback string `json:"feedback"`
}

const judgePrompt = `You are grading a learner's open-text answer to a flashcard question. Given the question, the correct answer, and the learner's answer, decide if the learner's response is correct.

Be lenient with minor spelling mistakes, different phrasing, and partial answers that demonstrate clear understanding. Be strict about factually wrong content.

Return a JSON object with:
{
  "correct": true or false,
  "feedback": "one short sentence explaining why it's correct or what was missing/wrong"
}
`

func JudgeOpenText(ctx context.Context, question, correctAnswer, questionJa, correctAnswerJa, learnerAnswer string) (JudgeResult, error) {
	client := newOpenAIClient()

	input := fmt.Sprintf("Question (EN): %s\nQuestion (JA): %s\nCorrect answer (EN): %s\nCorrect answer (JA): %s\nLearner's answer: %s",
		question, questionJa, correctAnswer, correctAnswerJa, learnerAnswer)

	resp, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model: "gpt-4.1-mini",
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(judgePrompt),
			openai.UserMessage(input),
		},
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONObject: &shared.ResponseFormatJSONObjectParam{Type: "json_object"},
		},
	})
	if err != nil {
		return JudgeResult{}, fmt.Errorf("openai judge request: %w", err)
	}
	if len(resp.Choices) == 0 {
		return JudgeResult{}, fmt.Errorf("no choices in judge response")
	}

	var result JudgeResult
	if err := json.Unmarshal([]byte(resp.Choices[0].Message.Content), &result); err != nil {
		return JudgeResult{}, fmt.Errorf("parse judge response: %w", err)
	}
	return result, nil
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
