// Package logx is the single logging entry point. Everything it emits goes to
// stderr, keeping stdout clean for JSON data (CLI convention) and guaranteeing
// serve mode never corrupts a transport with stray bytes.
package logx

import (
	"fmt"
	"os"
)

// Verbose toggles Debug output. Off by default.
var Verbose = false

// Infof writes an informational line to stderr.
func Infof(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
}

// Debugf writes only when Verbose is set.
func Debugf(format string, a ...any) {
	if !Verbose {
		return
	}
	fmt.Fprintf(os.Stderr, "[debug] "+format+"\n", a...)
}

// Errorf writes an error line to stderr.
func Errorf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "[error] "+format+"\n", a...)
}
