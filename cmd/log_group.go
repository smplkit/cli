package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	smplkit "github.com/smplkit/go-sdk/v3"
	"github.com/spf13/cobra"

	"github.com/smplkit/cli/internal/paginate"
)

type logGroupFileShape struct {
	ID     string  `json:"id,omitempty"`
	Name   string  `json:"name,omitempty"`
	Level  *string `json:"level,omitempty"`
	Parent *string `json:"parent,omitempty"`
}

func registerLogGroupCmd(root *cobra.Command) {
	lg := &cobra.Command{
		Use:   "log-group",
		Short: "Manage log group resources",
	}
	lg.AddCommand(logGroupListCmd())
	lg.AddCommand(logGroupGetCmd())
	lg.AddCommand(logGroupCreateCmd())
	lg.AddCommand(logGroupSetCmd())
	lg.AddCommand(logGroupDeleteCmd())
	root.AddCommand(lg)
}

func logGroupListCmd() *cobra.Command {
	var (
		limit int
		all   bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List log groups",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := withClient()
			if err != nil {
				return err
			}
			ctx := cliContext(cmd)
			ns := client.Logging().LogGroups()
			var groups []*smplkit.LogGroup
			if all {
				groups, err = paginate.All(ctx, ns.List, limit)
			} else {
				groups, err = paginate.Single(ctx, ns.List, limit)
			}
			if err != nil {
				return err
			}
			return renderer(cmd).RenderLogGroups(groups)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 0, "page size (server default when unset)")
	cmd.Flags().BoolVar(&all, "all", false, "fetch every page")
	return cmd
}

func logGroupGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: "Get a log group by id",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := withClient()
			if err != nil {
				return err
			}
			g, err := client.Logging().LogGroups().Get(cliContext(cmd), args[0])
			if err != nil {
				return err
			}
			return renderer(cmd).RenderLogGroup(g)
		},
	}
}

func logGroupCreateCmd() *cobra.Command {
	var (
		name   string
		level  string
		parent string
		file   string
	)
	cmd := &cobra.Command{
		Use:   "create <key>",
		Short: "Create a log group",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			client, err := withClient()
			if err != nil {
				return err
			}
			shape, err := loadLogGroupFile(file)
			if err != nil {
				return err
			}

			effName := firstNonEmpty(name, shape.Name)
			effParent := firstNonEmpty(parent, ptrString(shape.Parent))
			effLevel := firstNonEmpty(level, ptrString(shape.Level))

			opts := []smplkit.LogGroupOption{}
			if effName != "" {
				opts = append(opts, smplkit.WithLogGroupName(effName))
			}
			if effParent != "" {
				opts = append(opts, smplkit.WithLogGroupParent(effParent))
			}

			g := client.Logging().LogGroups().New(id, opts...)
			if effLevel != "" {
				lvl := smplkit.LogLevel(strings.ToUpper(effLevel))
				if !validLogLevel(lvl) {
					return fmt.Errorf("invalid --level %q", effLevel)
				}
				g.SetLevel(lvl, "")
			}
			if err := g.Save(cliContext(cmd)); err != nil {
				return err
			}
			return renderer(cmd).RenderLogGroup(g)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "display name")
	cmd.Flags().StringVar(&level, "level", "", "base log level")
	cmd.Flags().StringVar(&parent, "parent", "", "parent log group id")
	cmd.Flags().StringVarP(&file, "file", "f", "", "load definition from JSON file")
	return cmd
}

func logGroupSetCmd() *cobra.Command {
	var (
		name        string
		level       string
		clearLevel  bool
		parent      string
		clearParent bool
		file        string
	)
	cmd := &cobra.Command{
		Use:   "set <key>",
		Short: "Update a log group (read-modify-write)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			client, err := withClient()
			if err != nil {
				return err
			}
			ctx := cliContext(cmd)
			g, err := client.Logging().LogGroups().Get(ctx, id)
			if err != nil {
				return err
			}
			if file != "" {
				shape, ferr := loadLogGroupFile(file)
				if ferr != nil {
					return ferr
				}
				if shape.Name != "" {
					g.Name = shape.Name
				}
				if shape.Parent != nil {
					g.Group = shape.Parent
				}
				if shape.Level != nil {
					lvl := smplkit.LogLevel(strings.ToUpper(*shape.Level))
					g.Level = &lvl
				}
			}
			if cmd.Flags().Changed("name") {
				g.Name = name
			}
			if cmd.Flags().Changed("level") {
				if clearLevel {
					return fmt.Errorf("--level and --clear-level are mutually exclusive")
				}
				lvl := smplkit.LogLevel(strings.ToUpper(level))
				if !validLogLevel(lvl) {
					return fmt.Errorf("invalid --level %q", level)
				}
				g.Level = &lvl
			}
			if clearLevel {
				g.Level = nil
			}
			if cmd.Flags().Changed("parent") {
				if clearParent {
					return fmt.Errorf("--parent and --clear-parent are mutually exclusive")
				}
				p := parent
				g.Group = &p
			}
			if clearParent {
				g.Group = nil
			}
			if err := g.Save(ctx); err != nil {
				return err
			}
			return renderer(cmd).RenderLogGroup(g)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "display name")
	cmd.Flags().StringVar(&level, "level", "", "base log level")
	cmd.Flags().BoolVar(&clearLevel, "clear-level", false, "clear the base log level")
	cmd.Flags().StringVar(&parent, "parent", "", "parent log group id")
	cmd.Flags().BoolVar(&clearParent, "clear-parent", false, "clear the parent assignment")
	cmd.Flags().StringVarP(&file, "file", "f", "", "load full definition from JSON file (applied before scalar flags)")
	return cmd
}

func logGroupDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <key>",
		Short: "Delete a log group",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if !confirm(cmd, yes, fmt.Sprintf("Delete log group %q?", id)) {
				return fmt.Errorf("aborted")
			}
			client, err := withClient()
			if err != nil {
				return err
			}
			if err := client.Logging().LogGroups().Delete(cliContext(cmd), id); err != nil {
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

func loadLogGroupFile(path string) (logGroupFileShape, error) {
	var shape logGroupFileShape
	data, err := readFileFlag(path)
	if err != nil {
		return shape, err
	}
	if len(data) == 0 {
		return shape, nil
	}
	if err := json.Unmarshal(data, &shape); err != nil {
		return shape, fmt.Errorf("parse %s: %w", path, err)
	}
	return shape, nil
}
