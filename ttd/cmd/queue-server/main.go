package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"ttdqueue/internal/queue"
)

func main() {
	redisAddr := env("REDIS_ADDR", "localhost:6379")
	bookingPath := env("BOOKING_LOG_PATH", "data/bookings.jsonl")
	pollInterval, err := time.ParseDuration(env("TIMER_POLL_INTERVAL", "1s"))
	if err != nil {
		log.Fatalf("invalid TIMER_POLL_INTERVAL: %v", err)
	}
	bookings, err := queue.NewFileBookingStore(bookingPath)
	if err != nil {
		log.Fatalf("open booking store: %v", err)
	}
	service := queue.NewService(queue.NewRedisClient(redisAddr), bookings)
	stop := make(chan struct{})
	defer close(stop)
	service.StartExpiryWorker(stop, pollInterval)

	address := env("HTTP_ADDR", ":8080")
	log.Printf("TTD virtual queue listening on %s (Redis %s)", address, redisAddr)
	log.Fatal(http.ListenAndServe(address, queue.Handler(service)))
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
