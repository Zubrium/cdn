empty :=
space := $(empty) $(empty)
comma := ,
MODULES := core coordinator engines/website engines/content engines/forms engines/loader engines/router workers/archive workers/media workers/prerender presentation/router presentation/admin/sites presentation/admin/files presentation/admin/main presentation/admin/ui presentation/admin/content presentation/admin/forms presentation/admin/router presentation/admin/coordinator cmd/zubrium
PACKAGES := $(addsuffix /...,$(addprefix ./,$(MODULES)))
COVER_PACKAGES := $(subst $(space),$(comma),$(strip $(PACKAGES)))

.PHONY: generate-profiles build build-full test coverage vet legacy-build
generate-profiles:
	cd coordinator && go run ./cmd/profilegen -profile ../profiles/minimal.yaml -out ../cmd/zubrium/profile_core_generated.go -tag '!full'
	cd coordinator && go run ./cmd/profilegen -profile ../profiles/full.yaml -out ../cmd/zubrium/profile_full_generated.go -tag full

build: generate-profiles
	go build -o /dev/null ./cmd/zubrium

build-full: generate-profiles
	go build -tags=full -o /dev/null ./cmd/zubrium

test:
	@set -eu; overlay=$$(mktemp); trap 'rm -f "$$overlay"' EXIT; python3 tests/go-overlay.py > "$$overlay"; for module in $(MODULES); do (cd "$$module" && go test -overlay="$$overlay" ./...); done

coverage:
	@test -n "$$TEST_CORE_DATABASE_URL" -a -n "$$TEST_CORE_BOOTSTRAP_DATABASE_URL" -a -n "$$TEST_CORE_MANAGER_RACE_DATABASE_URL" -a -n "$$TEST_CORE_CREDENTIAL_RACE_DATABASE_URL" -a -n "$$TEST_CORE_GROUPS_DATABASE_URL" -a -n "$$TEST_CORE_SCHEMA_DATABASE_URL" -a -n "$$TEST_CORE_PLATFORM_DATABASE_URL" -a -n "$$TEST_CORE_PLATFORM_MIGRATION_DATABASE_URL" -a -n "$$TEST_CORE_PLATFORM_ADMIN_DATABASE_URL" -a -n "$$TEST_CORE_RUNTIME_ROLE_DATABASE_URL" -a -n "$$TEST_CORE_RUNTIME_ROLE_MIGRATION_DATABASE_URL" || (echo "Configure every fresh disposable Core database and migration/runtime login before measuring coverage"; exit 1)
	@set -eu; tmp=$$(mktemp -d); trap 'rm -r "$$tmp"' EXIT; \
	python3 tests/go-overlay.py > "$$tmp/overlay.json"; \
	go test -overlay="$$tmp/overlay.json" -tags=full -count=1 -covermode=atomic -coverpkg=$(COVER_PACKAGES) -coverprofile="$$tmp/all.out" $(PACKAGES); \
	go tool cover -func="$$tmp/all.out" | tee "$$tmp/summary"; \
	awk '/^total:/ {gsub("%", "", $$3); found=1; if ($$3+0 < 70) exit 1} END {if (!found) exit 1}' "$$tmp/summary"

vet:
	@set -eu; overlay=$$(mktemp); trap 'rm -f "$$overlay"' EXIT; python3 tests/go-overlay.py > "$$overlay"; for module in $(MODULES); do (cd "$$module" && go vet -overlay="$$overlay" ./...); done

legacy-build:
	cd legacy && GOWORK=off go build ./cmd/api
