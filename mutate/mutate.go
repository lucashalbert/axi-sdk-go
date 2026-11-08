// Package mutate is the shared safety model for commands that change state.
//
// A change is described as a Plan, authorized by an Authorizer and carried
// out by Run. The package is generic: it knows nothing about what is being
// changed, so Kubernetes tools, Argo CD tools and others share one contract.
//
//   - A destructive plan applied for real needs a confirm token equal to the
//     plan's resolved Target. A missing or mismatched token is a usage error
//     (exit 2) whose hint names the exact token expected.
//   - Other mutations need no token, and Run prints the plan, target first,
//     before it changes anything.
//   - DryRun never calls the apply callback.
//   - All plan and error text is masked with package redact.
package mutate

import (
	"github.com/lucashalbert/axi-sdk-go/axierr"
)

// Mode selects whether Run changes anything.
type Mode int

// Modes. The zero value is Apply.
const (
	Apply Mode = iota
	DryRun
)

func (m Mode) String() string {
	if m == DryRun {
		return "dry-run"
	}
	return "apply"
}

// Plan describes an intended change.
type Plan struct {
	// Operation is the verb being performed, e.g. "scale" or "delete".
	Operation string `json:"operation"`
	// Target is the resolved identity of what changes, e.g. "deployment/web".
	// For a destructive plan it is also the confirm token.
	Target string `json:"target"`
	// Scope holds labels that bound the change, e.g. cluster context and
	// namespace. Consumers decide the keys.
	Scope map[string]string `json:"scope,omitempty"`
	// Changes lists the changes in human-readable form. An empty list means
	// the desired state already holds.
	Changes []string `json:"changes"`
	// Destructive plans need a matching confirm token to be applied.
	Destructive bool `json:"destructive"`
}

// Authorizer decides whether a plan may run.
type Authorizer struct {
	// Confirm is the token the caller supplied, e.g. the value of --confirm.
	Confirm string
	// Flag is the flag name shown in hints. Default "--confirm".
	Flag string
}

// Authorize returns nil if p may run in mode. Dry runs change nothing and
// never need a token. Errors are usage errors (exit code 2).
func (a Authorizer) Authorize(p Plan, mode Mode) error {
	if p.Operation == "" || p.Target == "" {
		return axierr.Usage(orDefault(p.Operation, "mutate"), "plan needs an operation and a target",
			"set both Operation and Target on the plan")
	}
	if !p.Destructive || mode == DryRun {
		return nil
	}
	flag := orDefault(a.Flag, "--confirm")
	hint := "pass " + flag + " " + p.Target
	e := axierr.Usage(p.Operation, "", hint)
	e.Target = p.Target
	switch a.Confirm {
	case p.Target:
		return nil
	case "":
		e.Msg = "destructive operation requires confirmation"
	default:
		e.Msg = "confirm token does not match the target"
	}
	return e
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
