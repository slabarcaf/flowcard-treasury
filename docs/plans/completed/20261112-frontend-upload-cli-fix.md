# Frontend Upload CLI Fix

The high-level AWS CLI S3 copy and sync commands reject the expected bucket
owner argument used by the frontend release script.

- [x] Use S3 API object uploads with ownership checks on every request.
- [x] Preserve content types, cache headers, nested files, and index-last order.
- [x] Run executable upload and rollback regression tests, including failure
      before invalidation when an object upload fails.
- [x] Retry the reviewed frontend release after local checks pass.

Do not remove ownership checks, publish public ACLs, or change AWS resources
during local verification.

Verification: 121 focused workflow, upload, and rollback tests passed. Shell
syntax and diff checks passed. The installed AWS CLI accepted the PutObject
arguments in input-skeleton mode, which makes no service request. Tests also
cover nested filenames with spaces, per-object MIME/cache headers, ownership
checks, and stopping before HTML publication or invalidation on upload failure.
