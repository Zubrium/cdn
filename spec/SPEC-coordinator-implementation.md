# Coordinator: implementation contract

Status: Coordinator lifecycle, Core persistence boundary, profile-bound task
catalog, delayed worker-pool activation, durable scheduling, publication
fencing, and the authenticated job list/retry Admin API are implemented in the
current worktree. Coordinator package, API, frontend, PostgreSQL persistence,
build, and race checks have been run; detailed evidence and limitations are in
[QA_REPORT.md](../docs/QA_REPORT.md). Product-wide engine triggers, production
worker handlers, and provider/site orchestration remain separate integration
work. This document describes the implemented contract and marks the remaining
boundaries explicitly. Product decisions live in
[SPEC-coordinator-platform.md](SPEC-coordinator-platform.md).

## Ownership and package boundaries

- `coordinator/` owns compile-profile validation, runtime composition, instance
  lifecycle, worker orchestration, retry policy, scoped job control, and
  publication scheduling.
- Core owns durable instance leases, jobs, publication generations, and active
  route targets. Coordinator uses typed capabilities from `core/platform`; it
  does not issue SQL, DDL, or S3 operations.
- Workers receive only a typed job and a handler context. They use injected
  engine/Core contracts; worker handlers must not import another engine or
  reach Core persistence directly.
- Router receives a separate `RouterTargetStore` capability for target reads
  and explicit revision-checked changes. Coordinator has no general Router
  mutation capability; publication activation is a single narrow Core command.
- PostgreSQL schema changes belong to the clean Atlas baseline. Go code issues
  data operations only.

## Build profile and composition

The profile is one YAML document with `version: 1` and explicit lists for
`engines`, `workers`, `apis.admin`, `apis.public`, and `automation`. The parser
rejects unknown or duplicate fields, aliases/merge keys, multiple documents,
unknown or duplicate component IDs, missing lists, and missing dependencies.
Only the fixed component registry can map an ID to a Go import; a profile can
never supply an import path or Go expression. Resolution is deterministic and
dependency-first.

`profilegen` emits deterministic Go source with imports for the selected
compile-time modules, selected engine descriptor registration, and the
canonical `BuildPlan`. Generated files are `cmd/zubrium/profile_core_generated.go`
and `profile_full_generated.go`, created by `make generate-profiles`. The
generator rejects multiline build-tag input so it cannot escape the build
constraint comment. It emits the selected plan; it does not invent missing
runtime factories.

At startup, `Compose` checks that every selected runtime factory is registered,
that its dependencies exist and precede it, and that the static plan is valid
before starting any component. It starts components in dependency order, checks
readiness after startup, and then activates components that perform background
work. The single WorkerPool starts only after every selected engine and API has
passed startup readiness, so it cannot claim jobs while dependencies are
starting. Composition rolls back partial starts in reverse order, monitors
fatal component errors, and shuts down in reverse order. Shutdown is bounded by
the configured timeout when components honor their stop context. Factory,
startup, readiness, failure-source, and shutdown panics are converted to a
sanitized component failure; shutdown continues through earlier dependencies
after a stop panic. Typed-nil factory results and nil failure streams are
rejected. A readiness-probe panic or an unexpectedly closed failure stream
marks the runtime unready. Missing factories fail startup closed before side effects. The
current application registers runtime factories only for Core,
Coordinator, Loader, and File API; other profile modules remain compile-time
scaffolds and a profile selecting them cannot start until their factories are
implemented. The minimal profile now mounts Coordinator's Admin API on the
shared File API listener.

## Configuration and instance leases

