package cli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestReadBriefMissingShortPathReadsAsMistypedPath pins the genuine bad-path
// case: a short nonexistent argument still reads as a mistyped path, not
// brief-like prose, and the echoed argument appears exactly once.
func TestReadBriefMissingShortPathReadsAsMistypedPath(t *testing.T) {
	cmd := &cobra.Command{}
	_, err := readBrief(cmd, []string{"no-such-file.md"})
	if err == nil {
		t.Fatal("expected an error for a nonexistent path")
	}
	msg := err.Error()
	if strings.Contains(msg, "brief text") {
		t.Errorf("err = %q, want a mistyped-path message, not brief-like language", msg)
	}
	if got := strings.Count(msg, "no-such-file.md"); got != 1 {
		t.Errorf("echoed argument appeared %d times in %q, want exactly 1", got, msg)
	}
}

// TestReadBriefMultilineArgumentReadsAsBriefLike pins the brief-like
// detection: a newline in the argument can never be a real single-argument
// path, so it should produce the plain "looks like brief text" message.
func TestReadBriefMultilineArgumentReadsAsBriefLike(t *testing.T) {
	cmd := &cobra.Command{}
	arg := "I want to be able to refer to\nteams without the prefix"
	_, err := readBrief(cmd, []string{arg})
	if err == nil {
		t.Fatal("expected an error for a nonexistent, newline-containing argument")
	}
	msg := err.Error()
	if !strings.Contains(msg, "brief text") {
		t.Errorf("err = %q, want brief-like language", msg)
	}
	if strings.Contains(msg, "\n") {
		t.Errorf("err = %q, want a single clean sentence with no embedded newline", msg)
	}
}

// TestReadBriefLongSingleLineArgumentReadsAsBriefLike pins the length half
// of the heuristic: a single-line argument past briefLikeLength — no
// newline, but far longer than any real filename — should still read as
// brief-like, not as a mistyped path.
func TestReadBriefLongSingleLineArgumentReadsAsBriefLike(t *testing.T) {
	cmd := &cobra.Command{}
	arg := strings.Repeat("brief prose that is not a path ", 10)
	if len(arg) <= briefLikeLength {
		t.Fatalf("test fixture too short: %d bytes, want > %d", len(arg), briefLikeLength)
	}
	_, err := readBrief(cmd, []string{arg})
	if err == nil {
		t.Fatal("expected an error for a nonexistent, long argument")
	}
	msg := err.Error()
	if !strings.Contains(msg, "brief text") {
		t.Errorf("err = %q, want brief-like language", msg)
	}
	if strings.Count(msg, arg) != 0 {
		t.Errorf("err = %q, want the full untruncated argument not echoed verbatim", msg)
	}
}

// TestReadBriefEchoIsTruncated covers both the brief-like and not-found
// paths: an oversized argument's echo in the error message must be
// truncated, and truncation plus the "at most once" guarantee together rule
// out the double-echo this ticket exists to fix.
func TestReadBriefEchoIsTruncated(t *testing.T) {
	longName := strings.Repeat("x", briefLikeLength+50) + ".md"

	t.Run("brief-like", func(t *testing.T) {
		cmd := &cobra.Command{}
		_, err := readBrief(cmd, []string{longName})
		if err == nil {
			t.Fatal("expected an error")
		}
		msg := err.Error()
		if len(msg) >= len(longName) {
			t.Errorf("message length %d not shorter than raw argument length %d: echo was not truncated", len(msg), len(longName))
		}
		if strings.Count(msg, longName) != 0 {
			t.Errorf("err = %q, want the full argument not echoed verbatim", msg)
		}
	})
}
