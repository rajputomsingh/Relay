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
		"concurrency", cfg.Concurrency,
		"behavior", cfg.Behavior,
		"count", cfg.Count,
		"drain_timeout", cfg.DrainTimeout.String(),
		"warmup", cfg.WarmupDeliveries,
		"output", cfg.OutputDir,
	)

	ctx, cancel := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer cancel()

	connectCtx, cancelConnect := context.WithTimeout(ctx, 10*1e9)
	defer cancelConnect()

	client, err := mongo.Connect(options.Client().
		ApplyURI(cfg.MongoURI).
		SetAppName("relay-bench").
		SetServerSelectionTimeout(5e9))
	if err != nil {
		return fmt.Errorf("connect mongo: %w", err)
	}
	defer func() {
		disconnectCtx, disconnectCancel := context.WithTimeout(
			context.Background(), 5*1e9,
		)
		defer disconnectCancel()
		_ = client.Disconnect(disconnectCtx)
	}()

	if err := client.Ping(connectCtx, readpref.Primary()); err != nil {
		return fmt.Errorf("ping mongo: %w", err)
	}

	dbName := cfg.Database
	if dbName == "" {
		dbName = uniqueDatabaseName()
	}
	db := client.Database(dbName)

	logger.Info("benchmark database selected", "database", dbName)

	defer func() {
		dropCtx, dropCancel := context.WithTimeout(
			context.Background(), 10*1e9,
		)
		defer dropCancel()
		if err := dropBenchmarkDatabase(dropCtx, db); err != nil {
			logger.Warn("failed to drop benchmark database", "error", err)
		}
	}()

	if err := ensureIndexes(ctx, db); err != nil {
		return err
	}

	server := newMockServer(
		cfg.Behavior,
		cfg.SlowDelay,
		cfg.TransientFailures,
	)
	defer server.Close()

	logServerStart(logger, server)

	seeded, err := seed(
		ctx,
		db,
		server.URL(),
		cfg.Count,
		cfg.MaxAttemptsOrDefault(),
	)
	if err != nil {
		return err
	}

	logger.Info(
		"deliveries seeded",
		"count", len(seeded.DeliveryIDs),
		"endpoint_id", seeded.EndpointID.Hex(),
	)

	result, err := run(ctx, runnerOptions{
		cfg:      cfg,
		database: db,
		dbName:   dbName,
		server:   server,
		seedData: seeded,
		logger:   logger,
	})
	if err != nil {
		return err
	}

	path, err := writeJSONResult(cfg, result)
	if err != nil {
		return err
	}

	logger.Info("result written", "path", path)
	summarize(result)
	return nil
}
