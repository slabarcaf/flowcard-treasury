// Package agent defines the bounded, case-scoped investigation protocol.
package agent

import (
	"context"
	"encoding/json"
	"time"

	customercontext "github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/context"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/servicing"
	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/sources"
)

const (
	MaxModelCalls      = 12
	MaxToolCalls       = 12
	NormalMaxTokens    = 1500
	FinishMaxTokens    = 3000
	MaxRequestBytes    = 48 << 10
	FinishReserveBytes = 8 << 10
)

type Kind string

const (
	Shortfall  Kind = "SHORTFALL"
	Collection Kind = "COLLECTION"
)

type Rule struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Reason string `json:"reason"`
}

type SavedAssessment struct {
	ID                string `json:"id"`
	Status            string `json:"status"`
	ShortfallID       string `json:"shortfall_id"`
	TermDays          int    `json:"term_days"`
	ApprovalRequired  *bool  `json:"approval_required"`
	RuleReasons       []Rule `json:"rule_reasons"`
	FundingAuthorized bool   `json:"funding_authorized"`
	RequiresRecheck   bool   `json:"requires_recheck"`
}

type SourceRef struct {
	Handle    string `json:"handle"`
	SectionID string `json:"section_id"`
	Locator   string `json:"locator"`
}

type Packet struct {
	CaseID          string
	SourceBindingID string
	RunID           string
	Kind            Kind
	Mode            string
	CustomerID      string
	AccountRef      string
	ShortfallID     string
	Assessment      *SavedAssessment
	Context         *customercontext.Response
	// PrivateContextData retains the full normalized context outside model JSON.
	PrivateContextData    json.RawMessage `json:"-"`
	Rules                 []Rule
	RequiredContextFields []string
	SavedFacts            json.RawMessage
	EffectiveActions      []string
	Sources               []SourceRef
	SourceReader          *sources.Reader
	Bridge                *servicing.Bridge
	AllowedClaimFields    []string
	PromptVersion         string
	ModelVersion          string
	AgentMode             string
}

type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type Message struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type Request struct {
	Model           string    `json:"model"`
	MaxTokens       int       `json:"max_tokens"`
	System          string    `json:"system"`
	Messages        []Message `json:"messages"`
	Tools           []Tool    `json:"tools"`
	ToolChoice      any       `json:"tool_choice,omitempty"`
	DisableParallel bool      `json:"-"`
}

type ContentBlock struct {
	Type  string          `json:"type"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	Text  string          `json:"text,omitempty"`
}

type Response struct {
	ID         string            `json:"id"`
	Type       string            `json:"type"`
	Role       string            `json:"role"`
	Model      string            `json:"model"`
	Content    []json.RawMessage `json:"content"`
	StopReason string            `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

type Finding struct {
	RuleID      string      `json:"rule_id"`
	EvidenceIDs []string    `json:"evidence_ids,omitempty"`
	Sources     []SourceRef `json:"sources,omitempty"`
	Explanation string      `json:"explanation"`
}

type Finish struct {
	EvaluationID         *string   `json:"evaluation_id"`
	Summary              string    `json:"summary"`
	Findings             []Finding `json:"findings"`
	MissingInformation   []string  `json:"missing_information"`
	Conflicts            []string  `json:"conflicts"`
	RecommendedNextSteps []string  `json:"recommended_next_steps"`
}

type StepResult struct {
	Continue   bool
	Finished   bool
	Finish     *Finish
	Limitation string
	RetryAt    *time.Time
}

// ProviderFailureKind separates uncertain transport outcomes from known failures.
type ProviderFailureKind string

const (
	// ProviderAmbiguous means the request may have reached the provider.
	ProviderAmbiguous ProviderFailureKind = "AMBIGUOUS"
	// ProviderTransient means the provider returned a retryable status.
	ProviderTransient ProviderFailureKind = "TRANSIENT"
	// ProviderPermanent means configuration or response validation must stop retries.
	ProviderPermanent ProviderFailureKind = "PERMANENT"
)

// ProviderError is a credential-safe provider failure with bounded retry metadata.
type ProviderError struct {
	Kind       ProviderFailureKind
	Code       string
	RetryAfter time.Duration
}

func (e *ProviderError) Error() string { return e.Code }

type Provider interface {
	CreateMessage(context.Context, Request) (Response, error)
}

type ToolError struct{ Code string }

func (e *ToolError) Error() string { return e.Code }
