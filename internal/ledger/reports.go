package ledger

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mattwalters/wand/internal/journal"
)

// VelocityBucket is one UTC calendar day's worth of tokens, across every
// phase-round any run closed that day — the "am I laying down tokens
// consistently?" curve.
type VelocityBucket struct {
	Day       time.Time
	TokensIn  *int64
	TokensOut *int64
}

// Velocity buckets tokens by UTC calendar day, oldest first. since filters
// out phase-rounds that closed before it; the zero time takes the whole
// history.
func Velocity(runs []RunSummary, since time.Time) []VelocityBucket {
	buckets := make(map[time.Time]*VelocityBucket)
	var days []time.Time
	for _, run := range runs {
		for _, g := range run.Rounds {
			if g.At.IsZero() || g.At.Before(since) {
				continue
			}
			day := g.At.UTC().Truncate(24 * time.Hour)
			b, ok := buckets[day]
			if !ok {
				b = &VelocityBucket{Day: day}
				buckets[day] = b
				days = append(days, day)
			}
			b.TokensIn = addTokens(b.TokensIn, g.TokensIn)
			b.TokensOut = addTokens(b.TokensOut, g.TokensOut)
		}
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Before(days[j]) })
	out := make([]VelocityBucket, len(days))
	for i, d := range days {
		out[i] = *buckets[d]
	}
	return out
}

