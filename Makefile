.PHONY: help build run test test-coverage fmt fmt-check vet lint tidy clean \
	frontend-install frontend-dev frontend-build frontend-preview frontend-lint \
	docker-build docker-up docker-down ci all

BINARY := mockmt

help:
	@echo "Available targets:"
	@echo "  build             Build the Go binary"
	@echo "  run               Run the server locally"
	@echo "  test              Run Go tests"
	@echo "  test-coverage     Run Go tests with coverage report"
	@echo "  fmt               Format Go source with gofmt"
	@echo "  fmt-check         Check Go source is gofmt-clean"
	@echo "  vet               Run go vet"
	@echo "  lint              Run golangci-lint"
	@echo "  tidy              Run go mod tidy"
	@echo "  frontend-install  Install frontend dependencies"
	@echo "  frontend-dev      Run the frontend dev server"
	@echo "  frontend-build    Build the frontend for production"
	@echo "  frontend-preview  Preview the production frontend build"
	@echo "  docker-build      Build the Docker image via docker-compose"
	@echo "  docker-up         Start the app via docker-compose"
	@echo "  docker-down       Stop the app via docker-compose"
	@echo "  ci                Run fmt-check, vet, lint and test"
	@echo "  clean             Remove build artifacts"

build:
	go build -o $(BINARY) .

run:
	go run .

test:
	go test ./...

test-coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

fmt:
	gofmt -w .

fmt-check:
	@test -z "$$(gofmt -l .)" || (echo "The following files need gofmt:"; gofmt -l .; exit 1)

vet:
	go vet ./...

lint:
	golangci-lint run ./...

tidy:
	go mod tidy

frontend-install:
	cd frontend && npm install

frontend-dev:
	cd frontend && npm run dev

frontend-build:
	cd frontend && npm run build

frontend-preview:
	cd frontend && npm run preview

docker-build:
	docker compose build

docker-up:
	docker compose up -d

docker-down:
	docker compose down

ci: fmt-check vet lint test

all: build frontend-build

clean:
	rm -f $(BINARY) coverage.out
	rm -rf frontend/dist
