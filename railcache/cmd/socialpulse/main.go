// Command socialpulse runs an Instagram-style, event-driven engagement feature.
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

	"railcache/internal/social"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	config, err := social.LoadConfig()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	openCtx, cancelOpen := context.WithTimeout(ctx, 10*time.Second)
	store, err := social.OpenStore(openCtx, config.DatabaseURL)
	cancelOpen()
	if err != nil {
		return err
	}
	defer store.Close()

	broker := social.NewBroker(config.KafkaBrokers, log)
	defer broker.Close()
	runtime := social.NewRuntime(store, broker, config, log)
	runtime.Start(ctx)

	api, err := social.NewHTTPServer(store, log)
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr:              config.HTTPAddr,
		Handler:           api.Router(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		log.Info("socialpulse listening", "addr", config.HTTPAddr, "kafka_brokers", config.KafkaBrokers)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	select {
	case signal := <-stop:
		log.Info("shutdown signal received", "signal", signal)
	case err := <-errCh:
		return err
	}
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancelShutdown()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return err
	}
	cancel() // stop pollers before closing the DB and Kafka clients
	if err := runtime.Wait(shutdownCtx); err != nil {
		return err
	}
	return nil
}
