package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
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

const formatInstruction = `Return ONLY a JSON array with no markdown. Each item must have ALL of these fields:
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
Each flashcard should test one clear idea.
Write the front as a question or prompt.
Write the back as a short, accurate answer.
Prioritize key vocabulary, main ideas, steps, examples, and common misconceptions.
Do not add outside information.
If the content is unclear or missing context, make the best possible flashcards from what is provided.
It should have multiple possible answers both correct and incorrect, then show a set of 10 incorrect 1 correct
Avoid ambiguous questions
Keep explanations concise, clear, and easy to memorize.
Focus on the main learning objectives and any critical details mentioned in the activity.
Use the MS1 tone of the voice`

const Prompt = systemPrompt + "\n" + formatInstruction

func Generate(ctx context.Context, systemPrompt, content string) ([]CardData, error) {
	opts := []option.RequestOption{option.WithAPIKey(os.Getenv("OPENAI_API_KEY"))}
	if baseURL := os.Getenv("OPENAI_BASE_URL"); baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	client := openai.NewClient(opts...)

	fullPrompt := systemPrompt + "\n" + formatInstruction

	resp, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model: "gpt-5.4",
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(fullPrompt),
			openai.UserMessage(content),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("openai request: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}

	var cards []CardData
	if err := json.Unmarshal([]byte(resp.Choices[0].Message.Content), &cards); err != nil {
		return nil, fmt.Errorf("parse cards json: %w", err)
	}

	return cards, nil
}
