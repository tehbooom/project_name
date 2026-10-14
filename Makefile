GOLANGCI_LINT ?= golangci-lint

.PHONY: all lint unit web e2e test

all: test

lint:
	$(GOLANGCI_LINT) run ./...

unit:
	go test -race -covermode=atomic -coverprofile=coverage.out ./...

web:
	cd frontend && npm ci && npm run build

e2e:
	./scripts/e2e.sh

test: lint unit
