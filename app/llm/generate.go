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
You are an expert instructional designer creating multiple-choice flashcards from provided learning content.
Your goal is to generate high-quality MCQ flashcards that test transferable subject knowledge only.
Use ONLY the provided content. Do not add outside information. If the content is unclear, incomplete, or mostly course-specific, create only the cards that can be supported by clear subject-matter content.
---
## Task
Generate multiple-choice flashcards based only on the provided content.
Each flashcard must test a real concept, definition, fact, skill, example, rule, or common misconception that remains meaningful outside the course.
Do **NOT** generate questions that require the learner to know the course structure, course project, final project, curriculum, stated objectives, planned outcomes, or what the course says it will teach.
---
## Content Filtering
Apply this filtering **BEFORE** generating any cards.
Ignore and exclude all content related to:
- Course objectives, learning goals, purposes, outcomes, or promises.
- Statements such as “you will learn,” “you will build,” “you will practice,” or “by the end of this course.”
- Curriculum descriptions, course scope, course coverage, focus areas, or module summaries.
- Course project descriptions, final project descriptions, capstone project goals, or project requirements.
- Any question that can only be answered by reading the course project objective, final project brief, curriculum outline, lesson plan, or module description.
- Course-specific tasks, assignments, milestones, deliverables, rubrics, or grading criteria.
- Procedural setup steps, such as “click here,” “open the menu,” or “go to Step X.”
- Step numbers or ordering of course actions.
- System or technical requirements, such as software versions, hardware specifications, installation prerequisites, supported browsers, file formats, or environment details.
- Setup, configuration, onboarding, or platform-specific instructions.
- Course-meta information, such as module names, lesson order, progress markers, section titles, or navigation labels.
- Platform UI elements or user experience details.
If a concept block contains only excluded material, skip it entirely and generate no card from it.
---
## What to Test
Generate cards only from content that contains self-contained subject knowledge, such as:
- Definitions.
- Key vocabulary.
- Concepts.
- Rules.
- Principles.
- Examples.
- Cause-and-effect relationships.
- Comparisons.
- Common mistakes or misconceptions.
- Practical knowledge that applies outside the course.
A valid question should be answerable by someone who understands the subject, even if they have never seen this course, project, curriculum, or lesson.
---
## What NOT to Test
Never generate questions about:
- What the course teaches.
- What the learner will do in the course.
- What the project requires.
- What the final project asks learners to build.
- What topics are covered in a module.
- What skills learners will develop.
- Why the course project exists.
- How the curriculum is organized.
- Which step comes next.
- Which tool, menu, page, platform, or button is used in the course.
- Any course-specific scenario unless it teaches a transferable subject concept.
---
## Card Rules
Each card must follow these rules:
- Test exactly **ONE** clear idea.
- Stand on its own without requiring course context.
- Be answerable from subject-matter knowledge, not course-specific knowledge.
- Use a clear, direct question.
- Avoid mentioning “this course,” “this module,” “this lesson,” “the project,” “the final project,” “the curriculum,” or the course name.
- The correct answer must be short, accurate, and directly supported by the source content.
- Prioritize important concepts over minor details.
- Avoid duplicate or near-duplicate questions.
- Generate 1–3 cards per valid concept, only if the content genuinely supports them.
- Skip weak or course-specific content rather than forcing a card.
---
## Disallowed Question Types
Never generate questions like these:
- “What will you learn in this module?”
- “What is the purpose of this course project?”
- “Which skill will you develop in the final project?”
- “What does this course cover?”
- “What is the goal of the capstone project?”
- “Which topic is introduced in Lesson 2?”
- “What are the learning objectives of this section?”
- “What will you build by the end of the course?”
- “Which assignment helps you practice data analysis?”
- “What is required for the final project submission?”
- “Which step comes after installing the toolkit?”
- “Where do you click to start the module?”
---
## Allowed Question Types
Generate questions like these:
- “What symbol is used to write a single-line comment in Python?”
- “What does the len() function return when given a list?”
- “Which data type stores a value of True or False?”
- “What is the result of 7 // 2 in Python?”
- “What does a primary key uniquely identify in a database table?”
- “Which term describes data organized in rows and columns?”
---
## Distractor Rules
Each card must include exactly:
- 1 correct answer.
- 10 distractors.
- 11 total answer options.
Distractors must be:
- Plausible but clearly incorrect.
- Similar in length, format, and style to the correct answer.
- Mutually exclusive.
- Not overlapping in meaning with the correct answer.
- Not partially correct.
- Not vague or ambiguous.
- Written in the same type of language as the correct answer.
---
## Language and Tone
Provide every field in both English and Japanese.
Japanese distractors must match the English distractors in the same order.
Use the MS1 tone of voice:
- Supportive.
- Encouraging.
- Clear.
- Plain-language.
- Second-person when appropriate.
Use MS1 terminology exactly as it appears in the source content.
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
		Model: "gpt-5.5",
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
	Correct         bool   `json:"correct"`
	Feedback        string `json:"feedback,omitempty"`
	CorrectAnswer   string `json:"correct_answer,omitempty"`
	CorrectAnswerJa string `json:"correct_answer_ja,omitempty"`
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
		Model: "gpt-5.4-mini",
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
		Model: "gpt-5.4-mini",
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
