package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/mattwalters/wand/internal/cockpit"
	"github.com/mattwalters/wand/internal/covenant"
	"github.com/mattwalters/wand/internal/dispatch"
	"github.com/mattwalters/wand/internal/journal"
	"github.com/mattwalters/wand/internal/screen"
	"github.com/mattwalters/wand/internal/tui"
)

// sampleNotice says what the built-in board is, on screen, every time it is
// drawn — so nobody mistakes the fixture for their own team, and so the
// reason the keys refuse to write is visible before they press one.
const sampleNotice = "sample board — reads no Linear team, and writes none"

func newUICmd() *cobra.Command {
	var (
		teamKey    string
		script     string
		dumpScreen bool
		sample     bool
		width      int
		height     int
		harness    string
		model      string
		effort     string
		interval   time.Duration
	)

	cmd := &cobra.Command{
		Use:   "ui",
		Short: "Open the cockpit: everything waiting on a human",
		Long: "Open wand's cockpit — one screen answering one question: what is\n" +
			"waiting on me? Triage to judge, Needs Input to answer, ready-for-human\n" +
			"work to look at, and lanes no process is driving any more.\n\n" +
			"Blessing lives here. Promotion to Todo and to Scoping is the transition\n" +
			"the guard refuses everywhere else, because it hands out authorization an\n" +
			"agent does not have; this screen is the one place a person grants it.\n\n" +
			"With --dump-screen, a built-in sample board is rendered to plain text and\n" +
			"printed, which needs no terminal and no API key. Combined with --script\n" +
			"this makes any screen reachable from a single command, which is how an\n" +
			"agent inspects the interface without being able to see it — and, because\n" +
			"that path is wired with no writer at all, without being able to bless\n" +
			"anything through it.\n\n" +
			"--sample opens the same read-only board interactively, so the interface\n" +
			"can be walked through without an API key or a team.\n\n" +
			"Running it against a real board requires LINEAR_API_KEY, and a team key —\n" +
			"either --team-key, or [team] key in the nearest wand.toml.\n\n" +
			"Press 'e' to engage: the cockpit then polls Todo and Scoping on --interval\n" +
			"(default 1m) the way `wand dispatch --watch` does, spawning a winner as a\n" +
			"detached child that survives the cockpit closing. Engaging is deliberate —\n" +
			"it is never on by default, bare `wand` included — and needs the same\n" +
			"dependencies `wand dispatch` does (--harness, --model, --effort and\n" +
			"commands.verify in wand.toml); missing them leaves the cockpit open and\n" +
			"read-only, refusing only the 'e' key.",
		Args: cobra.NoArgs,
		Example: "  wand ui --team-key WND\n" +
			"  wand ui --sample\n" +
			"  wand ui --dump-screen\n" +
			"  wand ui --script j,enter --dump-screen",
		RunE: func(cmd *cobra.Command, _ []string) error {
			// A --team-key that would be silently ignored is worse than a
			// refusal: it reads as "this is my board" on a screen that is
			// nobody's board. Tested on the flag actually being passed, not
			// on the resolved value: a wand.toml-derived key must never flow
			// into this check, or every --dump-screen run inside a repo with
			// a keyed wand.toml would start erroring. Same rule for the
			// engage-mode flags: none of them mean anything on a board with
			// no writer behind it.
			if dumpScreen || sample {
				for _, name := range []string{"team-key", "harness", "model", "effort", "interval"} {
					if cmd.Flags().Changed(name) {
						return fmt.Errorf("the --%s flag does not apply with --dump-screen or --sample; both render the built-in sample board and read no team", name)
					}
				}
			}
			// Same rule, other direction. An interactive program is sized
			// by the terminal it is attached to: Bubble Tea's first message
			// is the real window size, which overwrites anything passed
			// here. A flag that is quietly overruled reads as "this is the
			// width I asked for" on a screen that is whatever width the
			// window happens to be.
			if !dumpScreen {
				for _, name := range []string{"width", "height"} {
					if cmd.Flags().Changed(name) {
						return fmt.Errorf("the --%s flag only applies with --dump-screen; an interactive board is sized by the terminal it runs in", name)
					}
				}
			}
			if dumpScreen {
				return dumpCockpit(cmd, script, width, height)
			}
			if script != "" {
				return fmt.Errorf("the --script flag only applies with --dump-screen")
			}
			if sample {
				_, err := tea.NewProgram(sampleModel(width, height)).Run()
				return err
			}
			return runCockpit(cmd, teamKey, harness, model, effort, width, height, interval)
		},
	}

	f := cmd.Flags()
	f.StringVar(&teamKey, "team-key", "", "Linear team key, e.g. WND (falls back to [team] key in wand.toml)")
	f.StringVar(&script, "script", "", "comma-separated keys to apply before rendering, e.g. \"j,enter\"")
	f.BoolVar(&dumpScreen, "dump-screen", false, "render the sample board to plain text and print it")
	f.BoolVar(&sample, "sample", false, "open the built-in sample board instead of reading Linear")
	f.IntVar(&width, "width", screen.DefaultWidth, "terminal width to render at")
	f.IntVar(&height, "height", screen.DefaultHeight, "terminal height to render at")
	f.StringVar(&harness, "harness", "claude-code", "worker harness engage mode runs a winner through: claude-code or codex")
	f.StringVar(&model, "model", "", "model engage mode gives every worker (default: the harness's default)")
	f.StringVar(&effort, "effort", "", "reasoning effort engage mode gives every worker (default: the harness's default)")
	f.DurationVar(&interval, "interval", time.Minute, "how often engage mode polls once engaged")

	return cmd
}

