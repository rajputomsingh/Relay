package main

import (
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(
		os.Stderr,
		&slog.HandlerOptions{Level: slog.LevelInfo},
	)))

	if err := run(); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		fmt.Fprintln(os.Stderr, "bench:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := parseFlags(os.Args[1:])
	if err != nil {
		return err
	}

	logger := slog.Default()

	logger.Info(
		"benchmark configuration resolved",
		"concurrency", cfg.Concurrency,
		"behavior", cfg.Behavior,
		"count", cfg.Count,
		"drain_timeout", cfg.DrainTimeout.String(),
		"warmup", cfg.WarmupDeliveries,
		"output", cfg.OutputDir,
	)

	server := newMockServer(
		cfg.Behavior,
		cfg.SlowDelay,
		cfg.TransientFailures,
	)
	defer server.Close()

	logServerStart(logger, server)

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "harness scaffolding only; runner arrives in the next commit.")
	return nil
}
