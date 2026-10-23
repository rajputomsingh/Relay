.PHONY: build test run docker-build docker-up docker-down

build:
    go build -o bin/relay ./cmd/relay

test:
    go test ./... -count=1

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
