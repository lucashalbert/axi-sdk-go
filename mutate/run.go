package mutate

import (
	"context"
	"errors"
	"io"

	"github.com/lucashalbert/axi-sdk-go/axierr"
	"github.com/lucashalbert/axi-sdk-go/output"
	"github.com/lucashalbert/axi-sdk-go/redact"
)

// Options configures Run.
type Options struct {
	Plan Plan
	Auth Authorizer
	Mode Mode
	// Format and Out select where and how the plan is rendered.
	Format output.Format
	Out    io.Writer
	// Validate is optional. In DryRun mode it checks the change without
	// making it, e.g. a server-side dry-run request. It is never called in
	// Apply mode.
	Validate func(context.Context) error
	// Apply performs the change. It is only called in Apply mode, after the
	// plan is printed and the change is authorized.
	Apply func(context.Context) error
}

// planDoc is the rendered form of a Plan, with every string masked.
type planDoc struct {
	Operation   string            `json:"operation"`
	Target      string            `json:"target"`
	Scope       map[string]string `json:"scope,omitempty"`
	Changes     []string          `json:"changes"`
	Destructive bool              `json:"destructive"`
	Mode        string            `json:"mode"`
	NoOp        bool              `json:"noop,omitempty"`
	Confirm     string            `json:"confirm,omitempty"`
}

func render(p Plan, mode Mode) map[string]planDoc {
	d := planDoc{
		Operation:   redact.Mask(p.Operation),
		Target:      redact.Mask(p.Target),
		Changes:     make([]string, len(p.Changes)),
		Destructive: p.Destructive,
		Mode:        mode.String(),
		NoOp:        len(p.Changes) == 0,
	}
	for i, c := range p.Changes {
		d.Changes[i] = redact.Mask(c)
	}
	if len(p.Scope) > 0 {
		d.Scope = make(map[string]string, len(p.Scope))
		for k, v := range p.Scope {
			d.Scope[redact.Mask(k)] = redact.Mask(v)
		}
	}
	if p.Destructive {
		d.Confirm = d.Target
	}
	return map[string]planDoc{"plan": d}
}

// Run authorizes o.Plan, prints it, then carries it out according to o.Mode.
//
// Authorization failures are returned before anything is printed or run.
// A plan with no changes is printed as a no-op and nothing is called.
// Callback errors are returned as-is if they are already *axierr.Error,
// otherwise as runtime errors (exit code 1) naming the operation and target.
func Run(ctx context.Context, o Options) error {
	if err := o.Auth.Authorize(o.Plan, o.Mode); err != nil {
		return err
	}
	if o.Mode == Apply && o.Apply == nil && len(o.Plan.Changes) > 0 {
		return axierr.Runtime(o.Plan.Operation, o.Plan.Target, errors.New("no apply callback"),
			"this is a bug in the tool, not in the command line")
	}
	if err := ctx.Err(); err != nil {
		return fail(o.Plan, err)
	}
	if err := output.Render(o.Out, render(o.Plan, o.Mode), o.Format); err != nil {
		return fail(o.Plan, err)
	}
	if len(o.Plan.Changes) == 0 {
		return nil
	}
	step := o.Apply
	if o.Mode == DryRun {
		step = o.Validate
	}
	if step == nil {
		return nil
	}
	if err := step(ctx); err != nil {
		return fail(o.Plan, err)
	}
	return nil
}

func fail(p Plan, err error) error {
	var ae *axierr.Error
	if errors.As(err, &ae) {
		return err
	}
	return axierr.Runtime(p.Operation, p.Target, err, "")
}
