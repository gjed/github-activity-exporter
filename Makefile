BINARY        := github-activity-exporter
CMD           := ./cmd/github-activity-exporter

WEAVER_VERSION ?= 0.28.0
WEAVER_IMAGE   ?= otel/weaver:v$(WEAVER_VERSION)

# Prefer Docker when available, fall back to local binary.
# Override: make generate WEAVER_USE_DOCKER=0
WEAVER_USE_DOCKER ?= $(shell docker info >/dev/null 2>&1 && echo 1 || echo 0)

.PHONY: build
build: ## Build the exporter binary
	go build -o $(BINARY) $(CMD)

.PHONY: test
test: ## Run unit tests
	go test ./...

.PHONY: test-integration
test-integration: ## Run integration tests (requires live OTLP receiver)
	go test -tags integration -timeout 120s ./tests/integration/...

.PHONY: lint
lint: ## Run linter
	golangci-lint run ./...

# Ensure ~/.weaver cache dir exists for Docker bind-mount
$(HOME)/.weaver:
	mkdir -p $@

.PHONY: install-weaver
install-weaver: ## Install OTel Weaver CLI (pulls Docker image if available, else binary)
	@if command -v docker >/dev/null 2>&1; then \
		echo "Docker available — pulling $(WEAVER_IMAGE)"; \
		docker pull $(WEAVER_IMAGE); \
	elif ! command -v weaver >/dev/null 2>&1; then \
		echo "Installing weaver v$(WEAVER_VERSION) via scripts/install-weaver.sh..."; \
		bash scripts/install-weaver.sh $(WEAVER_VERSION); \
	else \
		echo "weaver already installed: $$(weaver --version)"; \
	fi

.PHONY: check-schema
check-schema: $(HOME)/.weaver ## Validate OTel Weaver registry
ifeq ($(WEAVER_USE_DOCKER),1)
	docker run --rm \
		-u $(shell id -u):$(shell id -g) \
		--env HOME=/tmp/weaver \
		--mount type=bind,source=$(HOME)/.weaver,target=/tmp/weaver/.weaver \
		--mount type=bind,source=$(PWD)/registry,target=/workspace/registry,readonly \
		$(WEAVER_IMAGE) \
		registry check -r /workspace/registry/
else
	weaver registry check -r registry/
endif

.PHONY: generate
generate: $(HOME)/.weaver ## Generate Go constants and Grafana dashboard from Weaver registry
ifeq ($(WEAVER_USE_DOCKER),1)
	docker run --rm \
		-u $(shell id -u):$(shell id -g) \
		--env HOME=/tmp/weaver \
		--mount type=bind,source=$(HOME)/.weaver,target=/tmp/weaver/.weaver \
		--mount type=bind,source=$(PWD)/registry,target=/workspace/registry,readonly \
		--mount type=bind,source=$(PWD)/templates,target=/workspace/templates,readonly \
		--mount type=bind,source=$(PWD)/internal/semconv,target=/workspace/internal/semconv \
		--mount type=bind,source=$(PWD)/dashboards,target=/workspace/dashboards \
		--workdir /workspace \
		$(WEAVER_IMAGE) \
		registry generate -r registry/ --templates templates/ go internal/semconv/
	docker run --rm \
		-u $(shell id -u):$(shell id -g) \
		--env HOME=/tmp/weaver \
		--mount type=bind,source=$(HOME)/.weaver,target=/tmp/weaver/.weaver \
		--mount type=bind,source=$(PWD)/registry,target=/workspace/registry,readonly \
		--mount type=bind,source=$(PWD)/templates,target=/workspace/templates,readonly \
		--mount type=bind,source=$(PWD)/dashboards,target=/workspace/dashboards \
		--workdir /workspace \
		$(WEAVER_IMAGE) \
		registry generate -r registry/ --templates templates/ grafana dashboards/
else
	weaver registry generate \
		-r registry/ \
		--templates templates/ \
		go \
		internal/semconv/
	weaver registry generate \
		-r registry/ \
		--templates templates/ \
		grafana \
		dashboards/
endif

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'
