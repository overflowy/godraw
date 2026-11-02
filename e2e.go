//go:build e2e

package main

// An end-to-end build (go build -tags e2e) can be driven from another
// process when MYGO_AUTOMATION names a socket.
import _ "github.com/egoist/mygo/plugins/automation"
