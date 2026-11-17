# Core deployment and database operations

Core schema objects are created only by the versioned Atlas SQL files under [`core/internal/postgres`](../core/internal/postgres). Production Go code contains data queries and delegates schema changes to the pinned Atlas CLI; it does not execute schema DDL. Atlas CLI and the embedded Atlas migration library are pinned to `v1.3.0`.

## PostgreSQL users

Use a new, empty database for Core v2. The migration login and application login must be different PostgreSQL users and must target the same PostgreSQL host, port, and database name. Startup rejects URLs that point to different servers even if their database names match. Keep both credentials in a secret manager and pass them through the environment variables named by `database_url_env` and `migration_database_url_env`.

Before the first migration, the database operator provisions a `NOLOGIN` group role named `zubrium_core_runtime` and a runtime login that is a member of that role. The initial Atlas migration grants this role `USAGE` on `core` and only `SELECT`, `INSERT`, `UPDATE`, and `DELETE` on Core application tables. It receives no access to Atlas revision tables, DDL, `TRUNCATE`, or other module schemas. Keep future table grants explicit in each migration.

The migration login must be able to create the `core` schema and Atlas history tables and must be authorized to grant Core table privileges to `zubrium_core_runtime`; it must own or have migration rights on the Core schema. Do not reuse this login for runtime queries. The runtime login must be a direct member only of the `NOLOGIN` `zubrium_core_runtime` grant role. That grant role must not have role memberships or elevated attributes. `Start` checks effective schema/table privileges, role memberships and attributes before it bootstraps the first account; membership in a `CREATEDB`, migration or other elevated role is rejected even when those privileges are not currently inherited.

Role provisioning is PostgreSQL cluster administration, outside the Core-owned schema and outside application code. Apply it through the deployment's database/IaC process; do not grant `CREATEROLE` to the Core migration or runtime login, and do not add memberships to the runtime grant role.

## Atlas migration layout

- `schema-migrations/` creates the isolated `core` schema. Its one-time Atlas history is stored in `public.atlas_schema_revisions` because the target schema does not exist until this migration runs.
- `migrations/` creates all Core tables and indexes in `core`. Its independent Atlas history is stored in `core.atlas_schema_revisions`.
- The bootstrap Atlas environment connects with the migration URL's ordinary search path and records its history in `public`. The Core Atlas environment receives the same migration login with `search_path=core`, so Atlas scopes its schema inspection and revision history to Core instead of treating the bootstrap history as an unmanaged Core change.
- Both directories include `atlas.sum`, are embedded into the Core binary, and are staged with private temporary file permissions before Atlas runs. The application rejects unmanaged objects and incomplete Core baselines; it never drops or converts an existing schema.
- The initial migration is a clean baseline with table defaults and constraints declared inline. Do not add compatibility `ALTER TABLE` migrations for the retired model.

Schema DDL belongs in Atlas SQL. Go may insert the first administrator and reconcile data against configured groups after the migration succeeds. The migration and runtime DB users are required even when the schema is already current, because startup verifies the migration state and runtime privilege boundary.

`core.access_groups` lists static group keys. The initial administrator receives level `2` for a group when its value is omitted or `null`; explicitly configured levels `0`, `1`, and `2` override that initial assignment. These values apply only the first time a group is registered. Later administrator-managed assignments survive restarts and removing/re-adding a previously known group restores its saved assignments.

The configured `max_cached_object_bytes` must not exceed `cache_max_bytes`. This prevents the read-through path from loading an object into memory that cannot fit within the configured byte budget.

The runtime PostgreSQL pool requires at least three connections. One remains available to `LISTEN` for invalidations; each concurrent Loader commit reserves two more while it streams staged parts into the final object upload. Set `loader.max_concurrent_commits` explicitly; startup requires `core.max_connections >= 1 + 2 * max_concurrent_commits`, preventing nested connection acquisition from deadlocking under parallel commits. The separate migration pool has its own two-connection minimum.

## S3 requirements

Core stores private objects under generated UUID keys. The bucket must have all four public-access-block settings enabled, no public ACL or policy, and versioning disabled so permanent deletion removes bytes. Startup fails closed if any setting cannot be checked. Core uses one streaming `PutObject`; configure `max_object_bytes` at or below 5,000,000,000 bytes (5 GB), the S3 single-PUT ceiling. Public file delivery is a Router concern and never makes the Core bucket public.

## HTTP transport

The configured application listener serves plain HTTP; it does not terminate TLS. The example binds to loopback. For any non-local deployment, keep this listener on a trusted private network behind a TLS-terminating gateway or reverse proxy, and require TLS on the client-to-gateway connection. Do not expose the listener to untrusted networks or send bearer credentials over plaintext HTTP. The application does not use forwarded headers as an authentication or authorization signal.

## Disposable verification resources

Integration test databases must be newly created and named `zubrium_core_qa_*`; provision `zubrium_core_runtime` plus a separate migration login before running the Core migrations. The runtime DSN must use a non-superuser member of `zubrium_core_runtime`. By default, S3 integration uses an in-memory, loopback-only S3 protocol server. To check a real S3-compatible endpoint, supply all `TEST_CORE_S3_*` or `TEST_CORE_PLATFORM_S3_*` values; its bucket must be named `zubrium-core-qa-*`, private, non-versioned, and have all four public-access-block settings enabled. Confirm each exact database and bucket is task-owned before removing it after QA.
