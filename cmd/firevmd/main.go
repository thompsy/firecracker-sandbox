package main

import (
	"log/slog"
	"os"

	"github.com/thompsy/firecracker-sandbox/daemon"
)

func main() {
	err := daemon.NewServer().Run()
	if err != nil {
		slog.Error("failed to run", "error", err)
		os.Exit(1)
	}
}
