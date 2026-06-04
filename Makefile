# debugsymd — build, test, and container image (via ko.build).
#
# The module targets Go 1.26; GOTOOLCHAIN=auto lets the toolchain be fetched on
# first use, so these targets work even when the host `go` is older.

KO_DOCKER_REPO ?= ko.local
TAG            ?= latest
PLATFORMS      ?= linux/amd64,linux/arm64
GO             ?= GOTOOLCHAIN=auto go

.PHONY: help build test vet lint image image-publish up down logs clean

help: ## list targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | sort | \
		awk 'BEGIN{FS=":.*?## "}{printf "  %-16s %s\n", $$1, $$2}'

build: ## compile the daemon
	$(GO) build -o debugsymd .

test: ## run the test suite
	$(GO) test ./...

vet: ## go vet
	$(GO) vet ./...

lint: ## golangci-lint (binary must be built with Go >= 1.26)
	GOTOOLCHAIN=auto golangci-lint run ./...

image: ## build the image into the local Docker daemon (ko)
	KO_DOCKER_REPO=$(KO_DOCKER_REPO) GOTOOLCHAIN=auto ko build -B -t $(TAG) --local .

image-publish: ## build & push a multi-arch image (set KO_DOCKER_REPO to your registry)
	GOTOOLCHAIN=auto ko build -B -t $(TAG) --platform=$(PLATFORMS) .

up: image ## build the image and start the compose stack
	docker compose up -d

down: ## stop the compose stack
	docker compose down

logs: ## follow the daemon logs
	docker compose logs -f debugsymd

clean: ## remove the local binary
	rm -f debugsymd
