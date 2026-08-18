package cli

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/mattwalters/wand/internal/screen"
	"github.com/mattwalters/wand/internal/tui"
)

func newUICmd() *cobra.Command {
	var (
		script     string
		dumpScreen bool
		width      int
		height     int
	)

	cmd := &cobra.Command{
		Use:   "ui",
		Short: "Open the interactive interface",
		Long: "Open wand's interactive interface.\n\n" +
			"With --dump-screen, the interface is rendered to plain text and printed\n" +
			"instead of being displayed, which needs no terminal. Combined with\n" +
			"--script this makes any screen reachable from a single command, which is\n" +
			"how an agent inspects the interface without being able to see it.",
		Args: cobra.NoArgs,
		Example: "  wand ui\n" +
			"  wand ui --dump-screen\n" +
			"  wand ui --script j,enter --dump-screen",
		RunE: func(cmd *cobra.Command, _ []string) error {
			model := tui.New(width, height)

			if !dumpScreen {
				if script != "" {
					return fmt.Errorf("the --script flag only applies with --dump-screen")
				}
				_, err := tea.NewProgram(model).Run()
				return err
			}

			msgs, err := screen.ParseScript(script)
			if err != nil {
				return fmt.Errorf("could not parse --script: %w", err)
			}

			// The same renderer the test harness uses, so this output is
			// byte-identical to the golden files under internal/tui/testdata.
			result, err := screen.Render(model, msgs, width, height)
			if err != nil {
				return fmt.Errorf("rendering screen: %w", err)
			}

			_, err = fmt.Fprintln(cmd.OutOrStdout(), result)
			return err
		},
	}

	f := cmd.Flags()
	f.StringVar(&script, "script", "", "comma-separated keys to apply before rendering, e.g. \"j,enter\"")
	f.BoolVar(&dumpScreen, "dump-screen", false, "render the interface to plain text and print it")
	f.IntVar(&width, "width", screen.DefaultWidth, "terminal width to render at")
	f.IntVar(&height, "height", screen.DefaultHeight, "terminal height to render at")

	return cmd
}
