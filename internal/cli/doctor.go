package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/mattwalters/wand/internal/covenant"
	"github.com/mattwalters/wand/internal/doctor"
	"github.com/mattwalters/wand/internal/linear"
)

func newDoctorCmd() *cobra.Command {
	var teamKey string

	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diff the live Linear team against the covenant",
		Long: "doctor reads the team's statuses, labels, PR automations and settings,\n" +
			"diffs them against the covenant, and reports the drift. It writes\n" +
			"nothing; wand init is the verb that repairs.\n\n" +
			"Extra statuses outside the machine's path, extra labels, and\n" +
			"automations on events the covenant does not mention are tolerated\n" +
			"strangers, not drift.\n\n" +
			"Exit codes: 0 the team satisfies the covenant, 1 drift found,\n" +
			"2 the check could not run.\n\n" +
			"A checked-in wand.toml parameterizes the covenant; absent one, the\n" +
			"stock covenant applies. Requires LINEAR_API_KEY in the environment.",
		// Run, not RunE: the exit code is the contract — 1 is drift, 2 is
		// could-not-check — and fang exits 1 on any RunE error, which would
		// collapse the two. The command exits itself instead of returning.
		Run: func(cmd *cobra.Command, args []string) {
			if code := runDoctor(cmd, teamKey); code != doctor.ExitClean {
				os.Exit(code)
			}
		},
	}

	cmd.Flags().StringVar(&teamKey, "team-key", "", "Linear team key, e.g. WAND")
	return cmd
}

func runDoctor(cmd *cobra.Command, teamKey string) int {
	out, errOut := cmd.OutOrStdout(), cmd.ErrOrStderr()

	apiKey := os.Getenv("LINEAR_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(errOut, "could not check: LINEAR_API_KEY is not set; create a full-access key in Linear's security settings")
		return doctor.ExitError
	}
	if teamKey == "" {
		fmt.Fprintln(errOut, "could not check: --team-key is required (e.g. --team-key WAND)")
		return doctor.ExitError
	}

	cov, fromFile, err := covenant.Load(covenant.FileName)
	if err != nil {
		fmt.Fprintf(errOut, "could not check: %v\n", err)
		return doctor.ExitError
	}
	if fromFile {
		fmt.Fprintf(out, "using covenant file %s\n", covenant.FileName)
	} else {
		fmt.Fprintln(out, "no covenant file; using the stock covenant")
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Minute)
	defer cancel()

	return doctor.Run(ctx, &linear.Client{APIKey: apiKey}, out, errOut, teamKey, cov)
}
