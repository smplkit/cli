package cmd

import (
	"fmt"
	"strings"

	smplkit "github.com/smplkit/go-sdk/v3"
	"github.com/spf13/cobra"

	"github.com/smplkit/cli/internal/paginate"
)

func registerLoggerCmd(root *cobra.Command) {
	logger := &cobra.Command{
		Use:   "logger",
		Short: "Manage logger resources",
	}
	logger.AddCommand(loggerListCmd())
	logger.AddCommand(loggerGetCmd())
	logger.AddCommand(loggerSetCmd())
	logger.AddCommand(loggerDeleteCmd())
	root.AddCommand(logger)
}

func loggerListCmd() *cobra.Command {
	var (
		limit int
		all   bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List loggers",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := withClient()
			if err != nil {
				return err
			}
			ctx := cliContext(cmd)
			ns := client.Loggers()
			var loggers []*smplkit.Logger
			if all {
				loggers, err = paginate.All(ctx, ns.List, limit)
			} else {
				loggers, err = paginate.Single(ctx, ns.List, limit)
			}
			if err != nil {
				return err
			}
			return renderer(cmd).RenderLoggers(loggers)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 0, "page size (server default when unset)")
	cmd.Flags().BoolVar(&all, "all", false, "fetch every page")
	return cmd
}

func loggerGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <name>",
		Short: "Get a logger by id",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := withClient()
			if err != nil {
				return err
			}
			l, err := client.Loggers().Get(cliContext(cmd), args[0])
			if err != nil {
				return err
			}
			return renderer(cmd).RenderLogger(l)
		},
	}
}

func loggerSetCmd() *cobra.Command {
	var (
		level      string
		clearLevel bool
		group      string
		clearGroup bool
	)
	cmd := &cobra.Command{
		Use:   "set <name>",
		Short: "Update a logger (read-modify-write)",
		Long: "GET → apply --level / --clear-level (env-scoped iff --env is set; otherwise base)\n" +
			"and --group / --clear-group (base only) → PUT the full resource back.\n" +
			"There is no `logger create` — loggers are discovered by the runtime SDK; the\n" +
			"management surface only mutates existing ones.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			client, err := withClient()
			if err != nil {
				return err
			}
			ctx := cliContext(cmd)
			l, err := client.Loggers().Get(ctx, id)
			if err != nil {
				return err
			}

			env := strings.TrimSpace(globals.Env)
			// If --env is unset and SMPLKIT_ENVIRONMENT / profile have a
			// value, only treat the operation as env-scoped when the user
			// asked for an env-only verb. The base/per-env split here
			// keys solely off --env to match the SDK's empty-string
			// convention precisely.

			if cmd.Flags().Changed("level") && clearLevel {
				return fmt.Errorf("--level and --clear-level are mutually exclusive")
			}
			if cmd.Flags().Changed("level") {
				upper := smplkit.LogLevel(strings.ToUpper(level))
				if !validLogLevel(upper) {
					return fmt.Errorf("invalid --level %q (expected TRACE|DEBUG|INFO|WARN|ERROR|FATAL|SILENT)", level)
				}
				l.SetLevel(upper, env)
			}
			if clearLevel {
				l.ClearLevel(env)
			}

			if cmd.Flags().Changed("group") {
				if group == "" {
					l.Group = nil
				} else {
					g := group
					l.Group = &g
				}
			}
			if clearGroup {
				l.Group = nil
			}

			if err := l.Save(ctx); err != nil {
				return err
			}
			return renderer(cmd).RenderLogger(l)
		},
	}
	cmd.Flags().StringVar(&level, "level", "", "log level: TRACE|DEBUG|INFO|WARN|ERROR|FATAL|SILENT")
	cmd.Flags().BoolVar(&clearLevel, "clear-level", false, "clear the (env-scoped, if --env set) level")
	cmd.Flags().StringVar(&group, "group", "", "log group id (empty string clears)")
	cmd.Flags().BoolVar(&clearGroup, "clear-group", false, "clear the group assignment")
	return cmd
}

func loggerDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a logger",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if !confirm(cmd, yes, fmt.Sprintf("Delete logger %q?", id)) {
				return fmt.Errorf("aborted")
			}
			client, err := withClient()
			if err != nil {
				return err
			}
			if err := client.Loggers().Delete(cliContext(cmd), id); err != nil {
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

func validLogLevel(l smplkit.LogLevel) bool {
	switch l {
	case smplkit.LogLevelTrace, smplkit.LogLevelDebug, smplkit.LogLevelInfo,
		smplkit.LogLevelWarn, smplkit.LogLevelError, smplkit.LogLevelFatal,
		smplkit.LogLevelSilent:
		return true
	}
	return false
}
