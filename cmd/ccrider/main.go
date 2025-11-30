package main

import (
	"github.com/neilberkman/ccrider/internal/interface/cli"
)

// Version information (injected by GoReleaser)
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

func main() {
	cli.SetVersion(Version, Commit, Date)
	cli.Execute()
}
