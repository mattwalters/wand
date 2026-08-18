package cli

// Version is wand's version, overridden at build time:
//
//	go build -ldflags "-X github.com/mattwalters/wand/internal/cli.Version=v0.1.0" ./cmd/wand
var Version = "dev"
