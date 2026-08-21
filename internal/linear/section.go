package linear

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// Marker-fenced sections in the description.
//
// The description is the human's artifact, so a machine gets a fenced region
// of it and nothing else. An agent that rewrites a description wholesale
// silently deletes whatever a person had written there, and Linear surfaces
// description history poorly enough that the loss would not be noticed until
// someone went looking for a paragraph that is no longer anywhere. So: one
// marked region with a machine author, and every byte outside it belongs to
// whoever wrote it. The region is replaced in place, never merged and never
// guessed at.
//
//	<!-- wand:plan -->
//	## Implementation Plan
//	…
//	<!-- /wand:plan -->
//
// What the fence governs is what a machine writes REPEATEDLY — the plan a
// plan run regenerates on every run, whose previous copy nobody wants kept. A
// one-off write (a description correction) uses an anchored patch instead:
// the two are the same caution against different risks — a repeated write
// needs a region it owns outright, a one-off write needs an anchor that
// fails rather than clobbers.
//
// The marker id is decoupled from the headings inside it on purpose.
// "## Implementation Plan" is what a person reads and may rename freely;
// "wand:plan" is what code matches on. Tying the two together would mean a
// human editing a heading silently orphans the region, and the next write
// appends a second copy below it.

// sectionIDPattern constrains ids so a marker is a plain string match rather
// than a pattern. Everything here works by matching the whole marker
// literally, so an id carrying "-->" or a newline would produce a fence that
// cannot be found again — written once, unreadable thereafter, and appended
// to on every subsequent run.
var sectionIDPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// markers is the open/close pair for a section id.
//
// The trailing " -->" is part of what gets matched, which is what keeps
// neighbouring ids apart: "wand:plan" does not match inside
// "<!-- wand:plan-b -->", because the marker is compared whole.
type markers struct {
	open  string
	close string
}

func sectionMarkers(id string) (markers, error) {
	if !sectionIDPattern.MatchString(id) {
		return markers{}, fmt.Errorf(
			"linear: %q is not a usable section id; use lowercase words joined by hyphens, e.g. %q", id, "plan")
	}
	return markers{
		open:  "<!-- wand:" + id + " -->",
		close: "<!-- /wand:" + id + " -->",
	}, nil
}

// codeFence matches a fenced code block opening or closing, per CommonMark:
// up to three spaces of indent, then three or more backticks or tildes.
var codeFence = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})(.*)$")

// liveMarkerOffsets finds every place marker appears as a LIVE marker — not
// as text a human quoted.
//
// Two rules, and both exist because a description is documentation as often
// as it is data — the first tickets this code is pointed at describe this
// very convention, the only way prose can: by quoting the markers.
//
//   - Inside a fenced code block, a marker is an illustration. A markdown
//     block showing what a region looks like is not a region, and a raw
//     substring search cannot tell the two apart — it resolves both ends
//     inside the fence, finds exactly one of each so no refusal fires, and
//     the next write replaces the human's example with the machine's text.
//     Silent deletion of a person's words is the one failure this whole file
//     exists to prevent, so the fence state is tracked and anything inside
//     one is skipped.
//   - A marker must be alone on its line. That is how every marker this code
//     writes is emitted, and it costs nothing to require. It rules out the
//     other way a description quotes one — inline, in backticks,
//     mid-sentence — without needing to parse code spans at all.
//
// An unclosed fence runs to the end of the text, which is what CommonMark
// says it does; a region hidden behind one therefore reads as absent.
// WithSection catches that case by checking its own output rather than
// letting it through.
func liveMarkerOffsets(text, marker string) []int {
	var offsets []int
	var fenceChar byte
	var fenceLen int
	inFence := false
	offset := 0

	for _, line := range strings.Split(text, "\n") {
		m := codeFence.FindStringSubmatch(line)
		switch {
		case inFence:
			// A closing fence is the same character, at least as long, and
			// carries no info string. Anything else is still content inside
			// the block.
			if m != nil && m[1][0] == fenceChar && len(m[1]) >= fenceLen && strings.TrimSpace(m[2]) == "" {
				inFence = false
			}
		case m != nil:
			inFence = true
			fenceChar = m[1][0]
			fenceLen = len(m[1])
		case strings.TrimSpace(line) == marker:
			offsets = append(offsets, offset+strings.Index(line, marker))
		}
		offset += len(line) + 1
	}

	return offsets
}

// section is where a section sits in a description.
type section struct {
	before string // every byte up to the opening marker
	body   string // between the markers, untrimmed
	after  string // every byte past the closing marker
}

