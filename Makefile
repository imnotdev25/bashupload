# bashupload Makefile

.PHONY: all build build-server build-cli clean run dev docker-build docker-run docker-stop test deps help

# Variables
BINARY_SERVER=bashupload-server
BINARY_CLI=bashupload
DOCKER_IMAGE=bashupload:latest
PORT?=3000

# Default target
all: build

# Build both server and CLI
build: build-server build-cli

# Build server
build-server:
	@echo "Building server..."
	CGO_ENABLED=1 go build -o $(BINARY_SERVER) .
	@echo "Server built successfully: $(BINARY_SERVER)"

# Build CLI
build-cli:
	@echo "Building CLI..."
	CGO_ENABLED=1 go build -o $(BINARY_CLI) ./cmd/cli
	@echo "CLI built successfully: $(BINARY_CLI)"

# Build for different platforms
build-linux:
	@echo "Building for Linux..."
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -o $(BINARY_SERVER)-linux .
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -o $(BINARY_CLI)-linux ./cmd/cli

build-windows:
	@echo "Building for Windows..."
	CGO_ENABLED=1 GOOS=windows GOARCH=amd64 go build -o $(BINARY_SERVER).exe .
	CGO_ENABLED=1 GOOS=windows GOARCH=amd64 go build -o $(BINARY_CLI).exe ./cmd/cli

build-darwin:
	@echo "Building for macOS..."
	CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 go build -o $(BINARY_SERVER)-darwin .
	CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 go build -o $(BINARY_CLI)-darwin ./cmd/cli

# Clean built binaries
clean:
	@echo "Cleaning up..."
	rm -f $(BINARY_SERVER) $(BINARY_CLI)
	rm -f $(BINARY_SERVER)-* $(BINARY_CLI)-*
	rm -f $(BINARY_SERVER).exe $(BINARY_CLI).exe
	rm -f fileuploader.db
	rm -rf uploads/
	@echo "Clean completed"

# Run server
run: build-server
	@echo "Starting server on port $(PORT)..."
	PORT=$(PORT) ./$(BINARY_SERVER)

# Development mode with auto-reload
dev:
	@echo "Starting development server..."
	@if command -v air > /dev/null; then \
		air; \
	else \
		echo "Air not found. Installing..."; \
		go install github.com/cosmtrek/air@latest; \
		air; \
	fi

# Install dependencies
deps:
	@echo "Installing dependencies..."
	go mod download
	go mod tidy
	@echo "Dependencies installed"

# Docker commands
docker-build:
	@echo "Building Docker image..."
	docker build -t $(DOCKER_IMAGE) .
	@echo "Docker image built: $(DOCKER_IMAGE)"

docker-run: docker-build
	@echo "Running Docker container..."
	docker run -d \
		--name bashupload \
		-p $(PORT):3000 \
		-v $(PWD)/uploads:/app/uploads \
		-v $(PWD)/bashupload.db:/app/bashupload.db \
		$(DOCKER_IMAGE)
	@echo "Container started on port $(PORT)"

docker-stop:
	@echo "Stopping Docker container..."
	docker stop bashupload || true
	docker rm bashupload || true
	@echo "Container stopped"

# Docker Compose commands
compose-up:
	@echo "Starting with Docker Compose..."
	docker-compose up -d
	@echo "Services started"

compose-down:
	@echo "Stopping Docker Compose services..."
	docker-compose down
	@echo "Services stopped"

compose-logs:
	docker-compose logs -f

# Test commands
test:
	@echo "Running tests..."
	go test -v ./...

test-coverage:
	@echo "Running tests with coverage..."
	go test -v -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

# Benchmark
benchmark:
	@echo "Running benchmarks..."
	go test -bench=. -benchmem ./...

# CLI usage examples
demo-upload:
	@echo "Demo: Uploading a test file..."
	echo "Hello, World!" > test.txt
	./$(BINARY_CLI) upload test.txt
	rm test.txt

demo-info:
	@echo "Demo: Getting file info (replace FILE_ID with actual ID)..."
	@echo "./$(BINARY_CLI) info FILE_ID"

demo-download:
	@echo "Demo: Downloading a file (replace FILE_ID with actual ID)..."
	@echo "./$(BINARY_CLI) download FILE_ID downloaded_file.txt"

# Install CLI globally
install-cli: build-cli
	@echo "Installing CLI globally..."
	sudo cp $(BINARY_CLI) /usr/local/bin/
	@echo "CLI installed to /usr/local/bin/$(BINARY_CLI)"

# Create release package
release: clean build-linux build-windows build-darwin
	@echo "Creating release package..."
	mkdir -p release
	cp $(BINARY_SERVER)-linux release/
	cp $(BINARY_CLI)-linux release/
	cp $(BINARY_SERVER).exe release/
	cp $(BINARY_CLI).exe release/
	cp $(BINARY_SERVER)-darwin release/
	cp $(BINARY_CLI)-darwin release/
	cp README.md release/
	cp docker-compose.yml release/
	cp Dockerfile release/
	@echo "Release package created in ./release/"

# Database operations
db-reset:
	@echo "Resetting database..."
	rm -f bashupload.db
	@echo "Database reset completed"

db-backup:
	@echo "Backing up database..."
	cp bashupload.db bashupload.db.backup
	@echo "Database backed up to bashupload.db.backup"

# Show help
help:
	@echo "bashupload - Available Commands:"
	@echo ""
	@echo "Build Commands:"
	@echo "  build          - Build both server and CLI"
	@echo "  build-server   - Build only the server"
	@echo "  build-cli      - Build only the CLI"
	@echo "  build-linux    - Build for Linux"
	@echo "  build-windows  - Build for Windows"
	@echo "  build-darwin   - Build for macOS"
	@echo ""
	@echo "Run Commands:"
	@echo "  run            - Build and run the server"
	@echo "  dev            - Run in development mode with auto-reload"
	@echo ""
	@echo "Docker Commands:"
	@echo "  docker-build   - Build Docker image"
	@echo "  docker-run     - Build and run Docker container"
	@echo "  docker-stop    - Stop Docker container"
	@echo "  compose-up     - Start with Docker Compose"
	@echo "  compose-down   - Stop Docker Compose services"
	@echo "  compose-logs   - View Docker Compose logs"
	@echo ""
	@echo "Test Commands:"
	@echo "  test           - Run tests"
	@echo "  test-coverage  - Run tests with coverage report"
	@echo "  benchmark      - Run benchmarks"
	@echo ""
	@echo "Utility Commands:"
	@echo "  deps           - Install dependencies"
	@echo "  clean          - Clean built binaries"
	@echo "  install-cli    - Install CLI globally"
	@echo "  release        - Create release package"
	@echo "  db-reset       - Reset database"
	@echo "  db-backup      - Backup database"
	@echo ""
	@echo "Demo Commands:"
	@echo "  demo-upload    - Demo file upload"
	@echo "  demo-info      - Demo file info"
	@echo "  demo-download  - Demo file download"
	@echo ""
	@echo "Usage: make [command] [PORT=3000]"