// Package terminal encapsulates some platform-specific logic for detecting
// if we're running in a terminal and, if so, properly configuring that
// terminal to meet the assumptions that the rest of Terraform makes.
//
// Specifically, Terraform requires a Terminal which supports virtual terminal
// sequences and which accepts UTF-8-encoded text.
//
// This is an abstraction only over the platform-specific detection of and
// possibly initialization of terminals. It's not intended to provide
// higher-level abstractions of the sort provided by packages like termcap or
// curses; ultimately we just assume that terminals are "standard" VT100-like
// terminals and use a subset of control codes that works across the various
// platforms we support. Our approximate target is "xterm-compatible"
// virtual terminals.
package terminal

import (
	"os"
)

// Streams represents a collection of three streams that each may or may not
// be connected to a terminal.
//
// If a stream is connected to a terminal then there are more possibilities
// available, such as detecting the current terminal width. If we're connected
// to something else, such as a pipe or a file on disk, the stream will
// typically provide placeholder values or do-nothing stubs for
// terminal-requiring operatons.
//
// Note that it's possible for only a subset of the streams to be connected
// to a terminal. For example, this happens if the user runs Terraform with
// I/O redirection where Stdout might refer to a regular disk file while Stderr
// refers to a terminal, or various other similar combinations.
type Streams struct {
	Stdout *OutputStream
	Stderr *OutputStream
	Stdin  *InputStream
}

// Init tries to initialize a terminal, if Terraform is running in one, and
// returns an object describing what it was able to set up.
//
// An error for this function indicates that the current execution context
// can't meet Terraform's assumptions. For example, on Windows Init will return
// an error if Terraform is running in a Windows Console that refuses to
// activate UTF-8 mode, which can happen if we're running on an unsupported old
// version of Windows.
//
// Note that the success of this function doesn't mean that we're actually
// running in a terminal. It could also represent successfully detecting that
// one or more of the input/output streams is not a terminal.
func Init() (*Streams, error) {
	// These configure* functions are platform-specific functions in other
	// files that use //+build constraints to vary based on target OS.

	stderr, err := configureOutputHandle(os.Stderr)
	if err != nil {
		return nil, err
	}
	stdout, err := configureOutputHandle(os.Stdout)
	if err != nil {
		return nil, err
	}
	stdin, err := configureInputHandle(os.Stdin)
	if err != nil {
		return nil, err
	}

	return &Streams{
		Stdout: stdout,
		Stderr: stderr,
		Stdin:  stdin,
	}, nil
}