Runtime YAML requires explicit Coordinator values: `system_id`, job access
group, job-list page limits, instance lease, heartbeat interval, worker
concurrency, job lease, task timeout, poll interval, retry base/maximum,
maximum attempts, and a bindings list. The access group must also exist in
Core's static `access_groups`. A binding includes
site/content/route UUIDs and an explicit `auto_publish` boolean. Duplicate route
bindings and invalid IDs/timing relationships fail configuration loading.
The Core database pool must reserve at least one connection for Core, two per
concurrent Loader commit, and two per configured Coordinator worker (handler
work plus lease renewal); an undersized pool is rejected before startup.
Binding UUIDs are canonicalized and sorted before fingerprinting so equivalent
UUID spellings and list order yield the same epoch identity. The canonical set
of compiled component IDs is also included, so binaries with different build
profiles cannot join the same live Coordinator system. Secrets stay outside
the YAML and are excluded from diagnostics and the configuration fingerprint;
normalized database endpoint identity and non-secret runtime configuration are
included.
Before Core opens, the resolved configuration is compared with the compiled
build plan: publication bindings require `automation.site-publication`, Website,
Content, Router, and Prerender; selecting publication automation requires at
least one binding. A minimal profile with no publication bindings remains valid.

Core stores one active fingerprint and monotonically increasing configuration
epoch per system. Instance registration and fingerprint transitions serialize
on the database row. A differing fingerprint is rejected while any instance
lease is live. Once all instances have stopped, expired, or been released, a
new fingerprint advances the epoch and cancels queued/running old-epoch jobs.
Operators therefore stop all replicas, deploy the same YAML, then restart them.

Database time is authoritative. Operations validate the active epoch and lease
inside Core transactions and hold shared locks on the config/instance rows for
the transaction. Operations which claim, enqueue, schedule, or retry work check
the instance lease again before commit. Heartbeat failure clears readiness and
emits a normalized fatal error. API readiness is false until Core, Coordinator,
and every selected component are ready; File API traffic and `/readyz` return
`503`, `Cache-Control: no-store`, and `Retry-After: 1` until the composition is
ready or after any selected component loses readiness.

Graceful instance close stops its heartbeat and attempts to release the lease
using a detached, bounded cleanup context. A timed-out caller may stop waiting
while cleanup finishes; if cleanup cannot reach PostgreSQL, the lease expires
and the next registration uses database state to recover safely.

## Durable jobs and workers

Core validates task types, idempotency keys, UTF-8 payload/result object JSON, and
retry bounds. Reusing `(system, type, idempotency key)` with the same payload
and attempt limit returns the existing job; differing contents return a
conflict. Claim uses a transaction and `FOR UPDATE SKIP LOCKED`; each claim
increments attempts and the fencing token and records the configuration epoch,
owner, and database-clock lease.
The publication job type is reserved: generic enqueue rejects it, and the
publication command accepts only that type, requires `payload.route_id` to
match the transaction's route, and injects the generation itself. Callers
cannot create a publication job without the generation/fencing workflow.

An expired job can be reclaimed with a larger fencing token while attempts
remain. The final expired attempt becomes failed. Lease extension, completion,
and failure require the current system/instance epoch, owner, fencing token,
running state, and unexpired lease. Error details from handlers are not stored;
only a bounded machine error code is persisted. Retry delay is bounded,
exponential, and stable per job/attempt. Manual retry resets the failed job
through the Core capability. Before retrying a publication job, Core verifies
that the job still belongs to the current desired route generation; stale
failed publication jobs remain terminal. An authenticated operator API and job
list UI are not yet wired.

Composition creates one immutable `TaskCatalog` from all selected worker
handlers. `BuildTaskCatalog` canonicalizes the profile, requires exactly one
handler provider for each selected worker, rejects providers for unselected
workers and duplicate task types, and admits the reserved publication handler
only when publication automation is selected. A profile with no workers or
automation has no catalog. It gives each engine adapter a `JobScheduler` capability derived
from that catalog's explicit type subset, so the engine can queue only its
own tasks and no task without a compiled handler.
The capability binds each request to the current system, instance ID,
configuration epoch, and configured attempt limit, creates the job UUID, and
validates a bounded JSON-object payload and a clean idempotency key. A receipt
exposes only the job ID, state, and whether this call created it; task payloads
must contain opaque references rather than credentials or secrets. Publication
tasks are excluded from this generic capability.

