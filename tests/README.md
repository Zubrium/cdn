# Tests

`go/` mirrors Go package paths from the repository root. These tests remain in
their original Go packages through `go test -overlay`; this preserves access to
package-private symbols and `internal` imports without production-only exports.
Use `make test`, `make vet`, or `make coverage` from the repository root. A
plain `go test ./...` does not include the centralized tests.

`frontend/admin/src/` mirrors the admin source tree. The admin Vitest config
discovers these files; run them with `pnpm test` from `frontend/`.

The historical `legacy/` project keeps its own unchanged tests and commands.
Integration tests still require their explicitly configured disposable services.
