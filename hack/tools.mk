# Copyright 2021 SAP SE or an SAP affiliate company. All rights reserved. This file is licensed under the Apache Software License, v. 2 except as noted otherwise in the LICENSE file
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# This make file is supposed to be included in the top-level make file.
# It can be reused by repos vendoring g/g to have some common make recipes for building and installing development
# tools as needed.
# Recipes in the top-level make file should declare dependencies on the respective tool recipes (e.g. $(CONTROLLER_GEN))
# as needed. If the required tool (version) is not built/installed yet, make will make sure to build/install it.
# The *_VERSION variables in this file contain the "default" values, but can be overwritten in the top level make file.
GO_ADD_LICENSE                             := $(TOOLS_DIR)/addlicense
GO_ADD_LICENSE_VERSION                     ?= $(call version_gomod,github.com/google/addlicense)

GO_OCB                                     := $(TOOLS_DIR)/builder
GO_OCB_VERSION                             ?= $(call version_gomod,go.opentelemetry.io/collector/cmd/builder)

export PATH := $(abspath $(TOOLS_DIR)):$(PATH)

#########################################
# Common                                #
#########################################

# Tool targets should declare go.mod as a prerequisite, if the tool's version is managed via go modules. This causes
# make to rebuild the tool in the desired version, when go.mod is changed.
# For tools where the version is not managed via go.mod, we use a file per tool and version as an indicator for make

tool_version_file = $(TOOLS_DIR)/.version_$(subst $(TOOLS_DIR)/,,$(1))_$(2)

# Use this function to get the version of a go module from go.mod
version_gomod = $(shell go list -mod=mod -f '{{ .Version }}' -m $(1))

# This target cleans up any previous version files for the given tool and creates the given version file.
# This way, we can generically determine, which version was installed without calling each and every binary explicitly.
$(TOOLS_DIR)/.version_%:
	@mkdir -p  $(TOOLS_DIR)
	@version_file=$@; rm -f $${version_file%_*}*
	@touch $@

clean-tools:
	@rm -f $(GO_ADD_LICENSE) $(GO_OCB) $(TOOLS_DIR)/.version_*

create-tools: $(GO_ADD_LICENSE) $(GO_OCB)

#########################################
# Tools                                 #
#########################################

$(GO_ADD_LICENSE):  $(call tool_version_file,$(GO_ADD_LICENSE),$(GO_ADD_LICENSE_VERSION))
	GOBIN=$(abspath $(TOOLS_DIR)) go install github.com/google/addlicense@$(GO_ADD_LICENSE_VERSION)

$(GO_OCB): $(call tool_version_file,$(GO_OCB),$(GO_OCB_VERSION))
	GOBIN=$(abspath $(TOOLS_DIR)) go install go.opentelemetry.io/collector/cmd/builder@$(GO_OCB_VERSION)

.PHONY: create-tools clean-tools