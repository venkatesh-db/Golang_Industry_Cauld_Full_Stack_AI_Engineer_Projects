package queue

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"
)

var (
	ErrSlotFull     = errors.New("slot is full")
	ErrHoldMissing  = errors.New("hold does not exist")
	ErrHoldInactive = errors.New("hold is no longer active")
)

type Service struct {
	redis    Redis
	bookings BookingStore
	now      func() time.Time
}

func NewService(redis Redis, bookings BookingStore) *Service {
	return &Service{redis: redis, bookings: bookings, now: time.Now}
}

func capacityKey(slot string) string { return "ttd:queue:slot:" + slot + ":capacity" }
func usedKey(slot string) string     { return "ttd:queue:slot:" + slot + ":used" }
func holdKey(id string) string       { return "ttd:queue:hold:" + id }
func timerKey() string               { return "ttd:queue:hold-deadlines" }
func confirmKey() string             { return "ttd:queue:pending-confirmations" }

func (s *Service) SetCapacity(slot string, capacity int) error {
	if slot == "" || capacity < 0 {
		return errors.New("slot and a non-negative capacity are required")
	}
	_, err := s.redis.Do("SET", capacityKey(slot), strconv.Itoa(capacity))
	return err
}

// CreateHold uses one Lua transaction so concurrent visitors cannot overbook.
func (s *Service) CreateHold(slot, visitorID string, ttl time.Duration) (Hold, error) {
	if slot == "" || visitorID == "" || ttl <= 0 {
		return Hold{}, errors.New("slot, visitor_id, and a positive hold duration are required")
	}
	id, err := randomID()
	if err != nil {
		return Hold{}, err
	}
	now := s.now().UTC()
	expires := now.Add(ttl)
	script := `
local capacity = tonumber(redis.call('GET', KEYS[1]) or '-1')
local used = tonumber(redis.call('GET', KEYS[2]) or '0')
if capacity < 0 then return -2 end
if used >= capacity then return 0 end
redis.call('HSET', KEYS[3], 'id', ARGV[1], 'slot', ARGV[2], 'visitor_id', ARGV[3], 'status', 'held', 'expires_at', ARGV[4])
redis.call('INCR', KEYS[2])
redis.call('ZADD', KEYS[4], ARGV[5], ARGV[1])
return 1`
	result, err := s.redis.Do("EVAL", script, "4", capacityKey(slot), usedKey(slot), holdKey(id), timerKey(), id, slot, visitorID, expires.Format(time.RFC3339Nano), strconv.FormatInt(expires.UnixMilli(), 10))
	if err != nil {
		return Hold{}, err
	}
	value, ok := result.(int64)
	if !ok {
		return Hold{}, fmt.Errorf("unexpected Redis create-hold response: %T", result)
	}
	switch value {
	case 1:
		return Hold{ID: id, Slot: slot, VisitorID: visitorID, Status: StatusHeld, ExpiresAt: expires}, nil
	case 0:
		return Hold{}, ErrSlotFull
	default:
		return Hold{}, errors.New("slot capacity has not been configured")
	}
}

func (s *Service) ConfirmHold(id string) (Booking, error) {
	hold, err := s.GetHold(id)
	if err != nil {
		return Booking{}, err
	}
	if hold.Status == StatusConfirmed || hold.Status == StatusConfirming {
		return s.persistAndFinalize(hold)
	}
	if hold.Status != StatusHeld || !hold.ExpiresAt.After(s.now()) {
		return Booking{}, ErrHoldInactive
	}
	script := `
if redis.call('HGET', KEYS[1], 'status') ~= 'held' then return 0 end
if tonumber(redis.call('ZSCORE', KEYS[2], ARGV[1]) or '0') <= tonumber(ARGV[2]) then return 0 end
redis.call('HSET', KEYS[1], 'status', 'confirming', 'confirmed_at', ARGV[3])
redis.call('ZREM', KEYS[2], ARGV[1])
redis.call('ZADD', KEYS[3], ARGV[2], ARGV[1])
return 1`
	confirmedAt := s.now().UTC()
	result, err := s.redis.Do("EVAL", script, "3", holdKey(id), timerKey(), confirmKey(), id, strconv.FormatInt(s.now().UnixMilli(), 10), confirmedAt.Format(time.RFC3339Nano))
	if err != nil {
		return Booking{}, err
	}
	if result != int64(1) {
		latest, err := s.GetHold(id)
		if err != nil {
			return Booking{}, err
		}
		if latest.Status == StatusConfirming || latest.Status == StatusConfirmed {
			return s.persistAndFinalize(latest)
		}
		return Booking{}, ErrHoldInactive
	}
	hold.Status = StatusConfirming
	hold.ConfirmedAt = confirmedAt
	return s.persistAndFinalize(hold)
}

