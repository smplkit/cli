package cmd

import (
	"fmt"
	"strings"

	smplkit "github.com/smplkit/go-sdk/v3"
	"github.com/spf13/cobra"

	"github.com/smplkit/cli/internal/paginate"
)

func registerEnvCmd(root *cobra.Command) {
	env := &cobra.Command{
		Use:   "env",
		Short: "Manage environments",
	}
	env.AddCommand(envListCmd())
	env.AddCommand(envGetCmd())
	env.AddCommand(envCreateCmd())
	env.AddCommand(envSetCmd())
	env.AddCommand(envDeleteCmd())
	root.AddCommand(env)
}

func envListCmd() *cobra.Command {
	var (
		limit          int
		all            bool
		search         string
		classification string
		managed        bool
		unmanaged      bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List environments",
		Long: "Lists environments. --search/--classification/--managed/--unmanaged are client-side\n" +
			"filters applied to the returned page(s); use --all to scan the whole account.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if managed && unmanaged {
				return fmt.Errorf("--managed and --unmanaged are mutually exclusive")
			}
			client, err := withClient()
			if err != nil {
				return err
			}
			ctx := cliContext(cmd)
			ns := client.Platform().Environments()
			var envs []*smplkit.Environment
			if all {
				envs, err = paginate.All(ctx, ns.List, limit)
			} else {
				envs, err = paginate.Single(ctx, ns.List, limit)
			}
			if err != nil {
				return err
			}
			envs = filterEnvironments(envs, search, classification, managed, unmanaged)
			return renderer(cmd).RenderEnvironments(envs)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 0, "page size (server default when unset)")
	cmd.Flags().BoolVar(&all, "all", false, "fetch every page")
	cmd.Flags().StringVar(&search, "search", "", "case-insensitive substring match on id or name")
	cmd.Flags().StringVar(&classification, "classification", "", "filter by classification: STANDARD | AD_HOC")
	cmd.Flags().BoolVar(&managed, "managed", false, "(reserved) — server doesn't expose a managed filter; included for parity")
	cmd.Flags().BoolVar(&unmanaged, "unmanaged", false, "(reserved) — server doesn't expose a managed filter; included for parity")
	return cmd
}

func filterEnvironments(in []*smplkit.Environment, search, classification string, _, _ bool) []*smplkit.Environment {
	if search == "" && classification == "" {
		return in
	}
	classification = strings.ToUpper(classification)
	out := make([]*smplkit.Environment, 0, len(in))
	for _, e := range in {
		if search != "" {
			needle := strings.ToLower(search)
			if !strings.Contains(strings.ToLower(e.ID), needle) &&
				!strings.Contains(strings.ToLower(e.Name), needle) {
				continue
			}
		}
		if classification != "" && string(e.Classification) != classification {
			continue
		}
		out = append(out, e)
	}
	return out
}

func envGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: "Get an environment by id",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := withClient()
			if err != nil {
				return err
			}
			e, err := client.Platform().Environments().Get(cliContext(cmd), args[0])
			if err != nil {
				return err
			}
			return renderer(cmd).RenderEnvironment(e)
		},
	}
}

func envCreateCmd() *cobra.Command {
	var (
		name  string
		color string
	)
	cmd := &cobra.Command{
		Use:   "create <key>",
		Short: "Create an environment",
		Long: "Creates a STANDARD environment with managed=true (the server-controlled defaults; see\n" +
			"ADR-051). Requires the platform.managed_environments entitlement — a 402 from the\n" +
			"server is surfaced as-is so the upgrade path is visible.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			displayName := name
			if displayName == "" {
				displayName = id
			}
			client, err := withClient()
			if err != nil {
				return err
			}
			ns := client.Platform().Environments()
			opts := []smplkit.EnvironmentOption{}
			if color != "" {
				opts = append(opts, smplkit.WithEnvironmentColor(color))
			}
			e := ns.New(id, displayName, opts...)
			if err := e.Save(cliContext(cmd)); err != nil {
				return err
			}
			return renderer(cmd).RenderEnvironment(e)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "display name (defaults to the id)")
	cmd.Flags().StringVar(&color, "color", "", "hex color (e.g. #ef4444)")
	return cmd
}

func envSetCmd() *cobra.Command {
	var (
		name       string
		color      string
		clearColor bool
	)
	cmd := &cobra.Command{
		Use:   "set <key>",
		Short: "Update an environment (read-modify-write)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			client, err := withClient()
			if err != nil {
				return err
			}
			ctx := cliContext(cmd)
			e, err := client.Platform().Environments().Get(ctx, id)
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("name") {
				e.Name = name
			}
			if cmd.Flags().Changed("color") {
				if clearColor {
					return fmt.Errorf("--color and --clear-color are mutually exclusive")
				}
				c := color
				e.Color = &c
			}
			if clearColor {
				e.Color = nil
			}
			if err := e.Save(ctx); err != nil {
				return err
			}
			return renderer(cmd).RenderEnvironment(e)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "display name")
	cmd.Flags().StringVar(&color, "color", "", "hex color (e.g. #ef4444)")
	cmd.Flags().BoolVar(&clearColor, "clear-color", false, "clear the color")
	return cmd
}

func envDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <key>",
		Short: "Delete an environment",
		Long: "Deletes an environment. The SDK's Delete is non-cascading; the server refuses to\n" +
			"delete an environment that still has dependent resources.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if !confirm(cmd, yes, fmt.Sprintf("Delete environment %q?", id)) {
				return fmt.Errorf("aborted")
			}
			client, err := withClient()
			if err != nil {
				return err
			}
			if err := client.Platform().Environments().Delete(cliContext(cmd), id); err != nil {
				return err
			}
			if globals.Quiet {
				fmt.Fprintln(cmd.OutOrStdout(), id)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}
