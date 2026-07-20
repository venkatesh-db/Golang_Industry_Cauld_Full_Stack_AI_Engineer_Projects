package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/venkatesh/kafkaeda/internal/activity"
	"github.com/venkatesh/kafkaeda/internal/config"
	"github.com/venkatesh/kafkaeda/internal/dispatch"
	"github.com/venkatesh/kafkaeda/internal/driver"
	"github.com/venkatesh/kafkaeda/internal/platform/migrations"
	"github.com/venkatesh/kafkaeda/internal/platform/outbox"
	"github.com/venkatesh/kafkaeda/internal/platform/postgres"
	"github.com/venkatesh/kafkaeda/internal/ride"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:]); err != nil {
		slog.Error("rapido exited", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, arguments []string) error {
	role := "all"
	if len(arguments) > 0 {
		role = arguments[0]
	}
	cfg := config.FromEnv()
	pool, err := postgres.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	switch role {
	case "migrate":
		return migrations.Apply(ctx, pool)
	case "ride-api":
		return ride.RunHTTP(ctx, pool, cfg.HTTPAddr)
	case "driver-api":
		return driver.RunHTTP(ctx, pool, cfg.HTTPAddr)
	case "outbox-relay":
		relay := outbox.NewRelay(pool, cfg.KafkaBrokers)
		defer relay.Close()
		return relay.Run(ctx)
	case "dispatch-service":
		return dispatch.Run(ctx, pool, cfg.KafkaBrokers)
	case "ride-projection":
		return ride.RunProjector(ctx, pool, cfg.KafkaBrokers)
	case "activity-service":
		return activity.Run(ctx, pool, cfg.KafkaBrokers)
	case "all":
		if err := migrations.Apply(ctx, pool); err != nil {
			return err
		}
		return runAll(ctx, pool, cfg)
	default:
		return fmt.Errorf("unknown role %q; use migrate, ride-api, driver-api, outbox-relay, dispatch-service, ride-projection, activity-service, or all", role)
	}
}

func runAll(ctx context.Context, pool *pgxpool.Pool, cfg config.Config) error {
	relay := outbox.NewRelay(pool, cfg.KafkaBrokers)
	defer relay.Close()
	errors := make(chan error, 6)
	go func() { errors <- ride.RunHTTP(ctx, pool, cfg.HTTPAddr) }()
	go func() { errors <- driver.RunHTTP(ctx, pool, cfg.DriverAddr) }()
	go func() { errors <- relay.Run(ctx) }()
	go func() { errors <- dispatch.Run(ctx, pool, cfg.KafkaBrokers) }()
	go func() { errors <- ride.RunProjector(ctx, pool, cfg.KafkaBrokers) }()
	go func() { errors <- activity.Run(ctx, pool, cfg.KafkaBrokers) }()
	select {
	case <-ctx.Done():
		return nil
	case err := <-errors:
		return err
	}
}
