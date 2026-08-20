package scope

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// The draft-then-grill interview.
//
// A scope is a decision made once and paid for over the whole life of the
// work, so the cheapest moment to correct it is before it is written down.
// The interview is that moment, and its shape is deliberate:
//
//   - **Composed in code, not by a model.** The questions fall out of the
//     draft's own structure, so they are the same questions every time and
//     a test can hold every one of them. A model asked to interview a
//     human asks about what it finds interesting, which is not the same as
//     what is expensive to get wrong.
//
//   - **Ordered by blast radius.** The ticket itself first — the most
//     expensive thing to get wrong is the problem, and everything below is
//     wasted if the problem is. Then the recommendation, then the
//     assumptions costliest-first, then the open questions. A human who
//     stops answering half way has answered the half that mattered.
//
//   - **Every question quotes the draft verbatim.** "Does this assumption
//     hold?" is unanswerable without the assumption in front of you, and a
//     paraphrase asks about a claim the draft does not make.
//
//   - **A second, fresh session revises.** The interview's answers do not
//     go back to the session that wrote the draft: a session that has just
//     argued for A defends A, and the revision that comes back is the same
//     plan with the objections explained away. The reviser is cold and
//     sees the draft as somebody else's.

// Topics name what a question is about, worst blast radius first. They are
// printed to the human and to the reviser, so the reviser knows which part
// of its input each answer bears on.
const (
	TopicTicket         = "the ticket itself"
	TopicRecommendation = "the recommendation"
	TopicAssumption     = "an assumption"
	TopicOpenQuestion   = "an open question"
)

// Question is one thing to put to the human: what it is about, the draft's
// own words, and the ask.
type Question struct {
	Topic string
	// Quote is the draft's text, verbatim. It is printed as a blockquote
	// and passed to the reviser unchanged.
	Quote string
	Ask   string
}

// Answer pairs a question with what the human said.
type Answer struct {
	Question Question
	Text     string
}

// Questions composes the interview from a draft, in blast-radius order.
func Questions(d Draft) []Question {
	qs := []Question{{
		Topic: TopicTicket,
		Quote: d.Understanding,
		Ask: "Is that the problem you want solved? If the ticket means something else, " +
			"say what — everything below is scoped to this reading.",
	}}

	var rejected []string
	for _, a := range d.Approaches {
		if !strings.EqualFold(a.Name, d.Recommendation.Approach) {
			rejected = append(rejected, fmt.Sprintf("%s (trade-off: %s)", strings.TrimSpace(a.Name), strings.TrimSpace(a.Tradeoff)))
		}
	}
	ask := "Do you want it done that way?"
	if len(rejected) > 0 {
		ask += " The approaches it passed over: " + strings.Join(rejected, "; ") + "."
	}
	qs = append(qs, Question{
		Topic: TopicRecommendation,
		Quote: fmt.Sprintf("%s — %s", strings.TrimSpace(d.Recommendation.Approach), strings.TrimSpace(d.Recommendation.Why)),
		Ask:   ask,
	})

	for _, a := range SortedAssumptions(d) {
		qs = append(qs, Question{
			Topic: TopicAssumption,
			Quote: strings.TrimSpace(a.Statement),
			Ask: fmt.Sprintf("Does that hold? If it does not: %s (%s cost).",
				strings.TrimSpace(a.IfWrong), a.Cost),
		})
	}

	for _, q := range d.OpenQuestions {
		qs = append(qs, Question{
			Topic: TopicOpenQuestion,
			Quote: strings.TrimSpace(q),
			Ask:   "Do you know the answer, or should the implementer find out?",
		})
	}
	return qs
}

// Interview puts the questions to a human and collects the answers.
//
// An answer runs until a blank line, so a considered paragraph is as easy
// to give as a word. A question answered with nothing is skipped rather
// than recorded as an empty answer: silence means "as drafted", and
// handing the reviser a list of blanks would invite it to read them as
// objections. End of input ends the interview — the questions still
// unasked would be shouting into a closed pipe.
//
// It returns only the answered questions, which is also the count the
// ticket's provenance footer reports.
func Interview(in io.Reader, out io.Writer, qs []Question) ([]Answer, error) {
	r := bufio.NewReader(in)
	fmt.Fprintf(out, "\n%d question(s) about the draft, worst-consequence first. "+
		"Answer over as many lines as you like; a blank line ends an answer, "+
		"and an empty answer means \"as drafted\".\n", len(qs))

	var answers []Answer
	for i, q := range qs {
		fmt.Fprintf(out, "\n[%d/%d] %s\n\n%s\n\n%s\n> ", i+1, len(qs), q.Topic, blockquote(q.Quote), q.Ask)
		text, err := readAnswer(r)
		if err != nil && err != io.EOF {
			return answers, fmt.Errorf("reading your answer: %w", err)
		}
		if text != "" {
			answers = append(answers, Answer{Question: q, Text: text})
		}
		if err == io.EOF {
			fmt.Fprintln(out, "\n(input ended; the remaining questions go unasked)")
			break
		}
	}
	fmt.Fprintf(out, "\n%d answer(s).\n", len(answers))
	return answers, nil
}

// readAnswer reads until a blank line or the end of input.
func readAnswer(r *bufio.Reader) (string, error) {
	var lines []string
	for {
		line, err := r.ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		if strings.TrimSpace(line) == "" && err == nil {
			break
		}
		if line != "" {
			lines = append(lines, line)
		}
		if err != nil {
			return strings.TrimSpace(strings.Join(lines, "\n")), err
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n")), nil
}

// Transcript renders the answered questions for the reviser: the draft's
// own words, then the human's, kept apart so the reviser can tell which is
// which and treat the human's as the authority.
func Transcript(answers []Answer) string {
	var b strings.Builder
	for i, a := range answers {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "About %s, the draft says:\n\n%s\n\nAsked: %s\n\nThe human answered:\n\n%s\n",
			a.Question.Topic, blockquote(a.Question.Quote), a.Question.Ask, blockquote(a.Text))
	}
	return b.String()
}
