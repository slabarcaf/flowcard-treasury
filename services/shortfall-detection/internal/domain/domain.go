// Package domain defines normalized shortfall observations and inbox states.
package domain

// InboxState represents durable processing progress, separate from an outcome.
type InboxState string

// Inbox states preserve failed-attempt counts across crashes and explicit replay.
const (
	Received   InboxState = "RECEIVED"
	Processing InboxState = "PROCESSING"
	RetryWait  InboxState = "RETRY_WAIT"
	Processed  InboxState = "PROCESSED"
	Held       InboxState = "HELD"
)

// Outcome classifies an immutable observation, never an authorization to fund.
type Outcome string

// Observation outcomes distinguish zero current need from missing evidence.
const (
	ShortfallDetected  Outcome = "SHORTFALL_DETECTED"
	NoCurrentShortfall Outcome = "NO_CURRENT_SHORTFALL"
	Ignored            Outcome = "IGNORED"
	Unresolved         Outcome = "UNRESOLVED"
)
