MODULES := core engines/website engines/content engines/forms engines/loader presentation/router presentation/fileapi presentation/adminapi presentation/adminui cmd/zubrium
PACKAGES := ./core/... ./cmd/zubrium/... ./engines/website/... ./engines/content/... ./engines/forms/... ./engines/loader/... ./presentation/router/... ./presentation/fileapi/... ./presentation/adminapi/... ./presentation/adminui/...
COVER_PACKAGES := ./core/...,./cmd/zubrium/...,./engines/website/...,./engines/content/...,./engines/forms/...,./engines/loader/...,./presentation/router/...,./presentation/fileapi/...,./presentation/adminapi/...,./presentation/adminui/...

.PHONY: build build-full test coverage vet legacy-build
build:
	go build -o /dev/null ./cmd/zubrium

build-full:
	go build -tags=full -o /dev/null ./cmd/zubrium

test:
	@set -e; for module in $(MODULES); do (cd "$$module" && go test ./...); done

coverage:
	@test -n "$$TEST_CORE_DATABASE_URL" -a -n "$$TEST_CORE_BOOTSTRAP_DATABASE_URL" -a -n "$$TEST_CORE_MANAGER_RACE_DATABASE_URL" -a -n "$$TEST_CORE_CREDENTIAL_RACE_DATABASE_URL" -a -n "$$TEST_CORE_GROUPS_DATABASE_URL" -a -n "$$TEST_CORE_SCHEMA_DATABASE_URL" -a -n "$$TEST_CORE_PLATFORM_DATABASE_URL" -a -n "$$TEST_CORE_PLATFORM_MIGRATION_DATABASE_URL" -a -n "$$TEST_CORE_PLATFORM_ADMIN_DATABASE_URL" -a -n "$$TEST_CORE_RUNTIME_ROLE_DATABASE_URL" -a -n "$$TEST_CORE_RUNTIME_ROLE_MIGRATION_DATABASE_URL" || (echo "Configure every fresh disposable Core database and migration/runtime login before measuring coverage"; exit 1)
	@set -eu; tmp=$$(mktemp -d); trap 'rm -r "$$tmp"' EXIT; \
	go test -tags=full -count=1 -covermode=atomic -coverpkg=$(COVER_PACKAGES) -coverprofile="$$tmp/all.out" $(PACKAGES); \
	go tool cover -func="$$tmp/all.out" | tee "$$tmp/summary"; \
	awk '/^total:/ {gsub("%", "", $$3); found=1; if ($$3+0 < 70) exit 1} END {if (!found) exit 1}' "$$tmp/summary"

vet:
	@set -e; for module in $(MODULES); do (cd "$$module" && go vet ./...); done

legacy-build:
	cd legacy && GOWORK=off go build ./cmd/api
