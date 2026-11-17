# Acceptance coverage for the Coordinator architecture

This is a traceability list, not a claim of passing coverage. Tests live under
`tests/go` and use the repository's Go overlay. Legacy tests are historical
and do not prove the new architecture. The architecture review lists unresolved
contracts; scenarios depending on those contracts cannot yet be made executable
without choosing unapproved API or runtime behavior.

The user-defined target is at least 60% of agreed acceptance input sets for
each module. Every input set must include a specified precondition, action,
expected outcome, and relevant state assertions. An input set counts as covered only when a runnable test exercises
the real behavior against an implementation or an actual external boundary;
an interface, a scenario description, or a test of a fake does not qualify.
Red acceptance tests count as authored scenarios but not as passing behavior.
The denominator must be agreed and recorded before reporting a percentage,
must include missing or pending input sets, not only currently implemented
code, and must not be inflated with repetitive trivial variants. It has not
yet been enumerated for every module; no 60% claim is justified yet.

| Area | Existing executable tests | Remaining acceptance work |
| --- | --- | --- |
| Core files, groups, identity, cache | `core/platform/core_v2_contract_test.go`, `core/internal/storage`, `core/internal/postgres/direct_groups_test.go` | Real S3, multi-instance failures and public-schema baseline; run disposable integration suite |
| Core migrations | `tests/go/core/internal/postgres/{public_schema_acceptance,target_schema}_test.go` | Clean Atlas baseline must create all current Core tables under `core`, keep application objects out of `public`, and pass Atlas checksums; run fresh disposable PostgreSQL migration acceptance |
| Module data | `core/internal/postgres/module_data_test.go`, `core/platform/module_data_test.go` | Prove queue claim, lease and atomic publication capabilities or implement another contract |
| Loader and File API | `engines/loader/features/files/application`, `presentation/admin/files` | Actual public form attachment binding and restart/recovery with shared PostgreSQL/S3 |
| Coordinator | `tests/go/coordinator/{build_profile,catalog_profile,runtime,instance,workers,jobs,job_control,publication,service}_test.go`, `tests/go/presentation/admin/coordinator/handler_test.go`, `tests/go/cmd/zubrium/config/coordinator_profile_test.go`; disposable cases in `tests/go/core/internal/postgres/coordinator_test.go`; frontend API coverage in `tests/frontend/admin/src/api.test.ts` | Unit/API scenarios and four disposable PostgreSQL persistence scenarios pass. Browser mock QA covered list, failed-status filter, retry visibility and action, and 390px layout. Remaining integration: real Website/Content triggers, Archive/Media/Prerender handlers, Router service, provider multi-site update, and live authenticated Admin API against PostgreSQL. These are outside the delivered Coordinator subsystem and do not count as covered. |
| Router | `engines/router/presentation` contract tests | File/artifact-only target validation; host/path isolation, authorization and revision invalidation. Router v1 rejects handler targets by specification |
| Website | `engines/website/presentation/contract_test.go` | Durable ZIP import, Core FileSet creation, cleanup/retry, exact pins and new ABI |
| Content | `engines/content/presentation/contract_test.go` | Draft/publish concurrency, locales, fallback, external browser requests and media failure |
| Forms | `engines/forms/presentation/contract_test.go` | Anonymous sessions, upload binding/resume, expiry, schema change cancellation, delete/recovery |
| Archive | `workers/archive/inspect_test.go` | Core file creation, inheritance of groups, durable retries and cleanup |
| Media, Prerender | contract scaffolds only | Real processing, isolation, two render stages, deterministic inputs and failure handling |
| Admin/public UI | `tests/frontend/admin/src` targets existing copied UI | New provider recovery, direct group permissions, route dispatch and isolated browser acceptance |

Router handler-target acceptance is outside the approved Router v1 contract.
`TestRouteTargetRejectsUntrustedFileLocations` retains the restriction on
arbitrary file URLs, and handler targets are rejected alongside them.

Archive inspection acceptance also runs real ZIP inputs through
`TestInspectRejectsArchiveWithDamagedContent`,
`TestInspectRejectsEntryCountAndPerFileLimit`,
`TestInspectReturnsDigestOfUncompressedBytes`, and
`TestInspectRejectsExcessiveCompressionRatio`. These cover inspection only,
not the unimplemented Core-backed extraction and recovery workflow.
`TestInspectPortablePathInputs` exercises ten independently listed valid and
invalid paths. It rejects Windows drive-relative and drive-absolute paths
(`C:secret.txt`, `C:/secret.txt`, and lowercase drive prefixes); the regression
was reproduced before the fix and passes afterward.
