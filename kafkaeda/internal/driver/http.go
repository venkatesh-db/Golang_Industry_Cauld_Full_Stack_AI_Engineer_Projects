package driver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/venkatesh/kafkaeda/internal/events"
	"github.com/venkatesh/kafkaeda/internal/platform/outbox"
	"github.com/venkatesh/kafkaeda/internal/platform/postgres"
)

type locationCommand struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

func RunHTTP(ctx context.Context, pool *pgxpool.Pool, address string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "driver-api"})
	})
	mux.HandleFunc("POST /api/drivers/{id}/location", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("content-type"), "application/json") {
			writeError(w, http.StatusUnsupportedMediaType, "content-type must be application/json")
			return
		}
		var command locationCommand
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&command); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON request")
			return
		}
		if err := recordLocation(r.Context(), pool, r.PathValue("id"), command); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeError(w, http.StatusNotFound, "driver not found")
				return
			}
			if strings.Contains(err.Error(), "latitude") || strings.Contains(err.Error(), "longitude") {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			slog.Error("record driver location", "error", err)
			writeError(w, http.StatusInternalServerError, "unable to save location")
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "location accepted"})
	})

	server := &http.Server{Addr: address, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	slog.Info("driver API listening", "address", address)
	err := server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func recordLocation(ctx context.Context, pool *pgxpool.Pool, driverID string, command locationCommand) error {
	if driverID == "" {
		return fmt.Errorf("driver id is required")
	}
	if math.IsNaN(command.Latitude) || command.Latitude < -90 || command.Latitude > 90 {
		return fmt.Errorf("latitude must be between -90 and 90")
	}
	if math.IsNaN(command.Longitude) || command.Longitude < -180 || command.Longitude > 180 {
		return fmt.Errorf("longitude must be between -180 and 180")
	}
	return postgres.InTx(ctx, pool, func(tx pgx.Tx) error {
		result, err := tx.Exec(ctx, `UPDATE driver.drivers SET latitude = $2, longitude = $3, updated_at = now() WHERE id = $1`, driverID, command.Latitude, command.Longitude)
		if err != nil {
			return fmt.Errorf("update driver location: %w", err)
		}
		if result.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		event, err := events.New("driver.location.updated", "driver-api", "", "", events.DriverLocationUpdated{
			DriverID: driverID, Latitude: command.Latitude, Longitude: command.Longitude,
		})
		if err != nil {
			return fmt.Errorf("create location event: %w", err)
		}
		return outbox.Add(ctx, tx, events.DriverLocationTopic, driverID, event)
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
