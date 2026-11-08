// Command echo is a sample AXI tool that has nothing to do with Kubernetes.
//
// It keeps key/value entries in a JSON file and shows how a non-Kubernetes
// tool uses the SDK: cli.New for the shared commands, output for rendering,
// axierr for exit codes, and mutate for safe changes. It is an example, not a
// released binary.
package main

import (
	"os"

	"github.com/lucashalbert/axi-sdk-go/cli"
	"github.com/lucashalbert/axi-sdk-go/update"
)

// Set with -ldflags in a real tool.
var version, commit, date = "dev", "", ""

func main() {
	info := update.Info{Version: version, Commit: commit, Date: date}
	os.Exit(cli.Execute(cli.New("echo", info, module{})))
}
