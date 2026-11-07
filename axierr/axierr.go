// Package axierr is the shared error model: short, structured, safe to print.
//
// Every error names the failed operation, the target, and a remediation
// hint. Rendering and Error() pass all text through redact.Mask, so a
// credential that ends up in a wrapped error never reaches the terminal.
//
// Exit codes: 0 success, 1 runtime or API error, 2 usage error. Other error
// types opt in to a code by implementing `ExitCode() int`; ExitCode finds it
// anywhere in the wrap chain.
package axierr

import (
	"errors"
	"io"
	"strings"

	"github.com/lucashalbert/axi-sdk-go/output"
	"github.com/lucashalbert/axi-sdk-go/redact"
)

// Exit codes.
const (
	CodeOK      = 0
	CodeRuntime = 1
	CodeUsage   = 2
)

// Error is a structured, user-facing error.
type Error struct {
	Op     string // what was being attempted, e.g. "cluster info"
	Target string // what it was attempted on, e.g. "pod/web"
	Msg    string // what went wrong
	Hint   string // how to fix or retry
	Code   int    // exit code; 0 is treated as CodeRuntime
	Err    error  // underlying cause, never rendered separately
}

// Usage returns a usage error (exit code 2).
func Usage(op, msg, hint string) *Error {
	return &Error{Op: op, Msg: msg, Hint: hint, Code: CodeUsage}
}

// Runtime returns a runtime or API error (exit code 1) caused by err.
func Runtime(op, target string, err error, hint string) *Error {
	msg := "unknown error"
	if err != nil {
		msg = err.Error()
	}
	return &Error{Op: op, Target: target, Msg: msg, Hint: hint, Code: CodeRuntime, Err: err}
}

// Error returns "op target: msg" with secrets masked.
func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	head := strings.TrimSpace(e.Op + " " + e.Target)
	if head == "" {
		return redact.Mask(e.Msg)
	}
	return redact.Mask(head + ": " + e.Msg)
}

// Unwrap returns the underlying cause.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// ExitCode maps err to a process exit code: 0 for nil, the code of the
// first *Error or ExitCode() method in the chain, otherwise 1.
func ExitCode(err error) int {
	if err == nil {
		return CodeOK
	}
	var ae *Error
	if errors.As(err, &ae) && ae != nil && ae.Code > 0 {
		return ae.Code
	}
	var ec interface{ ExitCode() int }
	if errors.As(err, &ec) {
		if c := ec.ExitCode(); c > 0 {
			return c
		}
	}
	return CodeRuntime
}

type body struct {
	Op      string `json:"op,omitempty"`
	Target  string `json:"target,omitempty"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
	Exit    int    `json:"exit"`
}

// Render writes the error to w in format f, with every field masked.
// It is best effort: a failing writer cannot be reported anywhere useful.
func (e *Error) Render(w io.Writer, f output.Format) {
	if e == nil {
		return
	}
	_ = output.Render(w, map[string]body{"error": {
		Op:      redact.Mask(e.Op),
		Target:  redact.Mask(e.Target),
		Message: redact.Mask(e.Msg),
		Hint:    redact.Mask(e.Hint),
		Exit:    ExitCode(e),
	}}, f)
}
