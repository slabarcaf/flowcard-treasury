package domain

import "time"

// Job is one durable inbox claim. A crash does not increment FailedAttempts.
type Job struct {
	ID             string
	LeaseToken     string `json:"-"`
	LeaseExpiresAt time.Time
	FailedAttempts int
	Delivery       Delivery `json:"-"`
}
