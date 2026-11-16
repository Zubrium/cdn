.PHONY: build test test-integration fmt vet pull compose-up compose-down

build:
	go build ./...

fmt:
	gofmt -w ./cmd ./features ./presentation ./pkg ./migrations

vet:
	go vet ./...

test:
	go test ./...

test-integration:
	@test -n "$$TEST_DATABASE_URL" || (echo "Set TEST_DATABASE_URL to a disposable PostgreSQL database" && exit 1)
	go test -v ./cmd/integration

pull:
	docker compose pull server

compose-up: pull
	docker compose up -d --no-build

compose-down:
	docker compose down
