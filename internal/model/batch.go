package model

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
)

// Batch is the retry-safe transport unit between a collector and the core.
// Its ID is derived from queue item identities, so an interrupted request can
// be repeated without creating a new logical delivery.
type Batch struct {
	ID               string        `json:"id"`
	DeviceID         string        `json:"device_id"`
	CollectorVersion string        `json:"collector_version"`
	Observations     []Observation `json:"observations"`
}

func (b *Batch) Normalize() error {
	if strings.TrimSpace(b.ID) == "" {
		return errors.New("batch id is required")
	}
	if strings.TrimSpace(b.DeviceID) == "" {
		return errors.New("batch device_id is required")
	}
	if len(b.Observations) == 0 {
		return errors.New("batch observations cannot be empty")
	}
	for index := range b.Observations {
		if err := b.Observations[index].Normalize(); err != nil {
			return err
		}
		if b.Observations[index].DeviceID != b.DeviceID {
			return errors.New("every observation must have the batch device_id")
		}
	}
	return nil
}

// DeliveryBatchID returns the same ID for the same set of claimed queue rows,
// irrespective of claim order or a replacement lease after a crash.
func DeliveryBatchID(items []string) (string, error) {
	if len(items) == 0 {
		return "", errors.New("cannot create an empty delivery batch")
	}
	ids := append([]string(nil), items...)
	sort.Strings(ids)
	hash := sha256.New()
	for _, id := range ids {
		if strings.TrimSpace(id) == "" {
			return "", errors.New("queue item ID is required")
		}
		_, _ = hash.Write([]byte(id))
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
