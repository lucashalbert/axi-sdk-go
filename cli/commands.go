package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/lucashalbert/axi-sdk-go/axierr"
	"github.com/lucashalbert/axi-sdk-go/output"
	"github.com/lucashalbert/axi-sdk-go/update"
)

type versionOut struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// errVersionPrinted stops the root command after a version flag was answered.
var errVersionPrinted = errors.New("version printed")

// addVersionFlags gives the root --version and -V, plus -v when no module has
// already claimed that root shorthand. They are local to the root, so
// subcommands keep their own -v/-V, and only a module-owned root -v is left
// alone.
func (s *settings) addVersionFlags(root *cobra.Command) {
	f := root.Flags()
	pf := root.PersistentFlags()
	if f.Lookup("version") != nil || pf.Lookup("version") != nil {
		panic("cli: root flag --version is reserved")
	}
	if f.Lookup("V") != nil || pf.Lookup("V") != nil ||
		f.ShorthandLookup("V") != nil || pf.ShorthandLookup("V") != nil {
		panic("cli: root flag -V is reserved")
	}
	short := func(c string) string {
		if f.ShorthandLookup(c) != nil || pf.ShorthandLookup(c) != nil {
			return ""
		}
		return c
	}
	f.BoolP("version", short("v"), false, "print the version and exit")
	// ponytail: pflag shorthands need a flag name, so -V is a hidden "--V" too.
	f.BoolP("V", "V", false, "print the version and exit")
	_ = f.MarkHidden("V")
}

// versionFastPath prints the bare version when a root version flag is set.
func (s *settings) versionFastPath(cmd *cobra.Command, _ []string) error {
	if cmd.HasParent() {
		return nil
	}
	for _, n := range []string{"version", "V"} {
		if v, err := cmd.Flags().GetBool(n); err == nil && v {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), orDefault(s.info.Version, "dev"))
			if err != nil {
				return err
			}
			return errVersionPrinted
		}
	}
	return nil
}

func (s *settings) versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version, commit and build date",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f, err := Format(cmd)
			if err != nil {
				return err
			}
			return output.Render(cmd.OutOrStdout(), versionOut{
				Version: orDefault(s.info.Version, "dev"),
				Commit:  orDefault(s.info.Commit, "unknown"),
				Date:    orDefault(s.info.Date, "unknown"),
			}, f)
		},
	}
}

type updateOut struct {
	Current string `json:"current"`
	Latest  string `json:"latest"`
	Updated bool   `json:"updated"`
	Message string `json:"message"`
}

func (s *settings) updateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "update",
		Short: "Install the latest release, verifying its SHA-256 checksum",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f, err := Format(cmd)
			if err != nil {
				return err
			}
			if s.repo == "" {
				return axierr.Usage("update", "no release repository is configured for "+s.name,
					"build with a github.com module path or pass cli.Repo(\"owner/name\")")
			}
			latest, updated, err := s.install(cmd.Context(), update.InstallConfig{
				Repo: s.repo, Binary: s.name, Current: s.info.Version,
			})
			if err != nil {
				return axierr.Runtime("update", s.repo, err,
					"the running binary was not changed; retry, or download a release from https://github.com/"+s.repo+"/releases")
			}
			msg := "already up to date"
			if updated {
				msg = "installed " + latest
			}
			return output.Render(cmd.OutOrStdout(), updateOut{
				Current: orDefault(s.info.Version, "dev"), Latest: latest, Updated: updated, Message: msg,
			}, f)
		},
	}
}
