package cli

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/mattwalters/wand/internal/cockpit"
	"github.com/mattwalters/wand/internal/covenant"
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
			"Running it against a real board requires LINEAR_API_KEY and --team-key.",
		Args: cobra.NoArgs,
		Example: "  wand ui --team-key WND\n" +
			"  wand ui --sample\n" +
			"  wand ui --dump-screen\n" +
			"  wand ui --script j,enter --dump-screen",
		RunE: func(cmd *cobra.Command, _ []string) error {
			// A --team-key that would be silently ignored is worse than a
			// refusal: it reads as "this is my board" on a screen that is
			// nobody's board.
			if teamKey != "" && (dumpScreen || sample) {
				return fmt.Errorf("--team-key does not apply with --dump-screen or --sample; both render the built-in sample board and read no team")
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
			return runCockpit(cmd, teamKey, width, height)
		},
	}

	f := cmd.Flags()
	f.StringVar(&teamKey, "team-key", "", "Linear team key, e.g. WND")
	f.StringVar(&script, "script", "", "comma-separated keys to apply before rendering, e.g. \"j,enter\"")
	f.BoolVar(&dumpScreen, "dump-screen", false, "render the sample board to plain text and print it")
	f.BoolVar(&sample, "sample", false, "open the built-in sample board instead of reading Linear")
	f.IntVar(&width, "width", screen.DefaultWidth, "terminal width to render at")
	f.IntVar(&height, "height", screen.DefaultHeight, "terminal height to render at")

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
func runCockpit(cmd *cobra.Command, teamKey string, width, height int) error {
	if teamKey == "" {
		return fmt.Errorf("--team-key is required (e.g. --team-key WND)")
	}
	cl, err := linearFromEnv()
	if err != nil {
		return err
	}
	cov, _, err := covenant.Load(covenant.FileName)
	if err != nil {
		return err
	}
	runs, err := journal.Default()
	if err != nil {
		return err
	}

	back := &cockpitBackend{cl: cl, runs: runs, cov: cov, teamKey: teamKey}
	ctx, cancel := context.WithTimeout(cmd.Context(), apiTimeout)
	snap, err := back.Read(ctx)
	cancel()
	if err != nil {
		return err
	}

	model := tui.New(tui.Config{
		Snapshot: snap,
		Backend:  back,
		Covenant: cov,
		Width:    width,
		Height:   height,
	})
	_, err = tea.NewProgram(model).Run()
	return err
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

func (b *cockpitBackend) Apply(ctx context.Context, in cockpit.Intent) error {
	ctx, cancel := context.WithTimeout(ctx, apiTimeout)
	defer cancel()
	return cockpit.Apply(ctx, b.cl, b.cov, in)
}
