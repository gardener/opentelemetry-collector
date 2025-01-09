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
TOOLS_DIR                   := $(REPO_ROOT)/tools
BUILD_DIR                   := $(REPO_ROOT)/_build

#########################################
# Tools                                 #
#########################################
include $(REPO_ROOT)/hack/tools.mk

#########################################
.DEFAULT_GOAL := all

all: $(BIN_DIR) $(TOOLS_DIR) build

$(TOOLS_DIR):
	@mkdir -p $@

$(BIN_DIR):
	@mkdir -p $@

generate-distribution: $(GO_OCB)
	@echo "Generating opentelemetry collector distribution"
	@$(GO_OCB) \
		--skip-get-modules \
		--skip-compilation \
		--config $(REPO_ROOT)/manifest.yml

build: generate-distribution
	@echo "Building opentelemetry collector distribution"
	@$(REPO_ROOT)/hack/build_distribution.sh $(LD_FLAGS)

clean:
	@rm -rf $(REPO_ROOT)/_build
	@rm -f $(BIN_DIR)/$(NAME)

.PHONY: all build clean generate-distribution
