.PHONY: test build up down logs importer-dry-run

test:
	go test -race ./...

build:
	go build ./cmd/bot ./cmd/importer

up:
	docker compose up --build -d

down:
	docker compose down

logs:
	docker compose logs -f bot

importer-dry-run:
	docker compose run --rm --entrypoint /usr/local/bin/cracksms-importer bot --data /legacy/data.json
