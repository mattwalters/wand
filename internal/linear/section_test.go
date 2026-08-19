package linear

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- reading -----------------------------------------------------------------

func TestReadSectionAbsent(t *testing.T) {
	_, ok, err := ReadSection("Just a human's prose.\n\nTwo paragraphs of it.", "plan")
	if err != nil {
		t.Fatalf("readSection: %v", err)
	}
	if ok {
		t.Error("a description with no markers should read as absent")
	}
}

func TestReadSectionReturnsTrimmedBody(t *testing.T) {
	description := "Intro.\n\n<!-- wand:plan -->\n\n## Plan\n\ndo the thing\n\n<!-- /wand:plan -->\n\nOutro."
	body, ok, err := ReadSection(description, "plan")
	if err != nil {
		t.Fatalf("readSection: %v", err)
	}
	if !ok {
		t.Fatal("section should be present")
	}
	if body != "## Plan\n\ndo the thing" {
		t.Errorf("body %q, want the trimmed region contents", body)
	}
}

// A ticket that DOCUMENTS the convention is not parsed as carrying a region:
// markers inside CommonMark code fences are illustrations, not fences.
func TestMarkersInsideCodeFenceAreIllustrations(t *testing.T) {
	description := "The convention looks like this:\n\n" +
		"```markdown\n<!-- wand:plan -->\n## Plan\n<!-- /wand:plan -->\n```\n\n" +
		"That is all."

	_, ok, err := ReadSection(description, "plan")
	if err != nil {
		t.Fatalf("readSection: %v", err)
	}
	if ok {
		t.Fatal("markers inside a code fence must not count as a region")
	}

	// And a write must append a real region below, not replace the example.
	next, err := WithSection(description, "plan", "actual plan")
	if err != nil {
		t.Fatalf("withSection: %v", err)
	}
	if !strings.Contains(next, "```markdown\n<!-- wand:plan -->\n## Plan\n<!-- /wand:plan -->\n```") {
		t.Error("the human's fenced example was altered")
	}
	body, ok, err := ReadSection(next, "plan")
	if err != nil || !ok || body != "actual plan" {
		t.Errorf("appended region reads back as (%q, %v, %v), want (\"actual plan\", true, nil)", body, ok, err)
	}
}

// A marker quoted inline, mid-sentence, is not a marker: live markers are
// alone on their line.
func TestMarkerMustBeAloneOnItsLine(t *testing.T) {
	description := "Regions are fenced by <!-- wand:plan --> and <!-- /wand:plan --> markers."
	_, ok, err := ReadSection(description, "plan")
	if err != nil {
		t.Fatalf("readSection: %v", err)
	}
	if ok {
		t.Error("an inline mention of the markers must not count as a region")
	}
}

// The marker is compared whole, so "wand:plan" does not match inside
// "<!-- wand:plan-b -->".
func TestNeighbouringIDsDoNotCrossMatch(t *testing.T) {
	description := "<!-- wand:plan-b -->\nplan b\n<!-- /wand:plan-b -->"
	_, ok, err := ReadSection(description, "plan")
	if err != nil {
		t.Fatalf("readSection: %v", err)
	}
	if ok {
		t.Error("\"plan\" must not match inside a \"plan-b\" region")
	}
	body, ok, err := ReadSection(description, "plan-b")
	if err != nil || !ok || body != "plan b" {
		t.Errorf("plan-b reads as (%q, %v, %v), want (\"plan b\", true, nil)", body, ok, err)
	}
}

// --- refusals: ambiguity is a hard error, never a guess ----------------------