One `WorkerPool` consumes the complete immutable task catalog, claims one job
per worker, and enforces system-wide `WorkerConcurrency`. It renews leases,
limits execution with `TaskTimeout`, contains handler
panics, validates result size/shape, and marks the component unhealthy on a
durable-store failure, including lease-renewal storage errors. A confirmed
lost job lease is an expected ownership change; a lost Coordinator instance
lease is fatal. A fatal pool error cancels sibling workers. Handlers
must return when their context is cancelled: Go cannot safely terminate an
uncooperative goroutine. External/hostile execution (for example Chromium) must
use a separately supervised process with OS resource limits rather than rely on
this in-process timeout.

Coordinator can list jobs using a stable `(created_at, job_id)` cursor and
optional type/state filters, with a page size from 1 through 200. The list
returns a bounded summary only; it omits job payload and result. `GetJob`
remains an internal detail capability. A narrow `JobControl` capability binds
list/retry to one system and the active instance epoch; it cannot claim work or
read payload/result details. Core checks the Coordinator owner, current epoch,
and unexpired instance lease in the same transaction as each list query, while
holding shared configuration/instance locks. A stale capability cannot list
summaries after lease loss or a configuration transition. The Core
authorization capability resolves the bearer credential and rechecks direct
group access from PostgreSQL on every request; it carries neither user
administration nor file/storage capabilities. It does not authenticate or
authorize HTTP callers by itself. The Admin API exposes
`GET /api/v2/coordinator/jobs` and
`POST /api/v2/coordinator/jobs/{jobID}/retry`: level 1 on the configured group
can list summaries, and level 2 can retry. `GET
/api/v2/coordinator/jobs/permissions` reports whether the same credential has
retry access, but still requires read access. Administrator-management
privilege does not bypass this group check. Responses omit system scope,
payload, result, and internal error details. Only failed jobs can be retried;
a publication job whose generation is no longer desired remains terminal. The
API is mounted on the shared File API listener when `admin.coordinator` is
selected. The Admin SPA provides a paginated job table with state/type filters
and renders retry only when the server confirms group level 2; server-side
authorization remains authoritative. The UI and API are implemented but still
require final frontend and isolated PostgreSQL verification.

The API contract is:

- `GET /api/v2/coordinator/jobs?type=&state=&limit=&before=` accepts only those
  optional, single-valued query parameters. `state` is one of `queued`,
  `running`, `succeeded`, `failed`, or `cancelled`. `before` is an opaque
  canonical URL-safe cursor for `(created_at, job_id)`. A `200` response has
  `{items, next_cursor}`; each item contains `id`, `type`, `state`, `attempts`,
  `max_attempts`, `available_at`, optional `lease_until`, optional `error_code`,
  `created_at`, and `updated_at`. Unknown/repeated query keys, malformed
  cursors, and invalid limits return `400` without reaching Core.
- `GET /api/v2/coordinator/jobs/permissions` requires read access and returns
  `{can_retry: boolean}`. A valid reader without write access gets
  `{can_retry:false}`; authentication or Core availability errors remain
  `401`/`503`.
- `POST /api/v2/coordinator/jobs/{jobID}/retry` has no request body and returns
  `204` after Core accepts retry. Only `failed` jobs are retryable. Invalid
  IDs/bodies return `400`, missing jobs `404`, stale/terminal jobs `409`, and
  authorization failures `401`/`403`.

All API responses set `Cache-Control: no-store` and
`X-Content-Type-Options: nosniff`. Job page default and maximum are explicit
Coordinator YAML values (`jobs_page_default`, `jobs_page_maximum`) with a hard
maximum of 200. `job_access_group` must name a statically configured Core group;
the example profile uses `coordinator_jobs`.

## Publication scheduling and route activation

