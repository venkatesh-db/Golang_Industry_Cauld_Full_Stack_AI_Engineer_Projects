package queue

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func Handler(service *Service) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("PUT /slots/{slot}/capacity", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Capacity int `json:"capacity"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if err := service.SetCapacity(r.PathValue("slot"), input.Capacity); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /holds", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Slot        string `json:"slot"`
			VisitorID   string `json:"visitor_id"`
			HoldSeconds int    `json:"hold_seconds"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		hold, err := service.CreateHold(input.Slot, input.VisitorID, time.Duration(input.HoldSeconds)*time.Second)
		if errors.Is(err, ErrSlotFull) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, hold)
	})
	mux.HandleFunc("GET /holds/{id}", func(w http.ResponseWriter, r *http.Request) {
		hold, err := service.GetHold(r.PathValue("id"))
		if errors.Is(err, ErrHoldMissing) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, hold)
	})
	mux.HandleFunc("POST /holds/{id}/confirm", func(w http.ResponseWriter, r *http.Request) {
		booking, err := service.ConfirmHold(r.PathValue("id"))
		if errors.Is(err, ErrHoldInactive) || errors.Is(err, ErrHoldMissing) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, booking)
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": strings.TrimSpace(message), "status": strconv.Itoa(status)})
}
