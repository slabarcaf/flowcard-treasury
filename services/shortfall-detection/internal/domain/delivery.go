package domain

// Delivery contains authenticated original bytes and server-owned inbox scope.
// RawBody is protected evidence and must never enter logs or read API responses.
type Delivery struct {
	Environment string
	Source      string
	WebhookID   string
	EventName   string
	RawBody     []byte `json:"-"`
}