// dumpCockpit renders the sample board with no backend.
//
// No backend is the whole point, and it is a structural refusal rather than
// a flag: this is the path an agent can reach, so nothing on it can write.
// The sample board is also what makes the dump comparable — a screen built
// from whatever happened to be in a live Triage is one no two people can
// diff, and it would need an API key to look at a user interface.
func dumpCockpit(cmd *cobra.Command, script string, width, height int) error {
	msgs, err := screen.ParseScript(script)
	if err != nil {
		return fmt.Errorf("could not parse --script: %w", err)
	}

	// The same renderer the test harness uses, so this output is
	// byte-identical to the golden files under internal/tui/testdata.
	result, err := screen.Render(sampleModel(width, height), msgs, width, height)
	if err != nil {
		return fmt.Errorf("rendering screen: %w", err)
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), result)
	return err
}

// sampleModel is the built-in board with no backend behind it — the model
// both --dump-screen and --sample run. One constructor, so the screen an
// agent dumps and the screen a person walks through cannot differ.
func sampleModel(width, height int) tui.Model {
	return tui.New(tui.Config{
		Snapshot: cockpit.Sample(),
		Covenant: covenant.Default(),
		Width:    width,
		Height:   height,
		Notice:   sampleNotice,
	})
}

// runCockpit reads the real board, then hands it to the program.
//
// The read happens before the alternate screen opens, deliberately: a
// Linear failure is then an ordinary command error on stderr rather than a
// message trapped inside a full-screen app the user has to quit out of to
// read.
func runCockpit(cmd *cobra.Command, teamKey, harness, model, effort string, width, height int, interval time.Duration) error {
	cov, fileTeamKey, _, err := covenantFromCwd()
	if err != nil {
		return err
	}
	resolvedTeamKey, err := resolveTeamKey(teamKey, fileTeamKey)
	if err != nil {
		return err
	}
	cl, err := linearFromEnv()
	if err != nil {
		return err
	}
	runs, err := journal.Default()
	if err != nil {
		return err
	}

	back := &cockpitBackend{cl: cl, runs: runs, cov: cov, teamKey: resolvedTeamKey}
	ctx, cancel := context.WithTimeout(cmd.Context(), apiTimeout)
	snap, err := back.Read(ctx)
	cancel()
	if err != nil {
		return err
	}

	tm := tui.New(tui.Config{
		Snapshot: snap,
		Backend:  back,
		Covenant: cov,
		Width:    width,
		Height:   height,
		Engager:  buildEngager(cmd, teamKey, harness, model, effort),
		Interval: interval,
	})
	_, err = tea.NewProgram(tm).Run()
	return err
}

