// Package cli composes an AXI command line tool from Modules.
//
// New returns a Cobra root command with the shared pieces already wired:
// persistent --json and --yaml flags, a version command, root --version, -v
// and -V flags, a checksum-verified update command, and a once-a-day update
// notice on stderr.
// Execute runs it, writes any error to stdout as a structured message in the
// selected format, and returns the exit code.
package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/lucashalbert/axi-sdk-go/axierr"
	"github.com/lucashalbert/axi-sdk-go/output"
	"github.com/lucashalbert/axi-sdk-go/update"
)

// Module contributes commands to the root command.
type Module interface {
	Register(root *cobra.Command)
}

// Option values are Modules that adjust New instead of adding commands.
type Option interface {
	Module
	apply(*settings)
}

type option func(*settings)

func (option) Register(*cobra.Command) {}
func (o option) apply(s *settings)     { o(s) }

// Repo sets the GitHub repository ("owner/name") that update and the update
// notice read releases from. By default it is derived from the module path
// of the running binary when that is a github.com path.
func Repo(ownerName string) Option { return option(func(s *settings) { s.repo = ownerName }) }

// EnvPrefix sets the prefix of the tool's environment variables, e.g.
// "K8S_AXI_". By default it is the upper-cased tool name with non-alphanumeric
// characters turned into "_", plus a trailing "_".
func EnvPrefix(prefix string) Option { return option(func(s *settings) { s.envPrefix = prefix }) }

const (
	checkTTL     = 24 * time.Hour
	checkTimeout = update.DefaultTimeout
)

type settings struct {
	name      string
	info      update.Info
	repo      string
	envPrefix string
	cacheFile string // test seam; empty means <user cache dir>/<name>/update-check.json

	check   func(context.Context, update.CheckConfig) (string, bool, error)
	install func(context.Context, update.InstallConfig) (string, bool, error)
}

// New builds the root command for tool name. Built-in commands are registered
// before the modules, so a module cannot shadow them.
//
// New switches on cobra.EnableTraverseRunHooks so the update notice (a root
// PersistentPostRun) still runs when a subcommand defines its own hooks.
func New(name string, info update.Info, mods ...Module) *cobra.Command {
	cobra.EnableTraverseRunHooks = true
	s := &settings{name: name, info: info, check: update.Check, install: update.Update}
	for _, m := range mods {
		if o, ok := m.(Option); ok {
			o.apply(s)
		}
	}
	if s.envPrefix == "" {
		s.envPrefix = defaultEnvPrefix(name)
	}
	if s.repo == "" {
		s.repo = repoFromBuildInfo()
	}

	root := &cobra.Command{
		Use:           name,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.CompletionOptions.HiddenDefaultCmd = true
	root.PersistentFlags().Bool("json", false, "output JSON")
	root.PersistentFlags().Bool("yaml", false, "output YAML")
	root.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		return axierr.Usage(c.CommandPath(), err.Error(), "run '"+c.CommandPath()+" --help' for usage")
	})
	root.PersistentPostRun = s.notice
	// The root needs a Run for PersistentPreRunE to fire on bare flags; help
	// is what a non-runnable root printed already.
	root.Run = func(c *cobra.Command, _ []string) { _ = c.Help() }
	root.PersistentPreRunE = s.versionFastPath

	root.AddCommand(s.versionCmd(), s.updateCmd())
	for _, m := range mods {
		if _, ok := m.(Option); !ok {
			m.Register(root)
		}
	}
	s.addVersionFlags(root)
	return root
}

// Format returns the output format selected by --json and --yaml on cmd.
// Using both is a usage error.
func Format(cmd *cobra.Command) (output.Format, error) {
	j, _ := cmd.Flags().GetBool("json")
	y, _ := cmd.Flags().GetBool("yaml")
	f, err := output.ParseFormat(j, y)
	if err != nil {
		return f, axierr.Usage(cmd.CommandPath(), err.Error(), "pass only one of --json and --yaml")
	}
	return f, nil
}

// Execute runs root, prints any error to stdout as a structured message in
// the selected format (the same stream and format as normal output, so the
// agent reads it), and returns the process exit code (0, 1 or 2).
// Nothing is written to stderr for errors.
// Cobra's own unknown-command and flag errors are usage errors.
func Execute(root *cobra.Command) int {
	err := root.Execute()
	if err == nil || errors.Is(err, errVersionPrinted) {
		return axierr.CodeOK
	}
	var ae *axierr.Error
	if !errors.As(err, &ae) {
		code := axierr.ExitCode(err)
		if strings.HasPrefix(err.Error(), "unknown command") {
			code = axierr.CodeUsage
		}
		ae = &axierr.Error{Op: root.Name(), Msg: err.Error(), Code: code, Err: err}
		if code == axierr.CodeUsage {
			ae.Hint = "run '" + root.Name() + " --help' for usage"
		}
	}
	f, perr := Format(root)
	if perr != nil {
		f = output.TOON
	}
	ae.Render(root.OutOrStdout(), f)
	return axierr.ExitCode(ae)
}

func defaultEnvPrefix(name string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(name) {
		if r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String() + "_"
}

func repoFromBuildInfo() string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		return repoFromModulePath(bi.Main.Path)
	}
	return ""
}

// repoFromModulePath turns "github.com/owner/name[/...]" into "owner/name".
func repoFromModulePath(p string) string {
	parts := strings.Split(p, "/")
	if len(parts) >= 3 && parts[0] == "github.com" {
		return parts[1] + "/" + parts[2]
	}
	return ""
}

func (s *settings) cachePath() string {
	if s.cacheFile != "" {
		return s.cacheFile
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, s.name, "update-check.json")
}

// noNotice commands never print the notice: update already says it, the rest
// are help and shell-completion plumbing.
var noNotice = map[string]bool{"update": true, "help": true, "completion": true, "__complete": true, "__completeNoDesc": true}

// notice prints a one-line update hint on stderr. It never changes the
// command's result: errors are dropped and the lookup is bounded by
// checkTimeout and cached for checkTTL.
func (s *settings) notice(cmd *cobra.Command, _ []string) {
	if s.repo == "" || noNotice[cmd.Name()] || s.updateCheckDisabled() {
		return
	}
	cache := s.cachePath()
	if cache == "" {
		return // without a cache every run would pay for a lookup
	}
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	latest, newer, err := s.check(ctx, update.CheckConfig{
		Repo: s.repo, Current: s.info.Version, CacheFile: cache, TTL: checkTTL, Timeout: checkTimeout,
	})
	if err != nil || !newer {
		return
	}
	_, _ = cmd.ErrOrStderr().Write([]byte(s.name + " " + latest + " is available (running " +
		strings.TrimPrefix(s.info.Version, "v") + "); run '" + s.name + " update' to install it\n"))
}

func (s *settings) updateCheckDisabled() bool {
	v := strings.ToLower(os.Getenv(s.envPrefix + "NO_UPDATE_CHECK"))
	return v != "" && v != "0" && v != "false"
}
