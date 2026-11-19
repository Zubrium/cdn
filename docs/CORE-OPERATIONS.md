# Core deployment and database operations

Core schema DDL lives in the single Atlas migration directory [`core/infrastructure/postgres/migrations`](../core/infrastructure/postgres/migrations). Go code performs data operations only and invokes the pinned Atlas CLI `v1.3.0` through the migration identity.

## PostgreSQL identities

Use a new, empty PostgreSQL database for the target Core schema. The migration login and runtime login must be separate identities pointing to the same server, port and database. Pass credentials through the configured environment variables; do not store credentials in profile YAML or example configs.

Before migration, an operator provisions a `NOLOGIN` grant role named `zubrium_core_runtime` and a non-superuser runtime login that is a member of that role. The Atlas baseline grants the role `USAGE` on Core schemas and only `SELECT`, `INSERT`, `UPDATE` and `DELETE` on the enumerated Core tables, plus `USAGE` on the engine-data revision sequence. It receives no DDL, Atlas history, `TRUNCATE` or unrelated schema access. Keep role creation in deployment/IaC; application code does not create PostgreSQL roles.

The migration login must be allowed to create the Core schemas, objects and Atlas history. Do not reuse it for runtime queries or grant `CREATEROLE` to the runtime login. Runtime validation rejects superuser/elevated memberships and privileges beyond the approved Core DML boundary.

## Atlas baseline

There is one initial baseline under `migrations/`; there is no bootstrap `schema-migrations/` directory. It creates `access`, `files`, `uploads`, `file_index`, `engine_data` and `coordination`, with defaults and constraints in the fresh schema. `atlas.sum` belongs beside the migration. The runtime startup rejects unknown or unmanaged objects before applying the baseline and never resets or converts an existing database.

Go sources must contain no schema `CREATE`, `ALTER`, `DROP` or `GRANT` statements. Atlas SQL is the only source for tables, indexes, constraints, schemas, sequence and grants. Runtime queries name schemas explicitly; the pool search path is restricted to `pg_catalog`.

One PostgreSQL database represents one logical system. `coordination.runtime` stores one non-secret fingerprint and epoch. All replicas must use matching runtime/build configuration. A changed fingerprint cannot join while old instance leases remain active; stop all instances before switching the YAML/build profile.

Access group keys are declared in `core.access_groups`. Initial bootstrap assignments apply only when the administrator is first created; subsequent edits made through the Admin API persist across restart. A removed YAML group is inactive and denies access without deleting prior grants or file memberships; restoring the group restores its saved assignments.

## Capacity and object storage

`max_cached_object_bytes` must fit within `cache_max_bytes`. The runtime pool keeps one connection for PostgreSQL invalidation notifications and reserves two more per configured concurrent streaming upload commit for staged reads and final object writes. The separate migration pool requires at least two connections.

Core stores private S3 objects under generated UUID keys. The bucket must have all public-access-block settings enabled, no public ACL/policy, and versioning disabled. Startup fails closed if it cannot verify these requirements. Core performs streaming single `PutObject`; `max_object_bytes` must not exceed 5,000,000,000 bytes. Public reads are an explicit Router capability; they do not make the bucket or Core file endpoint public.

## HTTP transport

The configured application listener serves HTTP and does not terminate TLS. The example binds to loopback. Non-local deployments must place it on a trusted private network behind an externally managed TLS-terminating proxy, require TLS from clients to that proxy and never treat forwarded headers as authentication evidence.

The public Router listener has no built-in request-rate or IP-based limiter. Do not expose it directly to the internet: the deployer is responsible for placing it behind an external proxy and configuring the required admission controls there. The application neither chooses nor configures that proxy. The Router listener address is an explicit deployment setting and is not restricted to loopback by application validation.

## Disposable verification

Each PostgreSQL integration variable must point to a newly created disposable database with a task-specific name under `zubrium_core_qa_*`, provisioned with a distinct runtime and migration login. S3 tests use a loopback mock unless a separately owned private bucket under `zubrium-core-qa-*` is explicitly configured. Before cleanup, verify each exact database/bucket and confirm it has no active users; remove only resources recorded for that QA run.
