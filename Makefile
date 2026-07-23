GOTEST := go test
COVERAGE_FILE := coverage.out
GOLANGCI_LINT_VERSION := v2.11.4

.PHONY: codespell
codespell:
	@echo "Running codespell"
	@codespell

.PHONY: test
test:
	@echo "Running tests"
	@go test ./... -count=1 -v

.PHONY: testrace
testrace:
	@echo "Running tests with race flag"
	@go test ./... -count=100 -race -v -timeout 30m

.PHONY: coverage
coverage:
	@echo "Running tests with coverage"
	@go test ./... -v -coverprofile=$(COVERAGE_FILE) -count=1 -covermode=atomic
	@go tool cover -func=$(COVERAGE_FILE)

.PHONY: coveralls
coveralls:
	@echo "Uploading coverage to Coveralls"
	@goveralls -coverprofile=$(COVERAGE_FILE) -service=github

.PHONY: coveralls-deps
coveralls-deps:
	@echo "Installing goveralls"
	@go install github.com/mattn/goveralls@latest

.PHONY: lint-deps
lint-deps:
	@echo "Installing lint deps"
	@go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

.PHONY: lint
lint:
	@echo "Running go-linter"
	@go mod tidy
	@golangci-lint run --config=./.golangci.yml

.PHONY: fmt
fmt:
	@echo "Formatting code"
	@gofmt -w .
	@goimports -w .
