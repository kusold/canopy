# Run the canopy server
run:
    cd .. && go run ./canopy

# Run tests
test:
    go test ./...

# Run tests with verbose output
test-v:
    go test -v ./...

# Run tests with coverage
test-cover:
    go test -coverprofile=coverage.out ./...
    go tool cover -func=coverage.out

# Run linters
lint:
    golangci-lint run ./...

# Run all pre-commit hooks
check:
    nix develop --command prek run --all-files

# Tidy module dependencies
tidy:
    cd .. && go work sync
    go mod tidy
