.PHONY: test test-race lint build tidy clean archlint vet

GOFLAGS ?=
GO ?= go

test:
	$(GO) test $(GOFLAGS) ./...

test-race:
	$(GO) test -race -coverprofile=coverage.txt $(GOFLAGS) ./...

vet:
	$(GO) vet ./...

lint:
	golangci-lint run

archlint:
	$(GO) run ./tools/archlint ./...

build:
	$(GO) build -o bin/gact ./cmd/gact

tidy:
	$(GO) mod tidy

clean:
	rm -rf bin/ dist/ coverage.txt
