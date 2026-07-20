package queue

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

var ErrAlreadyRecorded = errors.New("booking already recorded")

// BookingStore must be backed by a transactional database in production.
// Record is idempotent so a confirmation retry cannot create two bookings.
type BookingStore interface {
	Record(Booking) error
}

// FileBookingStore is a small durable demo implementation. Every entry is
// appended and synced before the call succeeds.
type FileBookingStore struct {
	path string
	mu   sync.Mutex
	seen map[string]struct{}
}

func NewFileBookingStore(path string) (*FileBookingStore, error) {
	store := &FileBookingStore{path: path, seen: map[string]struct{}{}}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	for decoder.More() {
		var booking Booking
		if err := decoder.Decode(&booking); err != nil {
			return nil, err
		}
		store.seen[booking.HoldID] = struct{}{}
	}
	return store, nil
}

func (s *FileBookingStore) Record(booking Booking) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.seen[booking.HoldID]; exists {
		return ErrAlreadyRecorded
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(s.path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	encoded, err := json.Marshal(booking)
	if err != nil {
		return err
	}
	if _, err := file.Write(append(encoded, '\n')); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	s.seen[booking.HoldID] = struct{}{}
	return nil
}
