package cli

import (
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

const (
	versionAnnotation      = "axi-sdk-go/cli/version"
	versionShortAnnotation = "axi-sdk-go/cli/version-short"
)

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// addVersionFlags advertises root --version and -V when unclaimed, plus -v
// when no module has already claimed that root shorthand. Execute handles
// --version and -V before Cobra parses flags, so module-owned root flags cannot
// shadow the required version fast path.
func (s *settings) addVersionFlags(root *cobra.Command) {
	f := root.Flags()
	pf := root.PersistentFlags()
	short := func(c string) string {
		if f.ShorthandLookup(c) != nil || pf.ShorthandLookup(c) != nil {
			return ""
		}
		return c
	}
	versionShort := short("v")
	root.Annotations[versionShortAnnotation] = "false"
	if versionShort == "v" {
		root.Annotations[versionShortAnnotation] = "true"
	}
	if f.Lookup("version") == nil && pf.Lookup("version") == nil {
		f.BoolP("version", versionShort, false, "print the version and exit")
	}
	// ponytail: pflag shorthands need a flag name, so -V is a hidden "--V" too.
	if f.Lookup("V") == nil && pf.Lookup("V") == nil && short("V") != "" {
		f.BoolP("V", "V", false, "print the version and exit")
		_ = f.MarkHidden("V")
	}
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
