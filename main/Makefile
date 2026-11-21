empty :=
space := $(empty) $(empty)
comma := ,
ROOT := $(abspath $(dir $(lastword $(MAKEFILE_LIST)))/..)
MODULES := core engines/website engines/content engines/forms engines/loader engines/router workers/archive workers/media workers/prerender presentation/router presentation/admin/sites presentation/admin/files presentation/admin/ui presentation/admin/content presentation/admin/forms presentation/admin/router presentation/admin/base pkg main
PACKAGES := $(addsuffix /...,$(addprefix ./,$(MODULES)))
COVER_PACKAGES := $(subst $(space),$(comma),$(strip $(PACKAGES)))

.PHONY: generate-profiles build build-full build-router build-dev-fixtures test test-router test-dev-fixtures coverage vet lint lint-core-boundary
generate-profiles:
	cd "$(ROOT)" && cd main && go run ./internal/buildprofile/cmd/profilegen -profile profiles/minimal.yaml -out internal/composition/profile_core_generated.go -package composition -tag '!full && !devfixtures && !router'
	cd "$(ROOT)" && cd main && go run ./internal/buildprofile/cmd/profilegen -profile profiles/full.yaml -out internal/composition/profile_full_generated.go -package composition -tag 'full && !devfixtures && !router'
	cd "$(ROOT)" && cd main && go run ./internal/buildprofile/cmd/profilegen -profile profiles/development-fixtures.yaml -out internal/composition/profile_devfixtures_generated.go -package composition -tag 'devfixtures && !full && !router'
	cd "$(ROOT)" && cd main && go run ./internal/buildprofile/cmd/profilegen -profile profiles/router.yaml -out internal/composition/profile_router_generated.go -package composition -tag 'router && !full && !devfixtures'

build: generate-profiles
	cd "$(ROOT)" && go build -o /dev/null ./main/cmd/zubrium
	cd "$(ROOT)" && go build -tags=router -o /dev/null ./main/cmd/zubrium

build-full: generate-profiles
	cd "$(ROOT)" && go build -tags=full -o /dev/null ./main/cmd/zubrium

build-router: generate-profiles
	cd "$(ROOT)" && go build -tags=router -o /dev/null ./main/cmd/zubrium

build-dev-fixtures: generate-profiles
	cd "$(ROOT)" && go build -tags=devfixtures -o /dev/null ./main/cmd/zubrium

test-dev-fixtures: generate-profiles
	@set -eu; cd "$(ROOT)"; overlay=$$(mktemp); trap 'rm -f "$$overlay"' EXIT; python3 tests/go-overlay.py > "$$overlay"; (cd main && go test -count=1 -tags=devfixtures -overlay="$$overlay" ./...)

test:
	@set -eu; cd "$(ROOT)"; overlay=$$(mktemp); trap 'rm -f "$$overlay"' EXIT; python3 tests/go-overlay.py > "$$overlay"; for module in $(MODULES); do (cd "$$module" && go test -count=1 -overlay="$$overlay" ./...); done; (cd main && go test -count=1 -tags=router -overlay="$$overlay" ./...)

test-router:
	@set -eu; cd "$(ROOT)"; overlay=$$(mktemp); trap 'rm -f "$$overlay"' EXIT; python3 tests/go-overlay.py > "$$overlay"; (cd main && go test -count=1 -tags=router -overlay="$$overlay" ./...)

coverage:
	@test -n "$$TEST_ATLAS_BINARY" -a -n "$$TEST_CORE_DATABASE_URL" -a -n "$$TEST_CORE_BOOTSTRAP_DATABASE_URL" -a -n "$$TEST_CORE_MANAGER_RACE_DATABASE_URL" -a -n "$$TEST_CORE_CREDENTIAL_RACE_DATABASE_URL" -a -n "$$TEST_CORE_GROUPS_DATABASE_URL" -a -n "$$TEST_CORE_SCHEMA_DATABASE_URL" -a -n "$$TEST_CORE_DIRTY_DATABASE_URL" -a -n "$$TEST_CORE_ENGINE_DATA_DATABASE_URL" -a -n "$$TEST_CORE_PLATFORM_DATABASE_URL" -a -n "$$TEST_CORE_PLATFORM_MIGRATION_DATABASE_URL" -a -n "$$TEST_CORE_PLATFORM_ADMIN_DATABASE_URL" -a -n "$$TEST_CORE_RUNTIME_ROLE_DATABASE_URL" -a -n "$$TEST_CORE_RUNTIME_ROLE_MIGRATION_DATABASE_URL" || (echo "Configure Atlas v1.3.0 and every fresh disposable Core database and migration/runtime login before measuring coverage"; exit 1)
	@test -n "$$TEST_COORDINATOR_DATABASE_URL" -a -n "$$TEST_COORDINATOR_WORKFLOW_DATABASE_URL" -a -n "$$TEST_COORDINATOR_WORKFLOW_RACE_DATABASE_URL" -a -n "$$TEST_COORDINATOR_CHILDREN_DATABASE_URL" -a -n "$$TEST_COORDINATOR_CANCEL_DATABASE_URL" -a -n "$$TEST_COORDINATOR_STALE_CAPABILITY_DATABASE_URL" -a -n "$$TEST_COORDINATOR_RETRY_DATABASE_URL" -a -n "$$TEST_COORDINATOR_JOIN_DATABASE_URL" -a -n "$$TEST_COORDINATOR_LIFECYCLE_DATABASE_URL" -a -n "$$TEST_COORDINATOR_DELAYED_DATABASE_URL" -a -n "$$TEST_COORDINATOR_RESTART_DATABASE_URL" -a -n "$$TEST_COORDINATOR_RESTART_PENDING_DATABASE_URL" -a -n "$$TEST_COORDINATOR_TAKEOVER_DATABASE_URL" -a -n "$$TEST_COORDINATOR_RETENTION_DATABASE_URL" -a -n "$$TEST_COORDINATOR_DEPENDENCY_DATABASE_URL" -a -n "$$TEST_COORDINATOR_DEPENDENCY_WAIT_DATABASE_URL" -a -n "$$TEST_COORDINATOR_EXHAUSTED_DATABASE_URL" || (echo "Configure every fresh disposable Coordinator database before measuring coverage"; exit 1)
	@set -eu; cd "$(ROOT)"; tmp=$$(mktemp -d); trap 'rm -r "$$tmp"' EXIT; \
	python3 tests/go-overlay.py > "$$tmp/overlay.json"; \
	go test -overlay="$$tmp/overlay.json" -tags=full -count=1 -covermode=atomic -coverpkg=$(COVER_PACKAGES) -coverprofile="$$tmp/all.out" $(PACKAGES); \
	go tool cover -func="$$tmp/all.out" | tee "$$tmp/summary"; \
	awk '/^total:/ {gsub("%", "", $$3); found=1; if ($$3+0 < 70) exit 1} END {if (!found) exit 1}' "$$tmp/summary"

vet: lint-core-boundary
	@set -eu; cd "$(ROOT)"; overlay=$$(mktemp); trap 'rm -f "$$overlay"' EXIT; python3 tests/go-overlay.py > "$$overlay"; for module in $(MODULES); do (cd "$$module" && go vet -overlay="$$overlay" ./...); done

lint: lint-core-boundary

lint-core-boundary:
	cd "$(ROOT)" && GOWORK=off GO111MODULE=off go run ./tests/linter/lint_core_boundary.go
