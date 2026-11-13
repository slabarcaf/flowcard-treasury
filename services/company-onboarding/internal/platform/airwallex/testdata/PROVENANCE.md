# Airwallex Testdata Fixtures Provenance Manifest

This manifest documents the documentation sources, date consulted, version limitations, schema basis, synthetic substitutions, and verification status for all test fixtures in this directory.

---

## 1. Environment & API Versioning Limitations

- **Date Read:** 2026-11-06
- **Base URLs:**
  - Production: `https://api.airwallex.com/api/v1/`
  - Sandbox: `https://api.sandbox.airwallex.com/api/v1/` (confirmed host; `api-demo` rejected as unconfirmed)
  - Files: `https://files.airwallex.com/api/v1/` (Sandbox: `https://files.sandbox.airwallex.com/api/v1/`)
- **API Version Status:** **PROVISIONAL**.
  - Airwallex uses date-based versioning (`x-api-version: YYYY-MM-DD`).
  - Existing FlowCard codebase specifies `2026-08-21` for issuing simulations (`AIRWALLEX_ISSUING_API_VERSION` in `server/src/config.ts`).
  - The exact date version required for Connected Accounts native onboarding in sandbox has not been confirmed via live Docs MCP or sandbox schema response. `2024-06-01` was withdrawn as unsupported.

---

## 2. Webhook Payload Shape & Naming Basis

Official documentation (`https://www.airwallex.com/docs/developer-tools/webhooks/listen-for-webhook-events/account`) shows account webhook payloads using a camelCase top-level wrapper with `data.accountDetails` reflecting the *Get account by ID* response:

```jsonc
{
  "accountId": "acct_TOslkS7-MnyXlQuV8Lb4og",
  "data": {
    "accountDetails": {
      "authorisedPersonDetails": {
        "attachments": {
          "identityFiles": [
            {
              "description": "string"
            }
          ]
        }
      }
    }
  },
  "id": "86b69f91-06f0-3fe8-8faf-4de58954ca8e",
  "name": "account.active"
}
```

*Status:* **PROVISIONAL REFERENCE STRUCTURE**. The top-level envelope keys (`accountId`, `name`, `id`, `data`) and `accountDetails` wrapper are documented in official webhook guides, but the exact inner schema for onboarding status fields has not been recorded from live webhooks. It must not be treated as canonical until confirmed.

---

## 3. Fixture Inventory & Provenance

| Fixture File | Exact Source URL | Schema Basis & Limitations | Synthetic Substitutions | Classification |
| :--- | :--- | :--- | :--- | :--- |
| `account_create_request.json` | `https://www.airwallex.com/docs/api/scale/accounts/create` | Native API onboarding payload containing `account_details.business_details`, `primary_contact`, and `customer_agreements`. | Synthetic business "Acme Innovations LLC", synthetic EIN "12-3456789", synthetic person "Alex Morgan", synthetic IP/User-Agent. | **Verified Shape** (No `accepting_person_id` in platform schema) |
| `account_create_response.json` | `https://www.airwallex.com/docs/api/scale/accounts/create` | 200/201 response returning account identifier with initial status `CREATED`. | Replaced live account ID with synthetic `acct_sandbox_test001`. | **Verified Shape** |
| `account_submit_response.json` | `https://www.airwallex.com/docs/api/scale/accounts/submit` | Response to `POST /api/v1/accounts/{id}/submit` confirming immediate transition to `SUBMITTED`. | Replaced live ID with `acct_sandbox_test001`. | **Verified Shape** |
| `file_upload_response.json` | `https://www.airwallex.com/docs/developer-tools/files/upload-file` | File Service `POST /api/v1/files/upload` returning `file_id`, `filename`, `size`. | Replaced live UUID with `file_sandbox_test001`. | **Verified Shape** |
| `webhook_account_submitted.json` | `https://www.airwallex.com/docs/developer-tools/webhooks/listen-for-webhook-events/account` | Account status notification for `account.submitted`. | Event UUID `evt_sandbox_sub001`, account `acct_sandbox_test001`. | **Provisional Reference Shape** |
| `webhook_account_active.json` | `https://www.airwallex.com/docs/developer-tools/webhooks/listen-for-webhook-events/account` | Account status notification for `account.active`. Indicates full account activation. | Event UUID `evt_sandbox_act001`, account `acct_sandbox_test001`. | **Provisional Reference Shape** |
| `webhook_account_action_required.json` | `https://www.airwallex.com/docs/developer-tools/webhooks/listen-for-webhook-events/account` | Account status notification for `account.action_required`. Signals that further action is needed on the account (terms, missing info, or RFI). | Event UUID `evt_sandbox_actreq001`, account `acct_sandbox_test001`. | **Provisional Reference Shape** (Inner requirement schema provisional) |
| `webhook_account_suspended.json` | `https://www.airwallex.com/docs/developer-tools/webhooks/listen-for-webhook-events/account` | Account status notification for `account.suspended`. Signals account verification hold or suspension. | Event UUID `evt_sandbox_susp001`, account `acct_sandbox_test001`. | **Provisional Reference Shape** |
| `error_invalid_field_400.json` | `https://www.airwallex.com/docs/developer-tools/errors/overview` | Structured 400 Bad Request error with lowercase snake_case `code` (`invalid_argument`), `source`, and `message`. | Synthetic field path and message. | **Synthetic Transport Placeholder** (Not verified platform code enum) |
| `error_rate_limit_429.json` | `https://www.airwallex.com/docs/developer-tools/errors/overview` | Structured 429 Too Many Requests response with `code: "rate_limit_exceeded"` and `message`. | Synthetic message. | **Synthetic Transport Placeholder** (Not verified platform code enum) |
| `error_server_error_500.json` | `https://www.airwallex.com/docs/developer-tools/errors/overview` | Structured 500 Internal Server Error response with `code: "service_unavailable"` and `message`. | Synthetic message. | **Synthetic Transport Placeholder** (Not verified platform code enum) |

