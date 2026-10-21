package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"

	"github.com/rajputomsingh/relay/internal/config"
	"github.com/rajputomsingh/relay/internal/delivery"
	"github.com/rajputomsingh/relay/internal/endpoints"
	"github.com/rajputomsingh/relay/internal/handlers"

	_ "github.com/rajputomsingh/relay/docs"
	httpSwagger "github.com/swaggo/http-swagger"
)

// @title Relay API
// @version 1.0
// @description Reliable webhook delivery infrastructure.
// @termsOfService http://swagger.io/terms/

// @contact.name Relay Support
// @contact.url https://github.com/rajputomsingh/Relay

// @license.name Apache 2.0
// @license.url http://www.apache.org/licenses/LICENSE-2.0.html

// @host localhost:8080
// @BasePath /
// @securityDefinitions.apikey ApiKeyAuth
// @in header
// @name Authorization
func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	cfg, err := config.Load()
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Connect to MongoDB
	connectCtx, connectCancel := context.WithTimeout(ctx, 10*time.Second)
	defer connectCancel()

	client, err := mongo.Connect(options.Client().ApplyURI(cfg.MongoURI))
	if err != nil {
		slog.Error("failed to initialize mongo client", "error", err)
		os.Exit(1)
	}
	defer client.Disconnect(context.Background())

	if err := client.Ping(connectCtx, readpref.Primary()); err != nil {
		slog.Error("failed to ping mongo", "error", err)
		os.Exit(1)
	}

	db := client.Database(cfg.MongoDatabase)
	eventsCollection := db.Collection("events")
	endpointsCollection := db.Collection("endpoints")
	deliveriesCollection := db.Collection("deliveries")
	attemptsCollection := db.Collection("delivery_attempts")

	endpointStore := endpoints.NewStore(endpointsCollection)

	// Construct the delivery worker
	worker := delivery.NewWorker(
		eventsCollection,
		endpointsCollection,
		deliveriesCollection,
		attemptsCollection,
		delivery.Config{
			Logger:         slog.Default(),
			Concurrency:    cfg.WorkerConcurrency,
			RequestTimeout: 10 * time.Second,
		},
	)

	// Register HTTP handlers.
	mux := http.NewServeMux()

	mux.HandleFunc("/swagger/", httpSwagger.WrapHandler)

	healthHandler := handlers.NewHealthHandler(client)
	healthHandler.Register(mux)

	fanout := delivery.NewFanout(
		eventsCollection,
		endpointsCollection,
		deliveriesCollection,
		slog.Default(),
	)

	eventsHandler := handlers.NewEventsHandler(
		eventsCollection,
		cfg.RelayAPIKey,
		fanout,
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
		deliveriesCollection,
		cfg.RelayAPIKey,
	)

	metricsHandler := handlers.NewMetricsHandler(
		eventsCollection,
		attemptsCollection,
		deliveriesCollection,
		cfg.RelayAPIKey,
	).WithWorkerHealth(worker)
	metricsHandler.Register(mux)
	inspectionHandler.Register(mux)

	endpointsHandler := handlers.NewEndpointsHandler(
		endpointStore,
		cfg.RelayAPIKey,
	)
	endpointsHandler.Register(mux)

	server := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Start the delivery worker loop
	go worker.Run(ctx)

	// Start the HTTP server
	go func() {
		slog.Info("starting HTTP server", "port", cfg.Port)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http server error", "error", err)
			cancel()
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down gracefully...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("graceful shutdown failed", "error", err)
	}

	slog.Info("shutdown complete")
}
