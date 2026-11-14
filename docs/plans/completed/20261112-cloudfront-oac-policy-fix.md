# CloudFront OAC Policy Fix

The frontend module omitted the distribution ARN. Its fallback used a wildcard
string with an exact comparison, so CloudFront could not read private assets.

- [x] Pass the distribution output to the frontend bucket policy.
- [x] Remove wildcard fallback and omit read grants when CloudFront is disabled.
- [x] Verify exact-source policy behavior and configuration offline.
- [x] Review and apply the bucket-policy-only plan, then retry the site.

Keep public access blocks and HTTPS enforcement enabled. No secret values or
application-data changes are required.

Verification: three mocked-provider OpenTofu tests and 101 focused Node tests
passed. Demo configuration validation passed. The graph-export command could
not initialize the live backend because this execution context has no usable
AWS credentials. Ramil subsequently reviewed the one-change policy plan, applied
it, and confirmed that the frontend loads.