func TestMalformedMarkersRefuse(t *testing.T) {
	cases := map[string]string{
		"open with no close": "<!-- wand:plan -->\nthe rest of a human's description",
		"close with no open": "a stray line\n<!-- /wand:plan -->",
		"close before open":  "<!-- /wand:plan -->\nbetween\n<!-- wand:plan -->",
		"duplicate regions":  "<!-- wand:plan -->\na\n<!-- /wand:plan -->\n\n<!-- wand:plan -->\nb\n<!-- /wand:plan -->",
	}
	for name, description := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := ReadSection(description, "plan"); err == nil {
				t.Error("readSection should refuse rather than guess")
			}
			if _, err := WithSection(description, "plan", "new"); err == nil {
				t.Error("withSection should refuse rather than destroy text")
			}
		})
	}
}

func TestSectionIDValidation(t *testing.T) {
	for _, id := range []string{"", "Plan", "plan_b", "a--b", "-plan", "plan-", "has space"} {
		if _, _, err := ReadSection("", id); err == nil {
			t.Errorf("id %q should be rejected", id)
		}
	}
	for _, id := range []string{"plan", "plan-b", "abandon-note", "v2"} {
		if _, _, err := ReadSection("", id); err != nil {
			t.Errorf("id %q should be usable: %v", id, err)
		}
	}
}

// Content carrying a marker of its own would produce a description this code
// could never parse again; it is refused while the text is still recoverable.
func TestContentCarryingAMarkerRefuses(t *testing.T) {
	for _, body := range []string{
		"sneaky\n<!-- wand:plan -->",
		"sneaky\n<!-- /wand:plan -->",
	} {
		if _, err := WithSection("", "plan", body); err == nil {
			t.Error("content containing a live marker must be refused")
		}
	}
}

// Content can break a region without carrying a marker: an unbalanced code
// fence puts the closing marker inside a code block. The compose re-reads
// its own output and refuses rather than write a region nothing can find.
func TestUnbalancedFenceInContentRefuses(t *testing.T) {
	if _, err := WithSection("", "plan", "```go\nfunc main() {}"); err == nil {
		t.Error("content with an unclosed fence must be refused")
	}
}

// The same class of failure from the other side: a description whose last
// fence was never closed would swallow an appended region whole.
func TestUnclosedFenceAboveRegionRefuses(t *testing.T) {
	if _, err := WithSection("Setup:\n\n```bash\nmake install", "plan", "the plan"); err == nil {
		t.Error("appending below an unclosed fence must be refused")
	}
}

// A closing fence must be the same character, at least as long, and carry no
// info string — anything else is still inside the block, per CommonMark.
func TestFenceClosingRules(t *testing.T) {
	cases := map[string]struct {
		description string
		wantLive    bool
	}{
		"tilde does not close backtick": {
			description: "```\n~~~\n<!-- wand:plan -->\nx\n<!-- /wand:plan -->",
			wantLive:    false,
		},
		"shorter fence does not close longer": {
			description: "````\n```\n<!-- wand:plan -->\nx\n<!-- /wand:plan -->",
			wantLive:    false,
		},
		"info string means opening, not closing": {
			description: "```\n``` go\n<!-- wand:plan -->\nx\n<!-- /wand:plan -->",
			wantLive:    false,
		},
		"longer fence closes shorter": {
			description: "```\n````\n<!-- wand:plan -->\nx\n<!-- /wand:plan -->",
			wantLive:    true,
		},
		"four-space indent is not a fence": {
			description: "    ```\n<!-- wand:plan -->\nx\n<!-- /wand:plan -->",
			wantLive:    true,
		},
		"three-space indent is a fence": {
			description: "   ```\n<!-- wand:plan -->\nx\n<!-- /wand:plan -->",
			wantLive:    false,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, ok, err := ReadSection(tc.description, "plan")
			if err != nil {
				t.Fatalf("readSection: %v", err)
			}
			if ok != tc.wantLive {
				t.Errorf("markers live = %v, want %v", ok, tc.wantLive)
			}
		})
	}
}

// --- writing -----------------------------------------------------------------