---

## 4. Unresolved Evidence & Limitations

1. **Error Codes:** Error fixtures represent synthetic transport failure placeholders based on the documented `{"code", "source", "message"}` structure. They do NOT represent a complete or verified platform error code catalog. Tests assert generic string types for `code` and `message` rather than rigid uppercase enums.
2. **Conditional Webhook Events (`account.dormant`, `account.closed`):** Documented in Airwallex guides as available only from version `2026-08-21`. Classified as **UNRESOLVED / CONDITIONAL** and omitted from mandatory fixtures until the onboarding version is confirmed.
3. **Inner Webhook Detail Schema:** Exact schema for `data.requirements` or `data.accountDetails` during RFI has not been captured from live sandbox events.
4. **Sanitization Heuristic:** Key checks in `fixtures_test.go` perform a limited structural scan against common secret key names; this is a heuristic scan and does not constitute proof of zero PII or credentials.

---

## 5. Verified Lead Docs MCP RFI Contract (Native KYC RFI API)

- **Date Consulted:** 2026-11-08
- **Primary Source Guides & API References:**
  - `https://www.airwallex.com/docs/connected-accounts/handle-rfis/handle-kyc-rfi/native-kyc-rfi-api.md`
  - `https://www.airwallex.com/docs/api/risk/request_for_information_rfi/retrieve.md`
  - `https://www.airwallex.com/docs/api/risk/request_for_information_rfi/respond_rfis.md`
- **Verified Operations & Shapes:**
  - `GET /api/v1/rfis/{platformRfiId}` with Bearer token.
    - Schema: `{ "id": string, "account_id": string, "type": "KYC", "status": "ACTION_REQUIRED", "active_request": { "questions": [{ "id": string, "answer": { "type": "TEXT" } }] }, "answered_requests": [...] }`.
    - Mandatory Verification Gates: Account ID must match mapped connected external account ID (`PlatformAccount.ExternalAccountID()`); RFI status must be `ACTION_REQUIRED`; RFI type must be `KYC`; all active questions must have type `TEXT` and must have matching frozen answers before dispatch.
  - `POST /api/v1/rfis/{platformRfiId}/respond` with Bearer token and JSON body:
    - Schema: `{ "questions": [{ "id": "platformQuestionID", "answer": { "text": "frozen answer" } }] }`.
    - Generic `POST /api/v1/accounts/{id}/submit` is strictly forbidden for RFI responses.
  - Documented Operation Errors:
    - `invalid_state_for_operation`: RFI is not in `ACTION_REQUIRED` status.
    - `account_not_authorised_for_operation`: caller or target account is unauthorized.
    - `resource_not_found`: target RFI ID does not exist.
    - `invalid_argument`: malformed payload or unanswered questions.
- **Unresolved Evidence / Explicit Gates:**
  - **Success Response Schema/Status:** Not verified in Docs MCP references. The client accepts HTTP 200/201/204 as transport success without asserting unverified inner status fields (such as `ACCEPTED` or `RESOLVED`).
  - **Documentary Type RFIs:** Not covered by native text KYC flow; documentary RFIs remain gated for manual handling.