// persistAndFinalize leaves a hold in `confirming` if durable persistence
// fails. The recovery worker can safely retry it after a process crash.
func (s *Service) persistAndFinalize(hold Hold) (Booking, error) {
	if hold.ConfirmedAt.IsZero() {
		return Booking{}, errors.New("confirming hold has no confirmation time")
	}
	booking := Booking{ID: "booking-" + hold.ID, HoldID: hold.ID, Slot: hold.Slot, VisitorID: hold.VisitorID, Confirmed: hold.ConfirmedAt}
	if err := s.bookings.Record(booking); err != nil && !errors.Is(err, ErrAlreadyRecorded) {
		return Booking{}, err
	}
	if hold.Status == StatusConfirmed {
		return booking, nil
	}
	script := `
if redis.call('HGET', KEYS[1], 'status') ~= 'confirming' then return 0 end
redis.call('HSET', KEYS[1], 'status', 'confirmed')
redis.call('ZREM', KEYS[2], ARGV[1])
return 1`
	result, err := s.redis.Do("EVAL", script, "2", holdKey(hold.ID), confirmKey(), hold.ID)
	if err != nil {
		return Booking{}, err
	}
	if result != int64(1) {
		return Booking{}, ErrHoldInactive
	}
	return booking, nil
}

func (s *Service) GetHold(id string) (Hold, error) {
	result, err := s.redis.Do("HGETALL", holdKey(id))
	if err != nil {
		return Hold{}, err
	}
	values, ok := result.([]any)
	if !ok || len(values) == 0 {
		return Hold{}, ErrHoldMissing
	}
	fields := map[string]string{}
	for i := 0; i+1 < len(values); i += 2 {
		key, keyOK := values[i].(string)
		value, valueOK := values[i+1].(string)
		if keyOK && valueOK {
			fields[key] = value
		}
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, fields["expires_at"])
	if err != nil {
		return Hold{}, err
	}
	hold := Hold{ID: fields["id"], Slot: fields["slot"], VisitorID: fields["visitor_id"], Status: fields["status"], ExpiresAt: expiresAt}
	if confirmedAt := fields["confirmed_at"]; confirmedAt != "" {
		hold.ConfirmedAt, err = time.Parse(time.RFC3339Nano, confirmedAt)
		if err != nil {
			return Hold{}, err
		}
	}
	return hold, nil
}

// ReleaseExpiredHolds is idempotent. It is safe for multiple workers to run.
func (s *Service) ReleaseExpiredHolds() (int, error) {
	now := strconv.FormatInt(s.now().UnixMilli(), 10)
	result, err := s.redis.Do("ZRANGEBYSCORE", timerKey(), "-inf", now)
	if err != nil {
		return 0, err
	}
	ids, ok := result.([]any)
	if !ok {
		return 0, fmt.Errorf("unexpected Redis timer response: %T", result)
	}
	released := 0
	for _, raw := range ids {
		id, ok := raw.(string)
		if !ok {
			continue
		}
		script := `
local slot = redis.call('HGET', KEYS[1], 'slot')
if redis.call('HGET', KEYS[1], 'status') ~= 'held' then redis.call('ZREM', KEYS[2], ARGV[1]); return 0 end
redis.call('HSET', KEYS[1], 'status', 'expired')
redis.call('DECR', 'ttd:queue:slot:' .. slot .. ':used')
redis.call('ZREM', KEYS[2], ARGV[1])
redis.call('EXPIRE', KEYS[1], 86400)
return 1`
		value, err := s.redis.Do("EVAL", script, "2", holdKey(id), timerKey(), id)
		if err != nil {
			return released, err
		}
		if value == int64(1) {
			released++
		}
	}
	return released, nil
}

func (s *Service) StartExpiryWorker(stop <-chan struct{}, interval time.Duration) {
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.RecoverPendingConfirmations()
				_, _ = s.ReleaseExpiredHolds() // next tick safely retries transient Redis failures
			case <-stop:
				return
			}
		}
	}()
}

// RecoverPendingConfirmations completes a booking that was durable but whose
// Redis state was not finalized before the process stopped.
func (s *Service) RecoverPendingConfirmations() {
	result, err := s.redis.Do("ZRANGE", confirmKey(), "0", "99")
	if err != nil {
		return
	}
	ids, ok := result.([]any)
	if !ok {
		return
	}
	for _, raw := range ids {
		id, ok := raw.(string)
		if !ok {
			continue
		}
		hold, err := s.GetHold(id)
		if err == nil && hold.Status == StatusConfirming {
			_, _ = s.persistAndFinalize(hold)
		}
	}
}

func randomID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}
