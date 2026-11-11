---
worth: yes
added: 2026-10-22
---

# Airwallex risk events and immediate policy effects

The bridge-ledger plan covers webhook verification, deduplication, transfer and
charge status, and reconciliation. Separately define which account, RFI,
deposit, direct-debit, dispute, and card-decline events must bypass the daily
underwriting-profile refresh, what immediate customer state each event
produces, and how later events clear or supersede that state. The Airwallex
data inventory describes event-driven re-scoring, while the policy plan
currently relies on daily profiles; settle this before enabling production
decisions.
