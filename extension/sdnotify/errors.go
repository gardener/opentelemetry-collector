package sdnotify

import "errors"

var (
	errSIGHUP = errors.New("sdnotify: SIGHUP received, exiting to trigger supervisor restart")
)
