NAME                        := otelcol
REPO_ROOT                   := $(shell dirname $(realpath $(lastword $(MAKEFILE_LIST))))
BUILD_ARCH                  ?= $(shell uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
LD_FLAGS                    ?= "-s -w"

VERSION                     := $(shell cat "$(REPO_ROOT)/VERSION")
EFFECTIVE_VERSION           := $(VERSION)-$(shell git rev-parse --short HEAD)

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
EXCL_TOOLS_DIR			    := -not -path "./internal/tools/*"
EXCL_BUILD_DIR			    := -not -path "./_build/*"
COMPONENT_DIRS              := $(shell find . -type f -name "go.mod" \
									$(EXCL_TOOLS_DIR) $(EXCL_BUILD_DIR) \
									-exec dirname {} \; | sort | grep -E '^./')
.PHONY: print-component-dirs
print-component-dirs:
	@echo $(COMPONENT_DIRS)

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
	@$(MAKE) $(BUILD_DIR) 


go-sec-report:
	@if [ -n "$(COMPONENT_DIRS)" ]; then \
		@$(MAKE) $(COMPONENT_DIRS) TARGET="gosec-report"; \
	fi


# For some reaosn, gosec has issues when trying to reference a directory that isn't '.'.
# E.g. `$ gosec dir1/...` fails with a nil error. Thus we manually change cur dir
# before running gosec.
.PHONY: go-sec-report-build
go-sec-report-build: tools build
	cd $(BUILD_DIR) && $(TOOLS_DIR)/gosec $(GOSEC_REPORT_OPT) ./...

generate-distribution: tools
	@echo "Generating opentelemetry collector distribution"
	@$(REPO_ROOT)/_tools/builder \
		--skip-get-modules \
		--skip-compilation \
		--config $(REPO_ROOT)/manifest.yml

build: generate-distribution
	@echo "Building opentelemetry collector distribution"
	@$(REPO_ROOT)/hack/build_distribution.sh $(LD_FLAGS)

verify-extended: go-check go-test go-sec-report

clean:
	@rm -rf $(REPO_ROOT)/_build
	@rm -f $(BIN_DIR)/$(NAME)

tools:
	@$(MAKE) --no-print-directory -C $(REPO_ROOT)/internal/tools create-tools

clean-tools:
	@$(MAKE) --no-print-directory -C $(REPO_ROOT)/internal/tools clean-tools

docker-image:
	@echo "Building opentelemetry collector container image"
	@$(REPO_ROOT)/hack/build_docker_image.sh $(IMAGE_REPOSITORY) $(EFFECTIVE_VERSION) $(LD_FLAGS)

.PHONY: all build clean clean-tools docker-image generate-distribution go-generate go-fmt go-sec go-sec-report go-test tools verify-extended
