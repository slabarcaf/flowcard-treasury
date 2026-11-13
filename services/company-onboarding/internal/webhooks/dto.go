package webhooks

import "encoding/json"

// InboundWebhookPayload represents the standard webhook envelope received from the platform forwarder.
type InboundWebhookPayload struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	AccountID    string          `json:"accountId,omitempty"`
	AccountIDAlt string          `json:"account_id,omitempty"`
	Data         json.RawMessage `json:"data,omitempty"`
	CreatedAt    string          `json:"created_at,omitempty"`
	Environment  string          `json:"environment,omitempty"`
}

// ExtractAccountID resolves the target platform account ID from top-level or data envelope fields.
func (p InboundWebhookPayload) ExtractAccountID() string {
	if p.AccountID != "" {
		return p.AccountID
	}
	if p.AccountIDAlt != "" {
		return p.AccountIDAlt
	}
	if len(p.Data) > 0 {
		var accountData InboundWebhookAccountData
		if err := json.Unmarshal(p.Data, &accountData); err == nil {
			if accountData.ID != "" {
				return accountData.ID
			}
			if accountData.AccountID != "" {
				return accountData.AccountID
			}
			if accountData.AccountDetails != nil && accountData.AccountDetails.ID != "" {
				return accountData.AccountDetails.ID
			}
		}
	}
	return ""
}

// InboundWebhookAccountData captures nested account attributes inside the webhook data payload.
type InboundWebhookAccountData struct {
	ID             string                 `json:"id,omitempty"`
	AccountID      string                 `json:"accountId,omitempty"`
	Status         string                 `json:"status,omitempty"`
	AccountDetails *InboundAccountDetails `json:"account_details,omitempty"`
}

// InboundAccountDetails captures deep account_details object attributes.
type InboundAccountDetails struct {
	ID     string `json:"id,omitempty"`
	Status string `json:"status,omitempty"`
}

// WebhookAcknowledgeResponse is returned upon durable persistence.
type WebhookAcknowledgeResponse struct {
	Received bool   `json:"received"`
	EventID  string `json:"eventId,omitempty"`
}
