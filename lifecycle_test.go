package main

// This test pins README.md's "## The lifecycle" Mermaid diagram to the two
// packages that actually own the topology it draws, so a status added to
// covenant.Default or a destination added to (or removed from) the guard's
// forbidden set fails make check here instead of the diagram silently going
// stale — the same drift class the diagram itself exists to stop being
// prose about.

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/mattwalters/wand/internal/covenant"
	"github.com/mattwalters/wand/internal/guard"
)

var (
	mermaidFenceRe = regexp.MustCompile("(?s)```mermaid\n(.*?)```")
	stateAliasRe   = regexp.MustCompile(`(?m)^\s*state\s+"([^"]+)"\s+as\s+(\S+)\s*$`)
	transitionRe   = regexp.MustCompile(`(?m)^\s*(\S+)\s*-->\s*(\S+)\s*:\s*(.+?)\s*$`)
)

func TestReadmeLifecycleDiagramMatchesCovenantAndGuard(t *testing.T) {
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}

	fence := mermaidFenceRe.FindSubmatch(readme)
	if fence == nil {
		t.Fatal(`README.md has no "` + "```mermaid" + `" fence for the lifecycle diagram`)
	}
	diagram := string(fence[1])

	// A `state "Display Name" as ID` line renames a multi-word state; any
	// id with no such line is already its own display name (Triage,
	// Backlog, Todo, Done, Canceled, Duplicate all read fine as-is).
	displayName := map[string]string{}
	for _, m := range stateAliasRe.FindAllStringSubmatch(diagram, -1) {
		displayName[m[2]] = m[1]
	}
	resolve := func(id string) string {
		if n, ok := displayName[id]; ok {
			return n
		}
		return id
	}

	type edge struct{ from, to, label string }
	var edges []edge
	drawn := map[string]bool{}
	for _, m := range transitionRe.FindAllStringSubmatch(diagram, -1) {
		from, to, label := m[1], m[2], m[3]
		if from == "[*]" || to == "[*]" {
			continue
		}
		from, to = resolve(from), resolve(to)
		drawn[from] = true
		drawn[to] = true
		edges = append(edges, edge{from, to, label})
	}
	if len(edges) == 0 {
		t.Fatal("no state transitions parsed out of the lifecycle diagram")
	}

	var got []string
	for name := range drawn {
		got = append(got, name)
	}
	sort.Strings(got)

	var want []string
	for _, s := range covenant.Default().Statuses {
		want = append(want, s.Name)
	}
	sort.Strings(want)

	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("lifecycle diagram states do not match covenant.Default().Statuses:\n got  %v\n want %v", got, want)
	}

	// Every arrow into a status the guard forbids an agent from setting
	// must be drawn as a human arrow — the diagram's whole claim about who
	// may move a ticket where, checked against the one verdict function
	// wand's own write paths call.
	for _, e := range edges {
		if _, blocked := guard.CheckState(e.to); blocked && !strings.HasPrefix(e.label, "human") {
			t.Errorf("%s --> %s: guard forbids an agent from setting %q, but the diagram labels this arrow %q, not human",
				e.from, e.to, e.to, e.label)
		}
	}
}
