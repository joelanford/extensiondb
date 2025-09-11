.PHONY: help build run test clean db-up db-down db-reset migrate

# Default target
help:
	@echo "Available targets:"
	@echo "  build     - Build the application"
	@echo "  run       - Run the application"
	@echo "  test      - Run tests"
	@echo "  clean     - Clean build artifacts"
	@echo "  db-up     - Start PostgreSQL with docker-compose"
	@echo "  db-down   - Stop PostgreSQL"
	@echo "  db-reset  - Reset database (stop, remove volumes, start)"
	@echo "  migrate   - Run database migrations"

# Build the application
build:
	go build -o bin/extensiondb cmd/main.go

# Run the application
run: build
	./bin/extensiondb

# Run tests
test:
	go test ./...

# Clean build artifacts
clean:
	rm -rf bin/

# Start PostgreSQL database
db-up:
	docker-compose up -d postgres
	@echo "Waiting for PostgreSQL to be ready..."
	@until docker-compose exec postgres pg_isready -U postgres; do sleep 1; done
	@echo "PostgreSQL is ready!"

# Stop PostgreSQL database
db-down:
	docker-compose down

# Reset database (useful for development)
db-reset:
	docker-compose down -v
	docker-compose up -d postgres
	@echo "Waiting for PostgreSQL to be ready..."
	@until docker-compose exec postgres pg_isready -U postgres; do sleep 1; done
	@echo "PostgreSQL is ready!"

# Run database migrations
migrate: db-up
	@echo "Running database migrations..."
	go run -tags containers_image_openpgp cmd/main.go

# Development workflow
dev: db-up migrate
	@echo "Development environment ready!"

# Install dependencies
deps:
	go mod tidy
	go mod download
