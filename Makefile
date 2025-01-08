NAME                        := otelcol
REPO_ROOT                   := $(shell dirname $(realpath $(lastword $(MAKEFILE_LIST))))
BUILD_ARCH					?= $(shell uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
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

build: $(GO_OCB)
	@echo "Building $(NAME)..."
	@$(GO_OCB) \
	--config $(REPO_ROOT)/manifest.yml
clean:
	@rm -rf $(REPO_ROOT)/_build

.PHONY: all build clean