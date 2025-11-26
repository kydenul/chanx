
# Go parameters
GOCMD := go
GOBUILD := $(GOCMD) build
GOCLEAN := $(GOCMD) clean
GOTEST := $(GOCMD) test
GOGET := $(GOCMD) get
GOMOD := $(GOCMD) mod
GOFUMPT := gofumpt
GOLINT := golangci-lint

# Project parameters
BIN_DIR := bin
CMD_DIR := cmd
TARGETS := $(notdir $(wildcard $(CMD_DIR)/*))
PKG_LIST := $(shell $(GOCMD) list ./... | grep -v /vendor/)

# Build flags
VERSION := $(shell git describe --tags --always --dirty)
BUILD_TIME := $(shell date -u '+%Y-%m-%d %H:%M:%S')

# Colors for pretty printing
GREEN := \033[0;32m
BLUE := \033[0;34m
NC := \033[0m # No Color

# Targets
.PHONY: all clean test lint tidy help

# Default target
all: build
build: clean tidy fumpt lint

test:
	@printf "$(BLUE)Running tests ...$(NC)\n"
	@$(GOTEST) -v $(PKG_LIST)

fumpt:
	@printf "$(BLUE)Running fumpt ...$(NC)\n"
	@$(GOFUMPT) -w -l $(shell find . -name '*.go')

lint:
	@printf "$(BLUE)Running linter ...$(NC)\n"
	@$(GOLINT) run ./...

tidy:
	@printf "$(BLUE)Tidying and verifying module dependencies ...$(NC)\n"
	@$(GOMOD) tidy
	@$(GOMOD) verify

clean:
	@printf "$(BLUE)Cleaning up ...$(NC)\n"
	@$(GOCLEAN)
	@rm -rf $(BIN_DIR)/* *.pid *.perf

help:
	@echo "Available targets:"
	@echo "  all (build) : Build the program in release mode (default)"
	@echo "  test        : Run all tests"
	@echo "  fumpt       : Run gofumpt to format and simplify code"
	@echo "  lint        : Run golangci-lint for code quality checks"
	@echo "  tidy        : Tidy and verify go modules dependencies"
	@echo "  clean       : Remove object files and binaries"
	@echo "  help        : Display this help message"

# Debugging
print-%:
	@echo '$*=$($*)'