`PublicationScheduler` snapshots validated route bindings from startup YAML.
Trigger callers pass only a configured route ID and immutable snapshot/content
revisions and provider version; they cannot substitute site/content IDs, a base
artifact ID, or an `auto_publish` flag. The handler always asks Website to
build or resolve the site base for the configured site and snapshot. Disabled automatic bindings reject
ordinary publication. A separate Coordinator-only rebuild operation supports
explicit refresh flows and still requires a configured route; an Admin API must
authorize before exposing such an operation. Its fingerprint includes the
configuration epoch and all publication inputs. Repeated identical requests
share one generation and durable job.

Core changes desired generation, cancels queued/running work for older
generations on that route, and inserts the publication job in one transaction.
The job receives the generation in its payload and an internal stable
idempotency key. A manual Router target replacement uses expected
revision, validates the replacement file is ready, updates the target, and
advances desired publication generation in one transaction. Consequently a
build already in flight becomes stale; scheduling the same inputs after the
manual change creates a fresh generation/job.

The publication handler is constructed from the same configured bindings and
rejects a payload whose route/site/content tuple is not configured. It validates
the system, job type, owner/fencing tuple, payload IDs, revisions, and generation
before calling the builder. The Website builder receives the configured site
ID when it checks the snapshot. It checks cancellation between build stages.
Builder stages use stable keys derived from
the job ID so at-least-once retries can reconcile side effects. A build failure
does not change the active route. Activation is a Core transaction which locks
the desired generation, job lease, ready artifact, and target; the route UPDATE
itself checks the live instance/job lease, epoch, and fencing token. A stale
generation or expired worker cannot replace the current target. Repeating an
already committed activation is idempotent. A successful target change emits a
PostgreSQL notification.

Core's route invalidation listener sends individual route changes and a
wildcard full-reload marker after startup/reconnect or buffer overflow. Router
must reload active targets on the wildcard and must not serve a cached target
after an invalidation until its reload succeeds. The target-store capability is
issued only when Router is registered and only once per Core runtime.

## Integration gaps and acceptance

The following remain outside the implemented Coordinator boundary and must not
be represented as finished product flows:

- Website/Content publish events do not yet call the scheduler; their current
  modules are contracts/scaffolds. The coordinator API is ready for an injected
  immutable publication request.
- No real Archive, Media, or Prerender handler is registered in the running
  application. WorkerPool is implemented, but handlers/process isolation and
  their Core adapters remain module work.
- Router's typed target capability and invalidations exist, but a complete
  Router Engine/Presentation service is not yet wired to them.
- The authenticated job API and Admin SPA are wired. Handler tests cover
  current group authorization, safe summaries, and retry boundaries; browser QA
  verified list/filter/retry visibility against an intercepted mock API. The
  endpoint has not yet been exercised end-to-end against live PostgreSQL and a
  real authenticated Admin session. Do not retry jobs against shared product
  data.
- The provider recovery workflow (compatibility checks, shared provider switch,
  per-site child snapshots, and partial multi-site retry) is not implemented.
- The full build profile contains modules without runtime factories and must
  fail closed at startup; `build-full` alone is only a compile check.

Coordinator tests live under `tests/go/coordinator`, Core persistence tests
under `tests/go/core/internal/postgres`, and application integration tests under
`tests/go/cmd/zubrium`; the supported runner maps them into their Go packages
with `tests/go-overlay.py`. PostgreSQL scenarios require the explicitly named
disposable test URLs. The implemented Coordinator acceptance suite covers profile
rejection/determinism, startup rollback/order/readiness/shutdown,
instance/config fencing, idempotent enqueue, claim/reclaim/fencing, retry
limits, publication generation/manual override, stale-result rejection, and
invalidation recovery. Unit, isolated PostgreSQL, and mocked-browser evidence
are recorded in the QA report. Process-isolated production worker handlers and
live Admin API plus PostgreSQL authorization remain unverified integration
boundaries; do not infer them from unit tests or a generated build plan.
