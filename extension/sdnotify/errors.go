// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package sdnotify

import "errors"

var errSIGHUP = errors.New("sdnotify: SIGHUP received, exiting to trigger supervisor restart")
