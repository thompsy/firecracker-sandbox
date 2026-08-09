package main

import (
	"log/slog"
	"os"

	"github.com/thompsy/firecracker-sandbox/daemon"
)

func main() {
	s := daemon.NewServer()
	err := s.Reconcile()
	if err != nil {
		slog.Error("failed to reconcile", "err", err)
		os.Exit(1)
	}

	err = s.Run()
	if err != nil {
		slog.Error("failed to run", "err", err)
		os.Exit(1)
	}
}
