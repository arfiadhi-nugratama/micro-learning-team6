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
	Question      string   `json:"question"`
	CorrectAnswer string   `json:"correct_answer"`
	Distractors   []string `json:"distractors"`
}

const formatInstruction = `Return ONLY a JSON array with no markdown. Each item: {"question": "...", "correct_answer": "...", "distractors": ["wrong1", ..., "wrong7"]}. Each card must have at least 7 distractors. Generate as many cards as the content warrants.`

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
		Model: openai.ChatModelGPT4oMini,
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