// RenderVelocity prints the velocity table, oldest day first.
func RenderVelocity(buckets []VelocityBucket) string {
	if len(buckets) == 0 {
		return "no phase activity recorded.\n"
	}
	rows := make([][3]string, len(buckets))
	dayW, inW, outW := len("day"), len("tokens in"), len("tokens out")
	for i, bucket := range buckets {
		rows[i] = [3]string{bucket.Day.Format("2006-01-02"), tokenStr(bucket.TokensIn), tokenStr(bucket.TokensOut)}
		dayW = max(dayW, len(rows[i][0]))
		inW = max(inW, len(rows[i][1]))
		outW = max(outW, len(rows[i][2]))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%-*s  %*s  %*s\n", dayW, "day", inW, "tokens in", outW, "tokens out")
	for _, r := range rows {
		fmt.Fprintf(&b, "%-*s  %*s  %*s\n", dayW, r[0], inW, r[1], outW, r[2])
	}
	return b.String()
}

// phasePipelineOrder ranks phases for display, never by a metric — the
// not-list this ticket holds is that per-harness output must never look
// like a leaderboard. Run's own pipeline is implement, review, revise,
// fix-ci; plan's is scout, critic, revise, replan; the two are interleaved
// here so both read top-to-bottom in the order they actually occur. A phase
// name outside this set — a future phase this binary predates — sorts after
// every named one, alphabetically among themselves.
var phasePipelineOrder = []string{"scout", "critic", "implement", "review", "revise", "replan", "fix-ci"}

func phaseRank(phase string) int {
	for i, p := range phasePipelineOrder {
		if p == phase {
			return i
		}
	}
	return len(phasePipelineOrder)
}

// HarnessPhaseRow is one (harness, phase) cell: how many rounds it took
// across every run, and what they cost. Rounds counts distinct
// (phase, round) occurrences, not phase.ended records — a phase retried
// after a transient failure is still one round. For the phase named
// "review" (internal/run's review phase; internal/plan has no phase by that
// name), Rounds is also the review-round count.
type HarnessPhaseRow struct {
	Harness   string
	Phase     string
	Rounds    int
	TokensIn  *int64
	TokensOut *int64
	WallClock time.Duration
}

// HarnessPhase groups every run's phase-rounds by (harness, phase), summing
// tokens and wall-clock and counting distinct rounds. Sorted by the phase's
// place in its pipeline, then harness name alphabetically — never by a
// "best" metric, so this table cannot be read as a leaderboard.
func HarnessPhase(runs []RunSummary) []HarnessPhaseRow {
	type key struct{ Harness, Phase string }
	rows := make(map[key]*HarnessPhaseRow)
	var keys []key
	for _, run := range runs {
		for _, g := range run.Rounds {
			k := key{run.Harness, g.Phase}
			row, ok := rows[k]
			if !ok {
				row = &HarnessPhaseRow{Harness: run.Harness, Phase: g.Phase}
				rows[k] = row
				keys = append(keys, k)
			}
			row.Rounds++
			row.TokensIn = addTokens(row.TokensIn, g.TokensIn)
			row.TokensOut = addTokens(row.TokensOut, g.TokensOut)
			row.WallClock += g.WallClock
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if ra, rb := phaseRank(a.Phase), phaseRank(b.Phase); ra != rb {
			return ra < rb
		}
		if a.Phase != b.Phase {
			return a.Phase < b.Phase
		}
		return a.Harness < b.Harness
	})
	out := make([]HarnessPhaseRow, len(keys))
	for i, k := range keys {
		out[i] = *rows[k]
	}
	return out
}

// RenderHarnessPhase prints the per-harness x per-phase table.
func RenderHarnessPhase(rows []HarnessPhaseRow) string {
	if len(rows) == 0 {
		return "no phase activity recorded.\n"
	}
	type strRow [5]string
	strRows := make([]strRow, len(rows))
	harnessW, phaseW, roundsW := len("harness"), len("phase"), len("rounds")
	inW, outW := len("tokens in"), len("tokens out")
	for i, r := range rows {
		strRows[i] = strRow{r.Harness, r.Phase, strconv.Itoa(r.Rounds), tokenStr(r.TokensIn), tokenStr(r.TokensOut)}
		harnessW = max(harnessW, len(strRows[i][0]))
		phaseW = max(phaseW, len(strRows[i][1]))
		roundsW = max(roundsW, len(strRows[i][2]))
		inW = max(inW, len(strRows[i][3]))
		outW = max(outW, len(strRows[i][4]))
	}
	wallW := len("wall clock")
	walls := make([]string, len(rows))
	for i, r := range rows {
		walls[i] = r.WallClock.Round(time.Second).String()
		wallW = max(wallW, len(walls[i]))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%-*s  %-*s  %*s  %*s  %*s  %*s\n",
		harnessW, "harness", phaseW, "phase", roundsW, "rounds", inW, "tokens in", outW, "tokens out", wallW, "wall clock")
	for i, r := range strRows {
		fmt.Fprintf(&b, "%-*s  %-*s  %*s  %*s  %*s  %*s\n",
			harnessW, r[0], phaseW, r[1], roundsW, r[2], inW, r[3], outW, r[4], wallW, walls[i])
	}
	return b.String()
}

// ConvergenceCell is how many of a (harness, verb) pair's runs converged.
type ConvergenceCell struct {
	Converged int
	Total     int
}

// ConvergenceRow is one harness's convergence rate, broken out by verb —
// kept separate from [HarnessPhaseRow] because convergence is a whole-run
// outcome, and crediting it to one phase would double-count or pick a
// phase's harness arbitrarily.
type ConvergenceRow struct {
	Harness string
	ByVerb  map[string]ConvergenceCell
}

// Convergence groups every ended run by (harness, verb) and counts how many
// converged. A run still in progress has no verdict yet and is excluded.
// Rows sort by harness name alphabetically — no ranking, no highlighting:
// this is the operator's own telemetry, not a leaderboard.
func Convergence(runs []RunSummary) []ConvergenceRow {
	rows := make(map[string]*ConvergenceRow)
	var harnesses []string
	for _, run := range runs {
		if run.Outcome == "" {
			continue
		}
		row, ok := rows[run.Harness]
		if !ok {
			row = &ConvergenceRow{Harness: run.Harness, ByVerb: make(map[string]ConvergenceCell)}
			rows[run.Harness] = row
			harnesses = append(harnesses, run.Harness)
		}
		cell := row.ByVerb[run.Verb]
		cell.Total++
		if run.Outcome == journal.Converged {
			cell.Converged++
		}
		row.ByVerb[run.Verb] = cell
	}
	sort.Strings(harnesses)
	out := make([]ConvergenceRow, len(harnesses))
	for i, h := range harnesses {
		out[i] = *rows[h]
	}
	return out
}

// convergenceVerbs is the fixed column order for [RenderConvergence]: the
// two verbs this binary drives. A journal carrying an older run's "scope"
// (see internal/journal.Meta's own doc) reads as its own column rather than
// being folded into plan, since this package does not migrate what it reads.
var convergenceVerbs = []string{"run", "plan"}

// RenderConvergence prints the per-harness x per-verb convergence table.
func RenderConvergence(rows []ConvergenceRow) string {
	if len(rows) == 0 {
		return "no ended runs recorded.\n"
	}
	verbs := append([]string(nil), convergenceVerbs...)
	for _, r := range rows {
		for v := range r.ByVerb {
			if !contains(verbs, v) {
				verbs = append(verbs, v)
			}
		}
	}

	harnessW := len("harness")
	cellW := make([]int, len(verbs))
	for i, v := range verbs {
		cellW[i] = len(v)
	}
	cells := make([][]string, len(rows))
	for i, r := range rows {
		harnessW = max(harnessW, len(r.Harness))
		cells[i] = make([]string, len(verbs))
		for j, v := range verbs {
			cells[i][j] = cellStr(r.ByVerb[v])
			cellW[j] = max(cellW[j], len(cells[i][j]))
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%-*s", harnessW, "harness")
	for j, v := range verbs {
		fmt.Fprintf(&b, "  %*s", cellW[j], v)
	}
	b.WriteString("\n")
	for i, r := range rows {
		fmt.Fprintf(&b, "%-*s", harnessW, r.Harness)
		for j := range verbs {
			fmt.Fprintf(&b, "  %*s", cellW[j], cells[i][j])
		}
		b.WriteString("\n")
	}
	return b.String()
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func cellStr(c ConvergenceCell) string {
	if c.Total == 0 {
		return "—"
	}
	pct := 100 * float64(c.Converged) / float64(c.Total)
	return fmt.Sprintf("%d/%d (%.0f%%)", c.Converged, c.Total, pct)
}

// TicketRun is one run recorded against a ticket, for [TicketSummary]'s
// listing of every run's outcome.
type TicketRun struct {
	RunID   string
	Verb    string
	Outcome journal.Outcome
}

// TicketSummary is one ticket's totals across every run recorded against
// it — a ticket re-planned or re-run keeps every attempt, summed.
type TicketSummary struct {
	Ticket    string
	Runs      []TicketRun
	TokensIn  *int64
	TokensOut *int64
	WallClock time.Duration
}

// Tickets groups every run by its ticket, summing tokens and wall-clock and
// listing each run's outcome. Sorted by ticket identifier, numerically by
// issue number so WND-9 sorts before WND-10.
func Tickets(runs []RunSummary) []TicketSummary {
	rows := make(map[string]*TicketSummary)
	var tickets []string
	for _, run := range runs {
		row, ok := rows[run.Ticket]
		if !ok {
			row = &TicketSummary{Ticket: run.Ticket}
			rows[run.Ticket] = row
			tickets = append(tickets, run.Ticket)
		}
		row.Runs = append(row.Runs, TicketRun{RunID: run.ID, Verb: run.Verb, Outcome: run.Outcome})
		for _, g := range run.Rounds {
			row.TokensIn = addTokens(row.TokensIn, g.TokensIn)
			row.TokensOut = addTokens(row.TokensOut, g.TokensOut)
			row.WallClock += g.WallClock
		}
	}
	sort.Slice(tickets, func(i, j int) bool { return ticketLess(tickets[i], tickets[j]) })
	out := make([]TicketSummary, len(tickets))
	for i, t := range tickets {
		out[i] = *rows[t]
	}
	return out
}

// ticketLess compares ticket identifiers numerically by issue number
// ("WND-9" before "WND-10"), falling back to a plain string compare for
// anything not of that shape — the same rule internal/queue.Less uses for
// issue identifiers, duplicated here rather than imported since this
// package has no other reason to depend on internal/queue.
func ticketLess(a, b string) bool {
	ta, na, oka := splitTicket(a)
	tb, nb, okb := splitTicket(b)
	if oka && okb && ta == tb {
		return na < nb
	}
	return a < b
}

func splitTicket(id string) (team string, number int, ok bool) {
	i := strings.LastIndex(id, "-")
	if i < 0 {
		return "", 0, false
	}
	n, err := strconv.Atoi(id[i+1:])
	if err != nil {
		return "", 0, false
	}
	return id[:i], n, true
}

// RenderTickets prints the per-ticket totals table, one run's outcome list
// per ticket.
func RenderTickets(rows []TicketSummary) string {
	if len(rows) == 0 {
		return "no runs recorded.\n"
	}
	ticketW, inW, outW, wallW := len("ticket"), len("tokens in"), len("tokens out"), len("wall clock")
	type strRow struct {
		ticket, in, out, wall, runs string
	}
	strRows := make([]strRow, len(rows))
	for i, r := range rows {
		strRows[i] = strRow{
			ticket: r.Ticket,
			in:     tokenStr(r.TokensIn),
			out:    tokenStr(r.TokensOut),
			wall:   r.WallClock.Round(time.Second).String(),
			runs:   runsList(r.Runs),
		}
		ticketW = max(ticketW, len(strRows[i].ticket))
		inW = max(inW, len(strRows[i].in))
		outW = max(outW, len(strRows[i].out))
		wallW = max(wallW, len(strRows[i].wall))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%-*s  %*s  %*s  %*s  runs\n", ticketW, "ticket", inW, "tokens in", outW, "tokens out", wallW, "wall clock")
	for _, r := range strRows {
		fmt.Fprintf(&b, "%-*s  %*s  %*s  %*s  %s\n", ticketW, r.ticket, inW, r.in, outW, r.out, wallW, r.wall, r.runs)
	}
	return b.String()
}

func runsList(runs []TicketRun) string {
	parts := make([]string, len(runs))
	for i, r := range runs {
		outcome := string(r.Outcome)
		if outcome == "" {
			outcome = "in progress"
		}
		parts[i] = fmt.Sprintf("%s(%s): %s", r.Verb, r.RunID, outcome)
	}
	return strings.Join(parts, ", ")
}

func tokenStr(v *int64) string {
	if v == nil {
		return "—"
	}
	return strconv.FormatInt(*v, 10)
}
