# Frontend-only onboarding integration

Confirmed scope: do not change Ramil's backend. Do not push without Yidi's
explicit instruction. Keep all server/services source identical to team develop.

1. Retain the completed forms and verified Airwallex industry dictionary.
2. Map form data to the existing company/application/document API contracts.
3. Connect to a running backend via the development proxy with existing auth.
4. Display real returned states and explicit errors. Do not relabel DRAFT as
   submitted.
5. List missing customer auth, required write fields, intake transition and
   internal review actions for Ramil. Leave their controls unavailable until
   real endpoints exist.
6. Validate builds and frontend integration tests, and verify the final
   changed-file list contains no server/services changes.

## Outcome and supersession

The frontend integration scope was delivered and later expanded under PR #9.
Ramil explicitly approved the Go API and durable submission work, superseding
the original “no server/services changes” boundary and no-push instruction.
The UI uses existing operator-session auth; customer authentication was not
added. Submission remains subject to provider readiness gates. Information
requests and internal rejections remain unsupported. See the [completed
onboarding API integration plan](20261113-onboarding-api-integration.md) for
authenticated browser evidence and remaining provider, storage, and deployment
limits.
