# Zubrium: modular platform

The experimental `dev_v2` branch contains a clean Go workspace with separate modules for Core, engines, and Presentation. The legacy product remains under `legacy/`. The platform is not yet a replacement for the legacy service: Router, Loader, Admin API, Admin UI, and the migrated Website, Content, and Forms engines are still incomplete.

Core now owns a clean PostgreSQL `core` schema, immutable private files, static access groups, direct per-user group levels, credentials and administrator-management permission, file sets, S3 access, and local byte/metadata caching. Its database schema is created only by Atlas SQL migrations; Go code performs data operations and invokes the pinned Atlas CLI. See [the Core specification](docs/SPEC-core-v2.md), [modular architecture](docs/SPEC-modular-platform.md), and [deployment requirements](docs/CORE-OPERATIONS.md).

The first administrator is created only after Atlas succeeds. If no bootstrap secret is configured, a generated credential is printed once to stderr; restrict process-log access. `--recover-admin config.yaml` is an offline operator command and must not be exposed as a public service route. Configure distinct runtime and migration PostgreSQL users; the runtime login must be a member of `zubrium_core_runtime` and have no schema DDL or cross-module access.

`make build` builds the Core-only profile; `make build-full` includes optional module contracts. `make test`, `make vet`, and `make coverage` cover the new Go workspace; coverage must reach 70%. Integration tests require fresh disposable PostgreSQL databases named `zubrium_core_qa_*` and S3 buckets named `zubrium-core-qa-*`. Never point them at working data. See [the Core coverage and test matrix](docs/CORE-TEST-COVERAGE.md).
