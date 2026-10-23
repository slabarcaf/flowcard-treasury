---
worth: yes
added: 2026-10-21
---

# Airwallex webhook status updates

Use Airwallex webhooks as the primary way to update transfer and charge status
in the bridge ledger. Verify and deduplicate events, correlate them with
operations by `request_id` and Airwallex object ID, and append status
observations to the ledger.

Keep API lookup or polling as recovery for missed, delayed, duplicate, or
ambiguous events. Hold transfer reservations while status is `NEW`, `PENDING`,
or `SUSPENDED`; promote principal only on `SETTLED`, and release a reservation
only after `FAILED` confirms the transfer was returned. Reduce fee or principal
owed only when the corresponding charge is `SETTLED`.

The current frontend uses mocked services; implement this when the backend
Airwallex integration is taken up.
