package run

import (
	"strings"
	"testing"

	"github.com/mattwalters/wand/internal/linear"
)

func TestTitleFor(t *testing.T) {
	cases := []struct {
		name, suggested, ticketTitle, want string
	}{
		{"worker suggestion wins", "add the loop", "the loop ticket", "[WND-10] add the loop"},
		{"ticket title as fallback", "", "the loop ticket", "[WND-10] the loop ticket"},
		{"already prefixed is left alone", "[WND-10] add the loop", "", "[WND-10] add the loop"},
		{"case-insensitive prefix counts", "[wnd-10] add the loop", "", "[wnd-10] add the loop"},
		{"wrong identifier is prefixed anew", "[WND-9] add the loop", "", "[WND-10] [WND-9] add the loop"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := TitleFor("WND-10", tc.suggested, tc.ticketTitle); got != tc.want {
				t.Errorf("TitleFor = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPRBodyGlossesTheTicket(t *testing.T) {
	issue := linear.Issue{Identifier: "WND-10", Title: "the loop", URL: "https://linear.app/x/WND-10"}
	body := PRBody(issue, "did the thing", []string{"skipped the optional stage"})

	// The reference is glossed — identifier plus title plus link — never a
	// bare "WND-10" a reader must go resolve (the PW-189 convention).
	if !strings.Contains(body, "[WND-10 — the loop](https://linear.app/x/WND-10)") {
		t.Errorf("body lacks the glossed reference:\n%s", body)
	}
	if !strings.Contains(body, "did the thing") {
		t.Errorf("body lacks the summary:\n%s", body)
	}
	if !strings.Contains(body, "skipped the optional stage") {
		t.Errorf("body lacks the deviations:\n%s", body)
	}
}

func TestTicketGlossWithoutURL(t *testing.T) {
	got := TicketGloss(linear.Issue{Identifier: "WND-2", Title: "t"})
	if got != "WND-2 — t" {
		t.Errorf("gloss %q", got)
	}
}