// buildEngager assembles engage mode's dependencies — the same dispatch.Deps
// surface `wand dispatch` builds through dispatchDeps — and returns nil when
// they cannot be assembled, so the cockpit still opens read-only rather than
// refusing to open at all. Bare `wand` and `wand ui` both go through this:
// nothing here starts engage mode, it only makes the 'e' key possible to
// press. Errors are swallowed on purpose — Read has already succeeded by the
// time this runs, so a missing verify command or an unauthenticated `gh`
// (neither of which this function checks; dispatchDeps does not either) is a
// story for the moment the toggle is actually pressed, told through the same
// flash a failed judgment already uses.
func buildEngager(cmd *cobra.Command, teamKey, harness, model, effort string) tui.Engager {
	d, store, err := dispatchDeps(cmd, teamKey, harness, model, effort)
	if err != nil {
		return nil
	}
	bin, err := os.Executable()
	if err != nil {
		return nil
	}
	logDir := filepath.Join(store.Root, "dispatch", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil
	}
	return &cockpitEngager{deps: d, store: store, bin: bin, logDir: logDir}
}

// cockpitEngager is tui.Engager over the dispatch package: the same
// lock/select/spawn mechanics `wand dispatch --watch` runs in its own
// process, driven one poll at a time from inside the cockpit instead.
type cockpitEngager struct {
	deps   dispatch.Deps
	store  *journal.Store
	bin    string
	logDir string

	lock    *dispatch.Lock
	pending *dispatch.Pending
}

func (e *cockpitEngager) AcquireLock() error {
	lock, err := dispatch.Acquire(e.store.Root, e.deps.Repo)
	if err != nil {
		return err
	}
	e.lock = lock
	e.pending = dispatch.NewPending()
	return nil
}

func (e *cockpitEngager) ReleaseLock() error {
	if e.lock == nil {
		return nil
	}
	err := e.lock.Release()
	e.lock = nil
	return err
}

func (e *cockpitEngager) Tick(ctx context.Context) (tui.EngageResult, error) {
	if e.lock == nil {
		return tui.EngageResult{}, fmt.Errorf("dispatch: engage mode ticked without holding its own lock; this is a wand bug")
	}
	w := dispatch.WatchDeps{Deps: e.deps, Bin: e.bin, LogDir: e.logDir}
	res, err := w.Tick(ctx, e.store, e.pending, e.logDir)
	if err != nil {
		return tui.EngageResult{}, err
	}
	out := tui.EngageResult{
		Dispatched: res.Dispatched,
		LanesUsed:  res.LanesUsed,
		LanesCap:   res.LanesCap,
	}
	if res.Dispatched {
		out.Ticket = res.Winner.Issue.Identifier
		out.Verb = string(res.Winner.Verb)
	}
	return out, nil
}

// cockpitBackend is the live I/O behind the screen: Linear for the board and
// the judgments, the run store for the lanes.
type cockpitBackend struct {
	cl      cockpit.Linear
	runs    cockpit.Runs
	cov     covenant.Covenant
	teamKey string
}

func (b *cockpitBackend) Read(ctx context.Context) (cockpit.Snapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, apiTimeout)
	defer cancel()
	return cockpit.Read(ctx, b.cl, b.runs, b.cov, b.teamKey)
}

func (b *cockpitBackend) Apply(ctx context.Context, in cockpit.Intent) (cockpit.Intent, error) {
	ctx, cancel := context.WithTimeout(ctx, apiTimeout)
	defer cancel()
	return cockpit.Apply(ctx, b.cl, b.cov, in)
}
