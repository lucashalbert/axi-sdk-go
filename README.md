# axi-sdk-go

Go SDK for building agent-ergonomic (AXI) command line tools.

It holds the parts every AXI tool shares, so a tool only supplies its own domain commands:

- `output`: compact TOON output by default, with JSON and YAML alternatives.
- `axierr`: structured errors with stable exit codes (0 success, 1 runtime error, 2 usage error).
- `redact`: helpers that keep tokens and secrets out of output.
- `update`: version reporting, a cached once-a-day update check and checksum-verified self-update from GitHub Releases.
- `setup`: installers for an agent skill and a session-start hook.
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
`update` expects GoReleaser-style archives (`name_version_os_arch.tar.gz`, `.zip` on Windows) plus `checksums.txt`.

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
