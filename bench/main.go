package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(
		os.Stderr,
		&slog.HandlerOptions{Level: slog.LevelInfo},
	)))

	if err := runAll(); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		fmt.Fprintln(os.Stderr, "bench:", err)
		os.Exit(1)
	}
}

func runAll() error {
	cfg, err := parseFlags(os.Args[1:])
	if err != nil {
		return err
	}

	logger := slog.Default()

	logger.Info(
		"benchmark configuration resolved",
		"matrix", cfg.Matrix,
		"concurrency", cfg.Concurrency,
		"concurrency_levels", cfg.ConcurrencyLevels,
		"behavior", cfg.Behavior,
		"count", cfg.Count,
		"drain_timeout", cfg.DrainTimeout.String(),
		"output", cfg.OutputDir,
	)

	ctx, cancel := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer cancel()

	connectCtx, cancelConnect := context.WithTimeout(ctx, 10*time.Second)
	defer cancelConnect()

	client, err := mongo.Connect(options.Client().
		ApplyURI(cfg.MongoURI).
		SetAppName("relay-bench").
		SetServerSelectionTimeout(5 * time.Second))
	if err != nil {
		return fmt.Errorf("connect mongo: %w", err)
	}
	defer func() {
		disconnectCtx, disconnectCancel := context.WithTimeout(
			context.Background(), 5*time.Second,
		)
		defer disconnectCancel()
		_ = client.Disconnect(disconnectCtx)
	}()

	if err := client.Ping(connectCtx, readpref.Primary()); err != nil {
		return fmt.Errorf("ping mongo: %w", err)
	}

	if cfg.Matrix {
		return runMatrix(ctx, cfg, client, logger)
	}

	result, err := runSingle(ctx, cfg, client, logger)
	if err != nil {
		return err
	}
	summarize(result)
	return nil
}
