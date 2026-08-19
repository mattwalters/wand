package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mattwalters/wand/internal/covenant"
)

// Version, Commit and Date identify the build. They are overridden at build
// time via ldflags (goreleaser stamps them on release; see .goreleaser.yml):
//
//	go build -ldflags "-X github.com/mattwalters/wand/internal/cli.Version=v0.1.0" .
//
// The zero values match goreleaser's conventions for an unstamped build.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Report this binary's version and the covenant schema it speaks",
		Long: "version reports the build (version, commit, date) and the covenant\n" +
			"schema version this binary speaks. A repo's covenant file declares the\n" +
			"schema it was written against; compare the two to learn whether this\n" +
			"binary can read it.",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprint(cmd.OutOrStdout(), versionString(Version, Commit, Date, covenant.SchemaVersion))
		},
	}
}

// versionString renders the version report. Pure so tests can hold it.
func versionString(version, commit, date string, schema int) string {
	return fmt.Sprintf(
		"wand %s\n  commit:          %s\n  built:           %s\n  covenant schema: %d\n",
		version, commit, date, schema,
	)
}
