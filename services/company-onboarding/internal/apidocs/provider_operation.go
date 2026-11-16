package apidocs

import (
	"net/http"
	"reflect"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/webhooks"
)

func providerWebhookOperation() OperationDefinition {
	responses := map[int]ResponseDefinition{http.StatusOK: {Description: "Original receipt or unchanged duplicate committed durably", Type: reflect.TypeOf(webhooks.WebhookAcknowledgeResponse{}), ContentTypes: []string{"application/json"}}}
	for status, description := range map[int]string{400: "Invalid object, fields, scope or timestamp", 401: "Missing/invalid signature or timestamp too far ahead", 408: "Request body timed out", 409: "Changed event ID payload stored as conflict evidence; unfinished work quarantined", 410: "Event belongs to another service", 413: "Body exceeds inclusive 10 MiB limit", 500: "Receipt could not be committed", 503: "Intake secret/scope or repository unconfigured"} {
		responses[status] = ResponseDefinition{Description: description, ContentTypes: []string{"application/json"}, SchemaBuilder: func() *openapi3.SchemaRef {
			return &openapi3.SchemaRef{Ref: "#/components/schemas/WebhookErrorResponse"}
		}}
	}
	return OperationDefinition{
		ID: "receiveProviderOnboardingWebhook", Audience: "provider-webhook", Method: http.MethodPost, Path: "/webhooks/airwallex",
		Summary:     "Receive signed provider account/RFI events",
		Description: "HMAC-SHA256 over the exact x-timestamp string followed by original request bytes. One timestamp/signature header each. Maximum body 10 MiB, future-only five-minute timestamp tolerance; old timestamps accepted. Body id/name required, duplicate JSON keys rejected. Server owns sandbox/production scope. Acknowledgement follows durable commit. No internal JWT substitutes for provider authentication.",
		Tags:        []string{"Webhooks"},
		Parameters:  []ParameterDefinition{{Name: "x-timestamp", In: "header", Required: true, Description: "Unix milliseconds, as originally signed", Schema: &openapi3.Schema{Type: &openapi3.Types{"string"}, Pattern: `^[0-9]+$`, MinLength: 1, MaxLength: func() *uint64 { v := uint64(19); return &v }()}}},
		RequestBody: &RequestBodyDefinition{Required: true, ContentType: "application/json", Type: reflect.TypeOf(webhooks.InboundWebhookPayload{}), Description: "Original provider JSON object; account.* or rfi.* only; no event-ID header fallback"},
		Responses:   responses, Security: []map[string][]string{{"ProviderSignature": {}}},
	}
}
