// Command wand is an agent toolkit for repositories.
package main

import (
	"context"
	"os"

	"github.com/charmbracelet/fang"

	"github.com/mattwalters/wand/internal/cli"
)

func main() {
	if err := fang.Execute(
		context.Background(),
		cli.Root(),
		fang.WithVersion(cli.Version),
	); err != nil {
		os.Exit(1)
	}
}
