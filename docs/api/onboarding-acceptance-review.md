# Onboarding acceptance review

Initial review for the open onboarding plan, November 15, 2026. This matrix
separates delivered local behavior from provider documentation and actual
account capability. No provider request or account creation was performed.

[TOC]

## Current matrix

| Area | Local evidence | External evidence or remaining gate | Next action |
| --- | --- | --- | --- |
| Company/application CRUD | Go DTOs, UI serializers and saved-draft/person PATCH journey implemented | This does not create an activated provider account | Repeat through real operator login and the Go gateway |
| Submission revisions | Latest locked Company/Application are revalidated and frozen; application revision checked | Earlier revision-policy deferral is superseded | Preserve semantics and same-key retries through gateway |
| US business payload | Supported profile/person/contact/document mapping implemented | Complete entity/role/document matrix and account capability still require verification | Record field-by-field official sources before changing forms |
| Agreements | Sandbox omission of uncollected agreements is implemented; partial/non-sandbox records gated | Real acceptance metadata must not be invented; activation requirements remain | Keep real consent collection backlogged |
| Text RFI | Adapter/worker and local contract coverage exist | Official native RFI guide requires platform-account enablement | Verify enablement before provider execution; retain unsupported/manual channels |
| Documents | Local bounds and private storage interface exist | Actual storage permissions, provider-specific document matrix and end-to-end upload acceptance remain | Focused local storage test, then separately scoped sandbox verification |
| Uncertain create | Durable operation and manual hold behavior implemented | Safe provider lookup/absence guarantee remains unverified | Keep no-blind-retry behavior |
| User/customer ownership | Operator API trusts a signed server-side subject | Onboarding company ID is not a servicing customer ID | Operator onboarding first; trusted customer binding before financial commands |

## Sources checked

No connected Airwallex docs tool is exposed in this session. Official public
pages were read as the fallback:

- [Create account API](https://www.airwallex.com/docs/api/scale/accounts/create):
  account schema and terms-acceptance metadata. The large multi-endpoint page
  does not by itself establish the complete current US entity/role matrix.
- [Native onboarding RFI guide](https://www.airwallex.com/docs/connected-accounts/handle-rfis/handle-kyc-rfi/native-kyc-rfi-api):
  enablement requirement and text-answer flow. No broader document-response
  channel or account enablement is inferred from the example.
- Local `internal/platform/airwallex/testdata/PROVENANCE.md` retains previously
  checked shapes and unresolved provider-specific assumptions.

## US conditional-rule trace

The [US requirements](https://www.airwallex.com/docs/connected-accounts/onboarding/kyc-and-onboarding/native-api/business-kyc-requirements/united-states-%28us%29)
were checked against `internal/validation/us_connected_account.go` and the
Airwallex mapper. The four-state secondary/photo rule is already implemented;
the earlier two-ID selection check alone does not describe all validation.
This review justifies neither validator relaxation nor a new second-ID
requirement.

Local role coverage, operating address, EIN, products/revenue and person-data
checks exist. This is not full provider acceptance: the country guide includes
entity-dependent identifiers and non-registered sole traders, while the local
gate requires EIN. That broader subset needs an explicit compatibility decision.

The US minimum-activation guide requires agreements. API optionality at creation
must not be treated as proof that activation accepts omission. The existing
sandbox omission remains a local hackathon choice, with real collection
deferred.

## First shared implementation slice

Use real OPERATOR accounts and server sessions in the new Go gateway. Forward
only explicit onboarding routes with server-generated subject assertions.
CUSTOMER accounts remain denied operator onboarding routes. Preserve document
streaming, ID linkage, revision guards and uncertain submission receipts.

The local acceptance journey uses synthetic Company/People data and owned
test resources. Actual provider writes, credentials/capability checks and live
callbacks are outside this review and need a concrete execution scope.
