// Package id generates monotonic ULIDs so `ORDER BY id` reproduces send order.
package id

import (
	"crypto/rand"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
)

// entropy is shared across callers so concurrent ids stay ordered within a millisecond.
var entropy ulid.MonotonicReader = &ulid.LockedMonotonicReader{
	MonotonicReader: ulid.Monotonic(rand.Reader, 0),
}

// New returns a fresh, strictly increasing ULID string. Safe for concurrent use.
func New() (string, error) {
	id, err := ulid.New(ulid.Timestamp(time.Now()), entropy)
	if err != nil {
		return "", fmt.Errorf("id: %w", err)
	}
	return id.String(), nil
}
