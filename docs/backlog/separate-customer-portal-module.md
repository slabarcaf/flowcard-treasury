---
worth: yes
added: 2026-11-13
---
# Move the customer portal into a separate module

Move the customer-facing portal out of `src/features/bridge-capital/portal/`
into its own top-level frontend module, separate from the Bridge Capital
back-office code. Decide the destination and shared-contract boundaries, then
update routing, imports, and focused tests.
