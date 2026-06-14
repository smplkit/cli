package cmd

import (
	"fmt"
	"strings"

	smplkit "github.com/smplkit/go-sdk/v3"
	"github.com/spf13/cobra"

	"github.com/smplkit/cli/internal/paginate"
)

func registerServiceCmd(root *cobra.Command) {
	svc := &cobra.Command{
		Use:   "service",
		Short: "Manage service resources",
	}
	svc.AddCommand(serviceListCmd())
	svc.AddCommand(serviceGetCmd())
	svc.AddCommand(serviceCreateCmd())
	svc.AddCommand(serviceSetCmd())
	svc.AddCommand(serviceDeleteCmd())
	root.AddCommand(svc)
}

func serviceListCmd() *cobra.Command {
	var (
		limit  int
		all    bool
		search string
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List services",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := withClient()
			if err != nil {
				return err
			}
			ctx := cliContext(cmd)
			ns := client.Platform().Services()
			var svcs []*smplkit.Service
			if all {
				svcs, err = paginate.All(ctx, ns.List, limit)
			} else {
				svcs, err = paginate.Single(ctx, ns.List, limit)
			}
			if err != nil {
				return err
			}
			if search != "" {
				needle := strings.ToLower(search)
				filtered := make([]*smplkit.Service, 0, len(svcs))
				for _, s := range svcs {
					if strings.Contains(strings.ToLower(s.ID), needle) ||
						strings.Contains(strings.ToLower(s.Name), needle) {
						filtered = append(filtered, s)
					}
				}
				svcs = filtered
			}
			return renderer(cmd).RenderServices(svcs)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 0, "page size (server default when unset)")
	cmd.Flags().BoolVar(&all, "all", false, "fetch every page")
	cmd.Flags().StringVar(&search, "search", "", "case-insensitive substring match on id or name")
	return cmd
}

func serviceGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: "Get a service by id",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := withClient()
			if err != nil {
				return err
			}
			s, err := client.Platform().Services().Get(cliContext(cmd), args[0])
			if err != nil {
				return err
			}
			return renderer(cmd).RenderService(s)
		},
	}
}

func serviceCreateCmd() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "create <key>",
		Short: "Create a service",
		Args:  cobra.ExactArgs(1),
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
			s := client.Platform().Services().New(id, displayName)
			if err := s.Save(cliContext(cmd)); err != nil {
				return err
			}
			return renderer(cmd).RenderService(s)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "display name (defaults to the id)")
	return cmd
}

func serviceSetCmd() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "set <key>",
		Short: "Update a service (read-modify-write)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			client, err := withClient()
			if err != nil {
				return err
			}
			ctx := cliContext(cmd)
			s, err := client.Platform().Services().Get(ctx, id)
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("name") {
				s.Name = name
			}
			if err := s.Save(ctx); err != nil {
				return err
			}
			return renderer(cmd).RenderService(s)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "display name")
	return cmd
}

func serviceDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <key>",
		Short: "Delete a service",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if !confirm(cmd, yes, fmt.Sprintf("Delete service %q?", id)) {
				return fmt.Errorf("aborted")
			}
			client, err := withClient()
			if err != nil {
				return err
			}
			if err := client.Platform().Services().Delete(cliContext(cmd), id); err != nil {
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
