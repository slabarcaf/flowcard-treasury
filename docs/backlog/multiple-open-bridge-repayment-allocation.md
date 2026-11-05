---
worth: later
added: 2026-10-21
---

# Repayment allocation for multiple open bridges

The current demo assumes one open bridge per customer. Before supporting
multiple advances, decide how a settled deposit is allocated across per-bridge
fees and principal, and which principal is repaid first. Current defaults use
`fee-<transfer id>` and `repay-<deposit id>`; define how those IDs map if one
deposit repays several bridges. The Transfer Module proposed oldest-first
principal allocation, but the current source of truth does not settle the rule.
Revisit when multiple open bridges per customer enter implementation scope.
