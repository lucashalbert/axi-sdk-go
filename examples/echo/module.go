package main

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"

	"github.com/spf13/cobra"

	"github.com/lucashalbert/axi-sdk-go/axierr"
	"github.com/lucashalbert/axi-sdk-go/cli"
	"github.com/lucashalbert/axi-sdk-go/mutate"
	"github.com/lucashalbert/axi-sdk-go/output"
)

// module is the tool's cli.Module: one read command and two mutating ones.
type module struct{}

type entry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func (module) Register(root *cobra.Command) {
	root.PersistentFlags().String("file", envOr("ECHO_FILE", "echo.json"), "state file")

	root.AddCommand(&cobra.Command{
		Use:   "get <key>",
		Short: "Print an entry",
		Args:  cobra.ExactArgs(1),
		RunE:  get,
	})

	set := &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Create or change an entry",
		Args:  cobra.ExactArgs(2),
		RunE:  set,
	}
	set.Flags().Bool("dry-run", false, "show the plan without changing anything")
	root.AddCommand(set)

	del := &cobra.Command{
		Use:   "delete <key>",
		Short: "Delete an entry (needs --confirm entry/<key>)",
		Args:  cobra.ExactArgs(1),
		RunE:  del,
	}
	del.Flags().Bool("dry-run", false, "show the plan without changing anything")
	del.Flags().String("confirm", "", "token that must equal the target, entry/<key>")
	root.AddCommand(del)
}

func get(cmd *cobra.Command, args []string) error {
	f, err := cli.Format(cmd)
	if err != nil {
		return err
	}
	state, err := load(cmd)
	if err != nil {
		return err
	}
	v, ok := state[args[0]]
	if !ok {
		return &axierr.Error{
			Op: "get", Target: target(args[0]), Msg: "not found", Code: axierr.CodeRuntime,
			Hint: "list the file's keys with 'cat " + path(cmd) + "'",
		}
	}
	return output.Render(cmd.OutOrStdout(), entry{Key: args[0], Value: v}, f)
}

func set(cmd *cobra.Command, args []string) error {
	key, value := args[0], args[1]
	state, err := load(cmd)
	if err != nil {
		return err
	}
	plan := mutate.Plan{Operation: "set", Target: target(key), Scope: scope(cmd)}
	if old, ok := state[key]; !ok {
		plan.Changes = []string{"create " + key + " = " + value}
	} else if old != value {
		plan.Changes = []string{"change " + key + ": " + old + " -> " + value}
	}
	return run(cmd, plan, mutate.Authorizer{}, func() error {
		state[key] = value
		return save(cmd, state)
	})
}

func del(cmd *cobra.Command, args []string) error {
	key := args[0]
	state, err := load(cmd)
	if err != nil {
		return err
	}
	plan := mutate.Plan{Operation: "delete", Target: target(key), Scope: scope(cmd), Destructive: true}
	if _, ok := state[key]; ok {
		plan.Changes = []string{"delete " + key}
	}
	confirm, _ := cmd.Flags().GetString("confirm")
	return run(cmd, plan, mutate.Authorizer{Confirm: confirm}, func() error {
		delete(state, key)
		return save(cmd, state)
	})
}

func run(cmd *cobra.Command, plan mutate.Plan, auth mutate.Authorizer, apply func() error) error {
	f, err := cli.Format(cmd)
	if err != nil {
		return err
	}
	mode := mutate.Apply
	if dry, _ := cmd.Flags().GetBool("dry-run"); dry {
		mode = mutate.DryRun
	}
	return mutate.Run(cmd.Context(), mutate.Options{
		Plan: plan, Auth: auth, Mode: mode, Format: f, Out: cmd.OutOrStdout(),
		Apply: func(context.Context) error { return apply() },
	})
}

func target(key string) string { return "entry/" + key }

func scope(cmd *cobra.Command) map[string]string { return map[string]string{"file": path(cmd)} }

func path(cmd *cobra.Command) string {
	p, _ := cmd.Flags().GetString("file")
	return p
}

func load(cmd *cobra.Command) (map[string]string, error) {
	state := map[string]string{}
	b, err := os.ReadFile(path(cmd)) //nolint:gosec // the state file path is the --file flag by design
	if errors.Is(err, fs.ErrNotExist) {
		return state, nil
	}
	if err == nil {
		err = json.Unmarshal(b, &state)
	}
	if err != nil {
		return nil, axierr.Runtime("load", path(cmd), err, "fix or remove the state file")
	}
	return state, nil
}

func save(cmd *cobra.Command, state map[string]string) error {
	b, err := json.MarshalIndent(state, "", "  ")
	if err == nil {
		err = os.WriteFile(path(cmd), append(b, '\n'), 0o600)
	}
	if err != nil {
		return axierr.Runtime("save", path(cmd), err, "check the file is writable")
	}
	return nil
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}
