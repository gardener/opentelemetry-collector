REGISTRY                         ?= europe-docker.pkg.dev
BASE_REPOSITORY 	         ?= $(REGISTRY)/gardener-project/snapshots/gardener/otel
NAME_LOG_SHIPPER_COLLECTOR       := log-shipper
NAME_CONTROL_PLANE_COLLECTOR     := control-plane
REGISTRY_LOG_SHIPPER_COLLECTOR   ?= $(BASE_REPOSITORY)/collector-$(NAME_LOG_SHIPPER_COLLECTOR)
REGISTRY_CONTROL_PLANE_COLLECTOR ?= $(BASE_REPOSITORY)/collector-$(NAME_CONTROL_PLANE_COLLECTOR)

REPO_ROOT                    := $(shell dirname $(realpath $(lastword $(MAKEFILE_LIST))))
BUILD_ARCH                   ?= $(shell uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
LD_FLAGS                     ?= "-s -w"

VERSION                      := $(shell cat "$(REPO_ROOT)/VERSION")
EFFECTIVE_VERSION            := $(VERSION)-$(shell git rev-parse --short HEAD)

ifneq ($(strip $(shell git status --porcelain 2>/dev/null)),)
	EFFECTIVE_VERSION := $(EFFECTIVE_VERSION)-dirty
endif


#########################################
# Dirs                                  #
#########################################
BIN_DIR                     := $(REPO_ROOT)/bin
BUILD_DIR                   := $(REPO_ROOT)/_build
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

$(BUILD_DIR):
	@mkdir -p $@

.PHONY: $(COMPONENT_DIRS)
$(COMPONENT_DIRS):
	@echo "Running target '$(TARGET)' in component '$@'"
	@$(MAKE) --no-print-directory -C $@ $(TARGET)

add-license-headers:
	@$(MAKE) $(COMPONENT_DIRS) TARGET="add-license-headers"

go-check:
	@$(MAKE) $(COMPONENT_DIRS) TARGET="go-check"

go-generate: tools
	@$(MAKE) $(COMPONENT_DIRS) TARGET="go-generate"

go-fmt:
	@$(MAKE) $(COMPONENT_DIRS) TARGET="go-fmt"

go-test:
	@$(MAKE) $(COMPONENT_DIRS) TARGET="test"

go-imports:
	@$(MAKE) $(COMPONENT_DIRS) TARGET="goimports"

go-sec:
	@$(MAKE) $(COMPONENT_DIRS) TARGET="gosec"

go-sec-report:
	@$(MAKE) $(COMPONENT_DIRS) TARGET="gosec-report"

generate-distributions: tools $(BUILD_DIR)
	@echo "Generating opentelemetry collector distributions"
	@echo "Building Control Plane Collector"
	@$(REPO_ROOT)/_tools/builder \
		--skip-get-modules \
		--skip-compilation \
		--config $(REPO_ROOT)/collector-control-plane/manifest.yml
	@echo "Building Log Shipper Collector"
	@$(REPO_ROOT)/_tools/builder \
		--skip-get-modules \
		--skip-compilation \
		--config $(REPO_ROOT)/collector-log-shipper/manifest.yml

build: generate-distributions
	@echo "Building opentelemetry collector distribution"
	@$(REPO_ROOT)/hack/build_distribution.sh $(NAME_CONTROL_PLANE_COLLECTOR) $(LD_FLAGS)
	@$(REPO_ROOT)/hack/build_distribution.sh $(NAME_LOG_SHIPPER_COLLECTOR) $(LD_FLAGS)

verify-extended: go-check go-test go-sec-report

clean:
	@rm -rf $(REPO_ROOT)/_build
	@rm -f $(BIN_DIR)/$(NAME_LOG_SHIPPER_COLLECTOR)
	@rm -f $(BIN_DIR)/$(NAME_CONTROL_PLANE_COLLECTOR)

tools:
	@$(MAKE) --no-print-directory -C $(REPO_ROOT)/internal/tools create-tools

clean-tools:
	@$(MAKE) --no-print-directory -C $(REPO_ROOT)/internal/tools clean-tools

docker-images:
	@echo "Building opentelemetry collector container images"
	@$(REPO_ROOT)/hack/build_docker_image.sh $(NAME_CONTROL_PLANE_COLLECTOR) $(REGISTRY_CONTROL_PLANE_COLLECTOR) $(EFFECTIVE_VERSION) $(LD_FLAGS)
	@$(REPO_ROOT)/hack/build_docker_image.sh $(NAME_LOG_SHIPPER_COLLECTOR) $(REGISTRY_LOG_SHIPPER_COLLECTOR) $(EFFECTIVE_VERSION) $(LD_FLAGS)

.PHONY: all build clean clean-tools docker-images generate-distributions go-generate go-fmt go-sec go-sec-report go-test tools verify-extended
