GOLANGCI_LINT_VERSION := 2.15.0
GOLANGCI_LINT ?= golangci-lint

.PHONY: build test lint run docker-build docker-up docker-down monitoring-up monitoring-down

build:
	go build -o bin/relay ./cmd/relay

test:
	go test ./... -count=1

lint:
	@command -v "$(GOLANGCI_LINT)" >/dev/null 2>&1 || { \
		echo "Install golangci-lint v$(GOLANGCI_LINT_VERSION) as described in CONTRIBUTING.md" >&2; \
		exit 1; \
	}
	@version=$$("$(GOLANGCI_LINT)" version --short) && \
	if [ "$$version" != "$(GOLANGCI_LINT_VERSION)" ]; then \
		echo "Expected golangci-lint $(GOLANGCI_LINT_VERSION), got $$version; see CONTRIBUTING.md" >&2; \
		exit 1; \
	fi
	"$(GOLANGCI_LINT)" run --config=.golangci.yml ./...

run:
	go run ./cmd/relay

docker-build:
	docker build -t relay:local .

docker-up:
	docker compose up -d --build

docker-down:
	docker compose down

monitoring-up:
	docker compose -f docker-compose.monitoring.yml up -d

monitoring-down:
	docker compose -f docker-compose.monitoring.yml down

cli:
	go build -o bin/relay-cli.exe ./cmd/relay-cli