// locateSection finds a section in a description, or returns nil if it has
// none.
//
// Anything malformed errors instead of resolving to a best guess. Each case
// is a place where guessing would destroy text:
//
//   - An open with no close. A greedy read would take the rest of the
//     description as the region, and the next write would then replace all
//     of it — a human's paragraphs deleted by a routine that thought it
//     owned them.
//   - A close with no open. The same corruption seen from the other end;
//     there is no region here, only a stray line.
//   - Two regions with the same id. That is corruption, not a merge problem:
//     nothing in the text says which copy is current, so picking one
//     silently discards the other.
//
// A human is the only one who can repair any of these, and the error is how
// they get told rather than finding out later.
func locateSection(description, id string) (*section, error) {
	m, err := sectionMarkers(id)
	if err != nil {
		return nil, err
	}

	opens := liveMarkerOffsets(description, m.open)
	closes := liveMarkerOffsets(description, m.close)

	switch {
	case len(opens) == 0 && len(closes) == 0:
		return nil, nil
	case len(opens) == 0:
		return nil, fmt.Errorf(
			"linear: description has a closing %q with no opening marker; repairing that is a human's call — refusing rather than guess where the region starts", m.close)
	case len(closes) == 0:
		return nil, fmt.Errorf(
			"linear: description has an opening %q with no closing marker; refusing rather than treat the rest of the description as the region", m.open)
	case len(opens) > 1 || len(closes) > 1:
		return nil, fmt.Errorf(
			"linear: description has more than one %q region; that is corruption, not a merge — nothing says which copy is current, so refusing to pick one", "wand:"+id)
	}

	openAt, closeAt := opens[0], closes[0]
	if closeAt < openAt {
		return nil, fmt.Errorf(
			"linear: description has %q before %q; the markers are out of order", m.close, m.open)
	}

	return &section{
		before: description[:openAt],
		body:   description[openAt+len(m.open) : closeAt],
		after:  description[closeAt+len(m.close):],
	}, nil
}

// ReadSection returns the trimmed contents of a fenced section, and whether
// the description carries one. Malformed markers are an error, never a
// guess.
//
// It takes the description string rather than an issue: a caller has one in
// hand already from its own read, and there is nothing else on the issue
// this needs.
func ReadSection(description, id string) (string, bool, error) {
	found, err := locateSection(description, id)
	if err != nil {
		return "", false, err
	}
	if found == nil {
		return "", false, nil
	}
	return strings.TrimSpace(found.body), true, nil
}

// WithSection returns a description with one section written into it, as a
// pure string operation.
//
// It replaces exactly what is between the markers when they are present, and
// appends the whole fenced region at the end when they are not. Both
// branches leave every byte outside the region untouched, which is what
// makes a re-run idempotent: the same markdown written twice produces a
// byte-identical description, so a routine that runs again does not stack a
// second copy.
func WithSection(description, id, markdown string) (string, error) {
	m, err := sectionMarkers(id)
	if err != nil {
		return "", err
	}
	body := strings.TrimSpace(markdown)

	// Writing a marker INTO a region makes the fence unreadable on the next
	// pass, which is the one failure this whole design exists to prevent.
	// Refuse at the point where the text is still recoverable.
	if strings.Contains(body, m.open) || strings.Contains(body, m.close) {
		return "", fmt.Errorf(
			"linear: section content contains a %q marker of its own; that would produce a description this code could never parse again", "wand:"+id)
	}

	region := m.open + "\n" + body + "\n" + m.close
	found, err := locateSection(description, id)
	if err != nil {
		return "", err
	}

	var next string
	switch existing := strings.TrimSpace(description); {
	case found != nil:
		next = found.before + region + found.after
	case existing != "":
		next = existing + "\n\n" + region
	default:
		next = region
	}

	// Content can break a region without carrying a marker: an unbalanced
	// ``` in the body — or in a description whose last fence was never
	// closed — puts the markers inside a code block, where the next read
	// cannot see them. Left through, that region is invisible and a second
	// copy is appended below it on every subsequent run. Checking the output
	// is cheap and catches the whole class, whatever produced it.
	if got, ok, err := ReadSection(next, id); err != nil || !ok || got != body {
		return "", fmt.Errorf(
			"linear: writing the %q section would produce a description this code could not read back — most likely an unclosed code fence in the content or above the region; refusing rather than write a region nothing can find again", "wand:"+id)
	}

	return next, nil
}

// UpdateDescription replaces an issue's whole description.
//
// The blunt primitive, exported because UpsertSection is built on it and a
// caller that has genuinely composed a whole body should not have to reach
// for a private helper. Prefer UpsertSection — a wholesale write is the
// thing the fencing above exists to avoid.
func (c *Client) UpdateDescription(ctx context.Context, issueID, markdown string) error {
	return c.UpdateIssue(ctx, issueID, IssueUpdate{Description: &markdown})
}

// UpsertSection writes one fenced section onto an issue, idempotently.
//
// It takes the current description rather than re-reading it here, which
// would race the caller's own copy.
//
// A write that would change nothing is skipped, and that is not just a saved
// round trip: Linear records a description revision per update, so a loop
// that re-runs a converged phase would otherwise fill the history a human
// reads with entries that changed no text.
func (c *Client) UpsertSection(ctx context.Context, issueID, description, id, markdown string) (next string, changed bool, err error) {
	next, err = WithSection(description, id, markdown)
	if err != nil {
		return "", false, err
	}
	if next == description {
		return next, false, nil
	}
	if err := c.UpdateDescription(ctx, issueID, next); err != nil {
		return "", false, err
	}
	return next, true, nil
}