func TestWithSectionAppends(t *testing.T) {
	next, err := WithSection("", "plan", "the plan")
	if err != nil {
		t.Fatalf("withSection: %v", err)
	}
	if next != "<!-- wand:plan -->\nthe plan\n<!-- /wand:plan -->" {
		t.Errorf("empty description should become just the region, got %q", next)
	}

	next, err = WithSection("A human's prose.", "plan", "the plan")
	if err != nil {
		t.Fatalf("withSection: %v", err)
	}
	if next != "A human's prose.\n\n<!-- wand:plan -->\nthe plan\n<!-- /wand:plan -->" {
		t.Errorf("region should append below the prose with a blank line, got %q", next)
	}
}

func TestWithSectionReplacesInPlace(t *testing.T) {
	description := "Before, exactly.\n\n<!-- wand:plan -->\nold plan\n<!-- /wand:plan -->\n\nAfter, exactly."
	next, err := WithSection(description, "plan", "new plan")
	if err != nil {
		t.Fatalf("withSection: %v", err)
	}
	if next != "Before, exactly.\n\n<!-- wand:plan -->\nnew plan\n<!-- /wand:plan -->\n\nAfter, exactly." {
		t.Errorf("every byte outside the region must survive, got %q", next)
	}
}

// The same markdown written twice produces a byte-identical description, so
// a routine that runs again does not stack a second copy.
func TestWithSectionIsIdempotent(t *testing.T) {
	once, err := WithSection("Prose above.", "plan", "  the plan\n")
	if err != nil {
		t.Fatalf("first write: %v", err)
	}
	twice, err := WithSection(once, "plan", "the plan")
	if err != nil {
		t.Fatalf("second write: %v", err)
	}
	if twice != once {
		t.Errorf("re-run not byte-identical:\n once: %q\ntwice: %q", once, twice)
	}
}

// --- the network write -------------------------------------------------------

// A write that would change nothing is skipped entirely: Linear records a
// description revision per update, and a converged loop must not fill the
// history a human reads with entries that changed no text.
func TestUpsertSectionSkipsNetworkWhenUnchanged(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`{"data":{"issueUpdate":{"success":true}}}`))
	}))
	defer srv.Close()

	c := &Client{APIKey: "k", Endpoint: srv.URL}
	description := "Prose.\n\n<!-- wand:plan -->\nthe plan\n<!-- /wand:plan -->"
	next, changed, err := c.UpsertSection(context.Background(), "issue-1", description, "plan", "the plan")
	if err != nil {
		t.Fatalf("upsertSection: %v", err)
	}
	if changed {
		t.Error("nothing changed; changed should be false")
	}
	if next != description {
		t.Errorf("description %q, want it untouched", next)
	}
	if calls != 0 {
		t.Errorf("%d network calls, want none", calls)
	}
}

func TestUpsertSectionWritesWhenChanged(t *testing.T) {
	var sent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Variables struct {
				Input struct {
					Description string `json:"description"`
				} `json:"input"`
			} `json:"variables"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		sent = req.Variables.Input.Description
		w.Write([]byte(`{"data":{"issueUpdate":{"success":true}}}`))
	}))
	defer srv.Close()

	c := &Client{APIKey: "k", Endpoint: srv.URL}
	next, changed, err := c.UpsertSection(context.Background(), "issue-1", "Prose.", "plan", "the plan")
	if err != nil {
		t.Fatalf("upsertSection: %v", err)
	}
	if !changed {
		t.Error("a real write should report changed")
	}
	if sent != next || sent != "Prose.\n\n<!-- wand:plan -->\nthe plan\n<!-- /wand:plan -->" {
		t.Errorf("sent %q, want the composed description", sent)
	}
}

func TestUpdateDescriptionSurfacesRefusal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":{"issueUpdate":{"success":false}}}`))
	}))
	defer srv.Close()

	c := &Client{APIKey: "k", Endpoint: srv.URL}
	if err := c.UpdateDescription(context.Background(), "issue-1", "body"); err == nil {
		t.Error("success:false must surface as an error")
	}
}
