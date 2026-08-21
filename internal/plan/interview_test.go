package plan_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mattwalters/wand/internal/covenant"
	"github.com/mattwalters/wand/internal/plan"
)

// draftWithAssumptions is the fixture the interview tests grill: three
// assumptions, deliberately listed cheapest-first so the ordering under
// test cannot pass by accident.
func draftWithAssumptions(t *testing.T) plan.Draft {
	t.Helper()
	d := goodDraft()
	d["assumptions"] = []any{
		map[string]any{"statement": "cheap thing", "if_wrong": "a rename", "cost": "low"},
		map[string]any{"statement": "middling thing", "if_wrong": "an extra test", "cost": "medium"},
		map[string]any{"statement": "expensive thing", "if_wrong": "the whole approach changes", "cost": "high"},
	}
	parsed, err := plan.ParseDraft(raw(t, d), covenant.Default())
	if err != nil {
		t.Fatalf("ParseDraft: %v", err)
	}
	return parsed
}

// Blast radius is the ordering, and it is the whole design: a human who
// stops answering half way through must have answered the half that
// mattered. The problem first, then the recommendation, then what the plan
// rests on costliest-first, then the leftovers.
func TestQuestionsAreOrderedByBlastRadius(t *testing.T) {
	qs := plan.Questions(draftWithAssumptions(t))

	var topics []string
	for _, q := range qs {
		topics = append(topics, q.Topic)
	}
	want := []string{
		plan.TopicTicket,
		plan.TopicRecommendation,
		plan.TopicAssumption, plan.TopicAssumption, plan.TopicAssumption,
		plan.TopicOpenQuestion,
	}
	if strings.Join(topics, "|") != strings.Join(want, "|") {
		t.Fatalf("topics = %v\nwant %v", topics, want)
	}

	assumptions := []string{qs[2].Quote, qs[3].Quote, qs[4].Quote}
	wantOrder := []string{"expensive thing", "middling thing", "cheap thing"}
	for i := range wantOrder {
		if assumptions[i] != wantOrder[i] {
			t.Errorf("assumption %d = %q, want %q — the costliest is asked first", i+1, assumptions[i], wantOrder[i])
		}
	}
}

// A question about a claim the draft does not make is unanswerable, so
// every question carries the draft's own words rather than a paraphrase.
func TestEveryQuestionQuotesTheDraft(t *testing.T) {
	d := draftWithAssumptions(t)
	for i, q := range plan.Questions(d) {
		if strings.TrimSpace(q.Quote) == "" {
			t.Errorf("question %d quotes nothing", i+1)
		}
		if strings.TrimSpace(q.Ask) == "" {
			t.Errorf("question %d asks nothing", i+1)
		}
	}
	qs := plan.Questions(d)
	if qs[0].Quote != d.Understanding {
		t.Errorf("the first question quotes %q, not the draft's reading of the ticket", qs[0].Quote)
	}
	if !strings.Contains(qs[1].Quote, d.Recommendation.Approach) || !strings.Contains(qs[1].Quote, d.Recommendation.Why) {
		t.Errorf("the recommendation question does not quote the recommendation: %q", qs[1].Quote)
	}
	// The rejected approaches ride the ask: "do you want it done that way"
	// is only answerable next to what it was chosen over.
	if !strings.Contains(qs[1].Ask, "Filter in Build") {
		t.Errorf("the recommendation question does not say what was passed over: %q", qs[1].Ask)
	}
}

func TestInterviewCollectsAnswers(t *testing.T) {
	d := draftWithAssumptions(t)
	qs := plan.Questions(d)

	// Answer the first over two lines, skip the second, answer the third,
	// then stop reading. A blank line ends an answer; an empty answer means
	// "as drafted" and is not recorded.
	in := strings.NewReader("no, it is about the queue\nnot the vet pass\n\n\nyes, that holds\n\n")
	var out bytes.Buffer
	answers, err := plan.Interview(in, &out, qs)
	if err != nil {
		t.Fatalf("Interview: %v", err)
	}
	if len(answers) != 2 {
		t.Fatalf("recorded %d answers, want 2 (the skipped one is not an answer)", len(answers))
	}
	if answers[0].Text != "no, it is about the queue\nnot the vet pass" {
		t.Errorf("first answer = %q; a multi-line answer must survive whole", answers[0].Text)
	}
	if answers[0].Question.Topic != plan.TopicTicket || answers[1].Question.Topic != plan.TopicAssumption {
		t.Errorf("answers landed on the wrong questions: %q, %q", answers[0].Question.Topic, answers[1].Question.Topic)
	}
	// Every question the human could still answer is put to them, quoting
	// the draft where they can see it.
	if !strings.Contains(out.String(), d.Understanding) {
		t.Error("the interview did not show the draft's reading of the ticket")
	}
}

// A human who walks away mid-interview has still answered something. The
// run keeps what they said and stops asking rather than reading a closed
// pipe as a wall of empty answers.
func TestInterviewStopsAtTheEndOfInput(t *testing.T) {
	qs := plan.Questions(draftWithAssumptions(t))
	answers, err := plan.Interview(strings.NewReader("the premise is wrong"), &bytes.Buffer{}, qs)
	if err != nil {
		t.Fatalf("Interview: %v", err)
	}
	if len(answers) != 1 || answers[0].Text != "the premise is wrong" {
		t.Fatalf("answers = %+v, want the one thing that was said", answers)
	}
}

func TestTranscriptKeepsTheHumansWordsApartFromTheDrafts(t *testing.T) {
	qs := plan.Questions(draftWithAssumptions(t))
	answers, err := plan.Interview(strings.NewReader("solve the other problem\n\n"), &bytes.Buffer{}, qs)
	if err != nil {
		t.Fatalf("Interview: %v", err)
	}
	got := plan.Transcript(answers)
	if !strings.Contains(got, "> solve the other problem") {
		t.Errorf("the transcript does not quote the human: %q", got)
	}
	if !strings.Contains(got, plan.TopicTicket) {
		t.Errorf("the transcript does not say what the answer was about: %q", got)
	}
}
