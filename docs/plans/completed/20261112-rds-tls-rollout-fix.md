# RDS TLS Rollout Fix

The Node migration image lacks the Amazon RDS CA bundle. Its connection
fails certificate verification before any migration runs.

## Image correction

- [x] Add the public AWS RDS CA bundle to both runtime images at
      `/etc/ssl/certs/rds-global-bundle.pem`.
- [x] Build Linux amd64 images and verify the bundle is readable by each
      non-root runtime user and usable by the Node PostgreSQL client.

## Controlled rollout

- [x] Publish new immutable image digests and review task-definition updates
      while application services remain at zero tasks.
- [x] Update only TLS query parameters in the existing database secrets to
      `sslmode=verify-full` and the bundled `sslrootcert` path. Preserve all
      passwords, role names, database names, and endpoints.
- [x] Retry Node and Go migrations, inspect their exit status, and retain all
      existing submission and platform policy gates.

Secret values stay out of code, logs, shell arguments, and state. No certificate
verification bypass or additional runtime environment variable is introduced.
