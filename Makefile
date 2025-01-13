NAME                        := otelcol
REPO_ROOT                   := $(shell dirname $(realpath $(lastword $(MAKEFILE_LIST))))
BUILD_ARCH                  ?= $(shell uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
LD_FLAGS                    ?= "-s -w"

VERSION                     := $(shell cat "$(REPO_ROOT)/VERSION")
EFFECTIVE_VERSION           := $(VERSION)-$(shell git rev-parse --short HEAD)

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

.PHONY: $(COMPONENT_DIRS)
$(COMPONENT_DIRS):
	@echo "Running target '$(TARGET)' in component '$@'"
	@$(MAKE) --no-print-directory -C $@ $(TARGET)

add-license-headers:
	@$(MAKE) $(COMPONENT_DIRS) TARGET="add-license-headers"

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

generate-distribution: tools
	@echo "Generating opentelemetry collector distribution"
	@$(REPO_ROOT)/_tools/builder \
		--skip-get-modules \
		--skip-compilation \
		--config $(REPO_ROOT)/manifest.yml

build: generate-distribution
	@echo "Building opentelemetry collector distribution"
	@$(REPO_ROOT)/hack/build_distribution.sh $(LD_FLAGS)

clean:
	@rm -rf $(REPO_ROOT)/_build
	@rm -f $(BIN_DIR)/$(NAME)

tools:
	@$(MAKE) --no-print-directory -C $(REPO_ROOT)/internal/tools create-tools

clean-tools:
	@$(MAKE) --no-print-directory -C $(REPO_ROOT)/internal/tools clean-tools

.PHONY: all build clean clean-tools for-all generate-distribution go-generate go-fmt go-sec go-sec-report go-test tools
