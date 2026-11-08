# axi-sdk-go

Go SDK for building agent-ergonomic (AXI) command line tools.

It holds the parts every AXI tool shares, so a tool only supplies its own domain commands:

- `output`: compact TOON output by default, with JSON and YAML alternatives.
- `axierr`: structured errors with stable exit codes (0 success, 1 runtime error, 2 usage error).
- `redact`: helpers that keep tokens and secrets out of output.
- `update`: version reporting, a cached once-a-day update check and checksum-verified self-update from GitHub Releases.
- `setup`: installers for an agent skill and a session-start hook.
- `mutate`: plan, authorize and execute model for commands that change state, with confirm tokens for destructive operations and dry-run.
- `cli`: command composition on top of Cobra, adding `version`, `update` and the update notice.

The SDK has no Kubernetes dependency.
CI fails if any package imports `k8s.io/*`.

## Usage

```go
func main() {
	info := update.Info{Version: version, Commit: commit, Date: date} // set with -ldflags
	os.Exit(cli.Execute(cli.New("my-axi", info, myModule{})))
}
```

`cli.New` adds `--json` and `--yaml` persistent flags, `version`, `update` and a once-a-day update notice on stderr.
The release repository defaults to the binary's `github.com/owner/name` module path, override it with `cli.Repo`.
`MY_AXI_NO_UPDATE_CHECK=1` turns the notice off, and `cli.EnvPrefix` changes the prefix.
`update` expects GoReleaser-style archives (`name_version_os_arch.tar.gz`, `.zip` on Windows) for Linux, macOS and Windows on amd64 or arm64, plus `checksums.txt`.

## Mutations

`mutate` gives every mutating command the same safety contract.
A command describes the change as a `mutate.Plan` (operation, resolved target, scope labels such as cluster and namespace, human-readable changes, destructive or not) and hands it to `mutate.Run` with a callback that performs the change.

```go
plan := mutate.Plan{Operation: "delete", Target: "deployment/web", Destructive: true,
	Scope: map[string]string{"context": "prod", "namespace": "default"}, Changes: []string{"delete deployment/web"}}
err := mutate.Run(ctx, mutate.Options{
	Plan: plan, Auth: mutate.Authorizer{Confirm: confirmFlag}, Mode: mode, Format: f, Out: w,
	Validate: serverSideDryRun, // optional, called in DryRun mode only
	Apply:    doDelete,         // called in Apply mode only
})
```

- A destructive plan needs a token equal to `Target`. A missing or different token is a usage error (exit 2) whose hint names the exact token to pass.
- Other mutations need no token. The plan, target first, is printed before `Apply` runs.
- `DryRun` never calls `Apply`, needs no token and shows the token a destructive plan expects.
- A plan with no changes is reported as `noop` and nothing is called.
- Plan output is TOON, JSON or YAML, and all plan and error text is passed through `redact`.

`examples/echo` is a small non-Kubernetes tool (a key/value file) built this way. It is an example only and is not released.

## Install

```sh
go get github.com/lucashalbert/axi-sdk-go
```

## Development

```sh
go vet ./...
go test -race ./...
scripts/check-no-k8s.sh
```

## Releases

Releases are cut by semantic-release from conventional commits on `main`.
The project stays on `0.x`: a breaking change bumps the minor version until `1.0` is declared.
Pull request titles must be conventional commits, and they become the squash-merge commit message.

## License

BSD 3-Clause, see [LICENSE](LICENSE).
