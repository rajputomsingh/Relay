package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rajputomsingh/relay/internal/config"
	"github.com/rajputomsingh/relay/internal/database"
	"github.com/rajputomsingh/relay/internal/delivery"
	"github.com/rajputomsingh/relay/internal/handlers"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(
		os.Stdout,
		&slog.HandlerOptions{
			Level: slog.LevelInfo,
		},
	)))

	if err := run(); err != nil {
		slog.Error(
			"relay stopped",
			"error_type", fmt.Sprintf("%T", err),
		)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	startupCtx, cancelStartup := context.WithTimeout(
		context.Background(),
		10*time.Second,
	)

	client, err := database.Connect(startupCtx, cfg.MongoURI)
	cancelStartup()

	if err != nil {
		return fmt.Errorf("MongoDB initialization failed: %w", err)
	}

	defer func() {
		ctx, cancel := context.WithTimeout(
			context.Background(),
			5*time.Second,
		)
		defer cancel()

		if err := client.Disconnect(ctx); err != nil {
			slog.Error(
				"MongoDB disconnect failed",
				"error_type", fmt.Sprintf("%T", err),
			)
		}
	}()

	db := client.Database(cfg.MongoDatabase)

	eventsCollection := db.Collection("events")
	attemptsCollection := db.Collection("delivery_attempts")

	// Create the unique idempotency index.
	indexCtx, cancelIndex := context.WithTimeout(
		context.Background(),
		10*time.Second,
	)

	_, err = eventsCollection.Indexes().CreateOne(
		indexCtx,
		mongo.IndexModel{
			Keys: bson.D{
				{Key: "idempotency_key", Value: 1},
			},
			Options: options.Index().
				SetUnique(true).
				SetName("uniq_event_idempotency_key"),
		},
	)
	cancelIndex()

	if err != nil {
		return errors.New("failed to initialize idempotency index")
	}

	// Create indexes used by the delivery worker.
	deliveryIndexCtx, cancelDeliveryIndex := context.WithTimeout(
		context.Background(),
		10*time.Second,
	)

	_, err = eventsCollection.Indexes().CreateMany(
		deliveryIndexCtx,
		[]mongo.IndexModel{
			{
				Keys: bson.D{
					{Key: "delivery_status", Value: 1},
					{Key: "next_attempt_at", Value: 1},
					{Key: "received_at", Value: 1},
				},
				Options: options.Index().
					SetName("idx_delivery_due"),
			},
			{
				Keys: bson.D{
					{Key: "delivery_status", Value: 1},
					{Key: "lease_until", Value: 1},
				},
				Options: options.Index().
					SetName("idx_delivery_lease"),
			},
		},
	)
	cancelDeliveryIndex()

	if err != nil {
		return errors.New("failed to initialize delivery indexes")
	}

	// Indexes for event listing and stable cursor pagination.
	listingIndexCtx, cancelListingIndex := context.WithTimeout(
		context.Background(),
		10*time.Second,
	)

	_, err = eventsCollection.Indexes().CreateMany(
		listingIndexCtx,
		[]mongo.IndexModel{
			{
				Keys: bson.D{
					{Key: "received_at", Value: -1},
					{Key: "_id", Value: -1},
				},
				Options: options.Index().
					SetName("idx_events_received_id"),
			},
			{
				Keys: bson.D{
					{Key: "delivery_status", Value: 1},
					{Key: "received_at", Value: -1},
					{Key: "_id", Value: -1},
				},
				Options: options.Index().
					SetName("idx_events_status_received_id"),
			},
		},
	)
	cancelListingIndex()

	if err != nil {
		return errors.New("failed to initialize event listing indexes")
	}

	// Index for querying attempts by event and time.
	attemptIndexCtx, cancelAttemptIndex := context.WithTimeout(
		context.Background(),
		10*time.Second,
	)

	_, err = attemptsCollection.Indexes().CreateOne(
		attemptIndexCtx,
		mongo.IndexModel{
			Keys: bson.D{
				{Key: "event_id", Value: 1},
				{Key: "started_at", Value: -1},
			},
			Options: options.Index().
				SetName("idx_attempts_event_started"),
		},
	)
	cancelAttemptIndex()

	if err != nil {
		return errors.New("failed to initialize delivery attempt indexes")
	}

	// Compound index for stable cursor-based attempt pagination.
	paginationIndexCtx, cancelPaginationIndex := context.WithTimeout(
		context.Background(),
		10*time.Second,
	)

	_, err = attemptsCollection.Indexes().CreateOne(
		paginationIndexCtx,
		mongo.IndexModel{
			Keys: bson.D{
				{Key: "event_id", Value: 1},
				{Key: "started_at", Value: -1},
				{Key: "_id", Value: -1},
			},
			Options: options.Index().
				SetName("idx_attempts_event_started_id"),
		},
	)
	cancelPaginationIndex()

	if err != nil {
		return errors.New("failed to initialize attempt pagination index")
	}

	// Register HTTP handlers.
	mux := http.NewServeMux()

	healthHandler := handlers.NewHealthHandler(client)
	healthHandler.Register(mux)

	eventsHandler := handlers.NewEventsHandler(
		eventsCollection,
		cfg.RelayAPIKey,
		cfg.WebhookURL != "",
	)
	eventsHandler.Register(mux)

	eventsListHandler := handlers.NewEventsListHandler(
		eventsCollection,
		cfg.RelayAPIKey,
	)
	eventsListHandler.Register(mux)

	inspectionHandler := handlers.NewInspectionHandler(
		eventsCollection,
		attemptsCollection,
		cfg.RelayAPIKey,
	)

	metricsHandler := handlers.NewMetricsHandler(
		eventsCollection,
		attemptsCollection,
		cfg.RelayAPIKey,
	)
	metricsHandler.Register(mux)
	inspectionHandler.Register(mux)

	server := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	stopCtx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	workerDone := make(chan struct{})

	if cfg.WebhookURL != "" {
		worker := delivery.NewWorker(
			eventsCollection,
			attemptsCollection,
			delivery.Config{
				WebhookURL:           cfg.WebhookURL,
				PollInterval:         time.Duration(cfg.WorkerPollSeconds) * time.Second,
				MaxAttempts:          cfg.MaxDeliveryAttempts,
				RequestTimeout:       10 * time.Second,
				LeaseDuration:        30 * time.Second,
				WebhookSigningSecret: cfg.WebhookSigningSecret,
			},
		)

		go func() {
			defer close(workerDone)
			worker.Run(stopCtx)
		}()

		slog.Info("webhook delivery enabled")
	} else {
		close(workerDone)
		slog.Info(
			"webhook delivery disabled",
			"reason", "webhook_url_not_configured",
		)
	}

	serverErrors := make(chan error, 1)

	go func() {
		slog.Info(
			"relay starting",
			"environment", cfg.Environment,
			"port", cfg.Port,
		)
		serverErrors <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErrors:
		stop()
		<-workerDone

		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}

		// Do not log the raw server error; keep details out of logs.
		return fmt.Errorf("HTTP server failed: %w", err)

	case <-stopCtx.Done():
		slog.Info("shutdown signal received")

		shutdownCtx, cancel := context.WithTimeout(
			context.Background(),
			10*time.Second,
		)
		defer cancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			stop()
			<-workerDone
			return errors.New("HTTP server shutdown timed out")
		}

		stop()
		<-workerDone

		slog.Info("HTTP server shut down gracefully")
		return nil
	}
}
