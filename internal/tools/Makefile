# Copyright SAP SE or an SAP affiliate company and Gardener contributors
# SPDX-License-Identifier: Apache-2.0
DIR                                    := $(shell dirname $(realpath $(lastword $(MAKEFILE_LIST))))
REPO_ROOT                              := $(shell dirname $(shell dirname $(DIR)))
TOOLS_DIR                              := $(REPO_ROOT)/_tools

GO_ADD_LICENSE                         := $(TOOLS_DIR)/addlicense
GO_ADD_LICENSE_VERSION                 ?= $(call version_gomod,github.com/google/addlicense)

GO_GCI                                 := $(TOOLS_DIR)/gci
GO_GCI_VERSION                         ?= $(call version_gomod,github.com/daixiang0/gci)

GO_IMPORTS                             := $(TOOLS_DIR)/goimports
GO_IMPORTS_VERSION                     ?= $(call version_gomod,golang.org/x/tools)

GO_LINT                                := $(TOOLS_DIR)/golangci-lint
GO_LINT_VERSION                        ?= v1.63.4

GO_MDATAGEN                            := $(TOOLS_DIR)/mdatagen
GO_MDATAGEN_VERSION                    ?= $(call version_gomod,go.opentelemetry.io/collector/cmd/mdatagen)

GO_OTELCOL_BUILDER                     := $(TOOLS_DIR)/builder
GO_OTELCOL_BUILDER_VERSION             ?= $(call version_gomod,go.opentelemetry.io/collector/cmd/builder)

GO_SEC                                 := $(TOOLS_DIR)/gosec
GO_SEC_VERSION                         ?= v2.22.8

GO_TESTIFYLINT                         := $(TOOLS_DIR)/testifylint
GO_TESTIFYLINT_VERSION                 ?= $(call version_gomod,github.com/Antonboom/testifylint)

GO_TESTSUM                             := $(TOOLS_DIR)/gotestsum
GO_TESTSUM_VERSION                     ?= $(call version_gomod,gotest.tools/gotestsum)

$(TOOLS_DIR):
	mkdir -p $@

.PHONY: go-mod
go-mod:
	@go mod download && go mod tidy

export PATH := $(abspath $(TOOLS_DIR)):$(PATH)

#########################################
# Common                                #
#########################################
.DEFAULT_GOAL := create-tools

tool_version_file = $(TOOLS_DIR)/.version_$(subst $(TOOLS_DIR)/,,$(1))_$(2)

# Use this function to get the version of a go module from go.mod
version_gomod = $(shell go list -modfile=$(DIR)/go.mod -mod=mod -f '{{ .Version }}' -m $(1))

# This target cleans up any previous version files for the given tool and creates the given version file.
# This way, we can generically determine, which version was installed without calling each and every binary explicitly.
$(TOOLS_DIR)/.version_%:
	@mkdir -p  $(TOOLS_DIR)
	@version_file=$@; rm -f $${version_file%_*}*
	@touch $@

clean-tools:
	@rm -f $(GO_ADD_LICENSE) \
			$(GO_GCI) \
			$(GO_IMPORTS) \
			$(GO_LINT) \
			$(GO_MDATAGEN) \
			$(GO_OTELCOL_BUILDER) \
			$(GO_SEC) \
			$(GO_TESTSUM) \
			$(GO_TESTIFYLINT) \
			$(TOOLS_DIR)/.version_*

create-tools: go-mod \
				$(GO_ADD_LICENSE) \
				$(GO_GCI) \
				$(GO_IMPORTS) \
				$(GO_LINT) \
				$(GO_MDATAGEN) \
				$(GO_OTELCOL_BUILDER) \
				$(GO_SEC) \
				$(GO_TESTIFYLINT) \
				$(GO_TESTSUM)

#########################################
# Tools                                 #
#########################################

$(GO_ADD_LICENSE):  $(call tool_version_file,$(GO_ADD_LICENSE),$(GO_ADD_LICENSE_VERSION))
	@echo "install target: $@"
	@GOBIN=$(abspath $(TOOLS_DIR)) go install github.com/google/addlicense@$(GO_ADD_LICENSE_VERSION)

$(GO_GCI):  $(call tool_version_file,$(GO_GCI),$(GO_GCI_VERSION))
	@echo "install target: $@"
	@GOBIN=$(abspath $(TOOLS_DIR)) go install github.com/daixiang0/gci@$(GO_GCI_VERSION)

$(GO_IMPORTS): $(call tool_version_file,$(GO_IMPORTS),$(GO_IMPORTS_VERSION))
	@echo "install target: $@"
	@go build -o $(GO_IMPORTS) golang.org/x/tools/cmd/goimports

$(GO_LINT): $(call tool_version_file,$(GO_LINT),$(GO_LINT_VERSION))
	@echo "install target: $@"
	@GOBIN=$(abspath $(TOOLS_DIR)) CGO_ENABLED=1 go install github.com/golangci/golangci-lint/cmd/golangci-lint@$(GO_LINT_VERSION)

$(GO_MDATAGEN): $(call tool_version_file,$(GO_MDATAGEN),$(GO_MDATAGEN_VERSION))
	@echo "install target: $@"
	@GOBIN=$(abspath $(TOOLS_DIR)) go build -o $(GO_MDATAGEN) go.opentelemetry.io/collector/cmd/mdatagen

$(GO_OTELCOL_BUILDER): $(call tool_version_file,$(GO_OTELCOL_BUILDER),$(GO_OTELCOL_BUILDER_VERSION))
	@echo "install target: $@"
	@GOBIN=$(abspath $(TOOLS_DIR)) go install go.opentelemetry.io/collector/cmd/builder@$(GO_OTELCOL_BUILDER_VERSION)

$(GO_SEC): $(call tool_version_file,$(GO_SEC),$(GO_SEC_VERSION))
	@echo "install target: $@"
	@GOBIN=$(abspath $(TOOLS_DIR)) go install github.com/securego/gosec/v2/cmd/gosec@$(GO_SEC_VERSION)

$(GO_TESTIFYLINT): $(call tool_version_file,$(GO_TESTIFYLINT),$(GO_TESTIFYLINT_VERSION))
	@echo "install target: $@"
	@GOBIN=$(abspath $(TOOLS_DIR)) go install github.com/Antonboom/testifylint@$(GO_TESTIFYLINT_VERSION)

$(GO_TESTSUM): $(call tool_version_file,$(GO_TESTSUM),$(GO_TESTSUM_VERSION))
	@echo "install target: $@"
	@GOBIN=$(abspath $(TOOLS_DIR)) go install gotest.tools/gotestsum@$(GO_TESTSUM_VERSION)

.PHONY: create-tools clean-tools
