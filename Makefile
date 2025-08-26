NAME                        := otelcol
REPO_ROOT                   := $(shell dirname $(realpath $(lastword $(MAKEFILE_LIST))))
LD_FLAGS                    ?= "-s -w"

VERSION                     := $(shell cat "$(REPO_ROOT)/VERSION")
REVISION                    := $(shell git rev-parse --short HEAD)
EFFECTIVE_VERSION           := $(VERSION)-$(REVISION)

ifneq ($(strip $(shell git status --porcelain 2>/dev/null)),)
	EFFECTIVE_VERSION := $(EFFECTIVE_VERSION)-dirty
endif

REGISTRY                    ?= europe-docker.pkg.dev/gardener-project/snapshots/gardener/otel
IMAGE_REPOSITORY            := $(REGISTRY)/opentelemetry-collector

GOSEC_REPORT_OPT            ?= -exclude-generated -track-suppressions -stdout -fmt=sarif -out=gosec-report.sarif

#########################################
# Dirs                                  #
#########################################
BIN_DIR                     := $(REPO_ROOT)/bin
BUILD_DIR                   := $(REPO_ROOT)/_build
TOOLS_DIR                   := $(abspath $(REPO_ROOT)/_tools)
COMPONENT_DIRS              := $(shell find . -mindepth 2 \
						-type f -name "go.mod" \
						-not -path "./internal/tools/*" \
						-not -path "./_build/*" \
						-exec dirname {} \;)

.PHONY: print-component-dirs
print-component-dirs:
	@echo $(COMPONENT_DIRS)

.PHONY: print-effective-version
print-effective-version:
	@echo $(EFFECTIVE_VERSION)

.PHONY: print-revision
print-revision:
	@echo $(REVISION)

#########################################
.DEFAULT_GOAL := all
all: $(BIN_DIR) go-generate go-test build

$(BIN_DIR):
	@mkdir -p $@

.PHONY: $(COMPONENT_DIRS)
$(COMPONENT_DIRS):
	@echo "Running target '$(TARGET)' in component '$@'"
	@$(MAKE) --no-print-directory -C $@ $(TARGET)

add-license-headers:
	@if [ -n "$(COMPONENT_DIRS)" ]; then \
		@$(MAKE) $(COMPONENT_DIRS) TARGET="add-license-headers"; \
	fi

go-check:
	@if [ -n "$(COMPONENT_DIRS)" ]; then \
		@$(MAKE) $(COMPONENT_DIRS) TARGET="go-check"; \
	fi

go-generate: tools
	@if [ -n "$(COMPONENT_DIRS)" ]; then \
		@$(MAKE) $(COMPONENT_DIRS) TARGET="go-generate"; \
	fi

go-fmt:
	@if [ -n "$(COMPONENT_DIRS)" ]; then \
		@$(MAKE) $(COMPONENT_DIRS) TARGET="go-fmt"; \
	fi

go-test:
	@if [ -n "$(COMPONENT_DIRS)" ]; then \
		@$(MAKE) $(COMPONENT_DIRS) TARGET="test"; \
	fi

go-imports:
	@if [ -n "$(COMPONENT_DIRS)" ]; then \
		@$(MAKE) $(COMPONENT_DIRS) TARGET="goimports"; \
	fi

go-sec:
	@if [ -n "$(COMPONENT_DIRS)" ]; then \
		@$(MAKE) $(COMPONENT_DIRS) TARGET="gosec"; \
	fi

go-sec-report:
	@if [ -n "$(COMPONENT_DIRS)" ]; then \
		@$(MAKE) $(COMPONENT_DIRS) TARGET="gosec-report"; \
	fi

# For some reaosn, gosec has issues when trying to reference a directory that isn't '.'.
# E.g. `$ gosec dir1/...` fails with a nil error. Thus, we manually change cur dir
# before running gosec.
.PHONY: go-sec-report-build
go-sec-report-build: tools build
	cd $(BUILD_DIR) && $(TOOLS_DIR)/gosec $(GOSEC_REPORT_OPT) ./...

generate-distribution: builder-tool
	@echo "Generating opentelemetry collector distribution"
	$(REPO_ROOT)/_tools/builder \
		--skip-get-modules \
		--skip-compilation \
		--config $(REPO_ROOT)/manifest.yml

build: generate-distribution
	@echo "Building opentelemetry collector distribution"
	@cd $(BUILD_DIR) && \
		go mod download && \
		go mod tidy && \
		env CGO_ENABLED=0 GO111MODULE=on go build -ldflags $(LD_FLAGS) -o $(BIN_DIR)/$(NAME) .

verify-extended: go-check go-test go-sec-report

clean:
	@rm -rf $(REPO_ROOT)/_build
	@rm -f $(BIN_DIR)/$(NAME)

tools:
	@$(MAKE) --no-print-directory -C $(REPO_ROOT)/internal/tools create-tools

builder-tool:
	@$(MAKE) --no-print-directory -C $(REPO_ROOT)/internal/tools $(TOOLS_DIR)/builder

clean-tools:
	@$(MAKE) --no-print-directory -C $(REPO_ROOT)/internal/tools clean-tools

docker-image:
	@echo "Building opentelemetry collector container image"
	@docker build \
		--build-arg BUILD_DATE=$(shell date -u +'%Y-%m-%dT%H:%M:%SZ') \
		--build-arg EFFECTIVE_VERSION=$(EFFECTIVE_VERSION) \
		--build-arg REVISION=$(REVISION) \
		-t "$(IMAGE_REPOSITORY):$(EFFECTIVE_VERSION)" \
		-t "$(IMAGE_REPOSITORY):latest" \
		.

.PHONY: all build clean clean-tools docker-image generate-distribution go-generate go-fmt go-sec go-sec-report go-test tools verify-extended builder-tool go-sec-report-build
