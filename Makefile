.PHONY: all build test test-spec clean clean-tmp build-test-wasm help

# Default target
all: build

# Build the fastlike binary
build:
	@echo "Building fastlike..."
	@go build -o bin/fastlike ./cmd/fastlike

# Install fastlike to GOPATH/bin
install:
	@echo "Installing fastlike..."
	@go install ./cmd/fastlike

# Run all Go tests
test:
	@echo "Running Go tests..."
	@go test -v ./...

# Run spec tests with default wasm
test-spec:
	@echo "Running spec tests..."
	@cd specs && go test -v

# Run spec tests with custom wasm
test-spec-custom:
	@if [ -z "$(WASM)" ]; then \
		echo "Error: WASM variable not set. Usage: make test-spec-custom WASM=path/to/app.wasm"; \
		exit 1; \
	fi
	@echo "Running spec tests with custom wasm: $(WASM)"
	@cd specs && go test -v -wasm $(WASM)

# Build the spec test runner binary
build-spec-runner:
	@echo "Building spec test runner..."
	@cd specs && go test -c . -o spec-runner

# Build the Rust test wasm programs
build-test-wasm:
	@echo "Building Rust test wasm..."
	@cd specs/testdata/rust && cargo build --target wasm32-wasip1 --release
	@echo "Test wasm built at specs/testdata/rust/target/wasm32-wasip1/release/example.wasm"

# Format Go code
fmt:
	@echo "Formatting Go code..."
	@go fmt ./...

# Run Go linter
lint:
	@echo "Running Go linter..."
	@golangci-lint run || go vet ./...

# Tidy Go modules
tidy:
	@echo "Tidying Go modules..."
	@go mod tidy

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	@rm -rf bin/
	@cd specs && rm -f spec-runner
	@cd specs/testdata/rust && cargo clean

# Clean temporary files
clean-tmp:
	@echo "Cleaning temporary files..."
	@rm -rf tmp/

# Run the fastlike proxy (requires WASM and BACKEND variables)
run:
	@if [ -z "$(WASM)" ]; then \
		echo "Error: WASM variable not set. Usage: make run WASM=path/to/app.wasm BACKEND=localhost:8000"; \
		exit 1; \
	fi
	@if [ -z "$(BACKEND)" ]; then \
		echo "Error: BACKEND variable not set. Usage: make run WASM=path/to/app.wasm BACKEND=localhost:8000"; \
		exit 1; \
	fi
	@echo "Running fastlike proxy..."
	@go run ./cmd/fastlike -wasm $(WASM) -backend $(BACKEND) $(ARGS)

# Display help
help:
	@echo "Fastlike Makefile"
	@echo ""
	@echo "Targets:"
	@echo "  all               - Build the fastlike binary (default)"
	@echo "  build             - Build the fastlike binary to bin/fastlike"
	@echo "  install           - Install fastlike to GOPATH/bin"
	@echo "  test              - Run all Go tests"
	@echo "  test-spec         - Run spec tests with default wasm"
	@echo "  test-spec-custom  - Run spec tests with custom wasm (requires WASM=path)"
	@echo "  build-spec-runner - Build the spec test runner binary"
	@echo "  build-test-wasm   - Build the Rust test wasm programs"
	@echo "  fmt               - Format Go code"
	@echo "  lint              - Run Go linter"
	@echo "  tidy              - Tidy Go modules"
	@echo "  clean             - Clean build artifacts"
	@echo "  clean-tmp         - Clean temporary files"
	@echo "  run               - Run the fastlike proxy (requires WASM and BACKEND)"
	@echo "  help              - Display this help message"
	@echo ""
	@echo "Examples:"
	@echo "  make build"
	@echo "  make test"
	@echo "  make build-test-wasm"
	@echo "  make run WASM=app.wasm BACKEND=localhost:8000"
	@echo "  make run WASM=app.wasm BACKEND=example=localhost:8000 ARGS='-v 2'"
	@echo "  make test-spec-custom WASM=path/to/app.wasm"
