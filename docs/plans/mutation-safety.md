# Mutation safety and sample module

## Goal

Give AXI tools one small, generic way to plan, authorize and execute a change.
k8s-axi will use it for plan, scale, rollout restart, apply and delete.
A future argocd-axi can use it unchanged.
The package has no Kubernetes dependency, and the existing `scripts/check-no-k8s.sh` check stays green.

## Package `mutate`

Exported API, kept small:

- `Plan`: operation, target identity, scope labels (for example cluster context and namespace), human-readable changes, destructive flag.
- `Mode`: `Apply` or `DryRun`.
- `Authorizer`: holds the caller-supplied confirm token and the flag name used in hints.
  `Authorize(Plan, Mode)` is the only policy decision point.
- `Options` and `Run`: authorize the plan, render it, then call the consumer's callbacks.

Contracts, from the design and PRD:

- A destructive plan applied for real needs a token equal to the resolved target identity, exactly (no trimming, no case folding).
  A missing or mismatched token is a usage error (exit 2) built with `axierr.Usage`, and its hint names the exact token expected.
- Other mutations need no token.
  `Run` prints the plan, target first, before it executes anything.
- `DryRun` never calls `Apply`.
  It calls the optional `Validate` callback (server-side dry-run where the consumer supports it) and reports the plan.
  It needs no token, so an agent can preview a destructive change and read the token from the output.
- A plan with no changes is reported as a no-op and nothing is called.
- Plan output goes through `output.Render` (TOON, JSON, YAML).
  Every string in the plan and every error passes through `redact.Mask`.
- Callback failures become runtime errors (exit 1), unless they already carry an `axierr.Error`.

## Sample module `examples/echo`

A package `main` in the SDK module, built with `cli.New`.
It uses only dependencies the SDK already has, so the dependency graph and the no-k8s check cover it with no extra CI wiring.
It is not a release artifact: the repository has no release build for binaries.

- `echo get <key>`: read command.
- `echo set <key> <value>` and `echo delete <key>`: mutating commands backed by a JSON file, using `mutate`.

End-to-end tests build the binary and run it, covering output formats, dry-run, token errors and redaction.

## Tasks

1. Plan doc (this file).
2. `mutate`: Plan, Mode, Authorizer (tests first).
3. `mutate`: Run and plan rendering (tests first).
4. `examples/echo` with end-to-end tests.
5. README.
