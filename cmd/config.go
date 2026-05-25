package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	smplkit "github.com/smplkit/go-sdk/v3"
	"github.com/spf13/cobra"

	"github.com/smplkit/cli/internal/paginate"
	"github.com/smplkit/cli/internal/values"
)

type configFileShape struct {
	ID           string                            `json:"id,omitempty"`
	Name         string                            `json:"name,omitempty"`
	Description  *string                           `json:"description,omitempty"`
	Parent       *string                           `json:"parent,omitempty"`
	Items        map[string]interface{}            `json:"items,omitempty"`
	Environments map[string]map[string]interface{} `json:"environments,omitempty"`
}

func registerConfigCmd(root *cobra.Command) {
	cfg := &cobra.Command{
		Use:   "config",
		Short: "Manage configuration resources",
	}
	cfg.AddCommand(configListCmd())
	cfg.AddCommand(configGetCmd())
	cfg.AddCommand(configCreateCmd())
	cfg.AddCommand(configSetCmd())
	cfg.AddCommand(configDeleteCmd())
	root.AddCommand(cfg)
}

func configListCmd() *cobra.Command {
	var (
		limit     int
		all       bool
		parent    string
		search    string
		managed   bool
		unmanaged bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List configurations",
		Long: "Lists configurations. --parent/--search/--managed/--unmanaged are client-side\n" +
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
			ns := client.Config()
			var configs []*smplkit.ConfigEntry
			if all {
				configs, err = paginate.All(ctx, ns.List, limit)
			} else {
				configs, err = paginate.Single(ctx, ns.List, limit)
			}
			if err != nil {
				return err
			}
			configs = filterConfigs(configs, parent, search, managed, unmanaged)
			return renderer(cmd).RenderConfigs(configs)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 0, "page size (server default when unset)")
	cmd.Flags().BoolVar(&all, "all", false, "fetch every page")
	cmd.Flags().StringVar(&parent, "parent", "", "filter by parent id")
	cmd.Flags().StringVar(&search, "search", "", "case-insensitive substring match on id or name")
	cmd.Flags().BoolVar(&managed, "managed", false, "only configurations whose ID is reserved (parent == \"common\")")
	cmd.Flags().BoolVar(&unmanaged, "unmanaged", false, "only configurations without the reserved parent")
	return cmd
}

func filterConfigs(in []*smplkit.ConfigEntry, parent, search string, managed, unmanaged bool) []*smplkit.ConfigEntry {
	if parent == "" && search == "" && !managed && !unmanaged {
		return in
	}
	out := make([]*smplkit.ConfigEntry, 0, len(in))
	for _, c := range in {
		if parent != "" {
			if c.Parent == nil || *c.Parent != parent {
				continue
			}
		}
		if search != "" {
			needle := strings.ToLower(search)
			if !strings.Contains(strings.ToLower(c.ID), needle) &&
				!strings.Contains(strings.ToLower(c.Name), needle) {
				continue
			}
		}
		if managed && (c.Parent == nil || *c.Parent != "common") {
			continue
		}
		if unmanaged && c.Parent != nil && *c.Parent == "common" {
			continue
		}
		out = append(out, c)
	}
	return out
}

func configGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: "Get a configuration by id",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := withClient()
			if err != nil {
				return err
			}
			c, err := client.Config().Get(cliContext(cmd), args[0])
			if err != nil {
				return err
			}
			return renderer(cmd).RenderConfig(c)
		},
	}
}

func configCreateCmd() *cobra.Command {
	var (
		name   string
		parent string
		file   string
	)
	cmd := &cobra.Command{
		Use:   "create <key>",
		Short: "Create a configuration",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			client, err := withClient()
			if err != nil {
				return err
			}
			shape, err := loadConfigFile(file)
			if err != nil {
				return err
			}

			ns := client.Config()
			opts := []smplkit.ConfigOption{}
			effName := firstNonEmpty(name, shape.Name)
			if effName != "" {
				opts = append(opts, smplkit.WithConfigName(effName))
			}
			effParent := firstNonEmpty(parent, ptrString(shape.Parent))
			if effParent != "" {
				opts = append(opts, smplkit.WithConfigParent(effParent))
			}
			if len(shape.Items) > 0 {
				opts = append(opts, smplkit.WithConfigItems(shape.Items))
			}
			if len(shape.Environments) > 0 {
				opts = append(opts, smplkit.WithConfigEnvironments(shape.Environments))
			}
			if shape.Description != nil {
				opts = append(opts, smplkit.WithConfigDescription(*shape.Description))
			}

			c := ns.New(id, opts...)
			if err := c.Save(cliContext(cmd)); err != nil {
				return err
			}
			return renderer(cmd).RenderConfig(c)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "display name")
	cmd.Flags().StringVar(&parent, "parent", "", "parent config id (e.g. \"common\")")
	cmd.Flags().StringVarP(&file, "file", "f", "", "load definition from JSON file")
	return cmd
}

func configSetCmd() *cobra.Command {
	var (
		name        string
		parent      string
		items       []string
		itemType    string
		removeItems []string
		envValues   []string
		managed     bool
		unmanaged   bool
		file        string
	)
	cmd := &cobra.Command{
		Use:   "set <key>",
		Short: "Update a configuration (read-modify-write)",
		Long: "GET → apply --name/--parent/--item/--remove-item/--env-value/--managed/--unmanaged →\n" +
			"PUT the full resource back. -f config.json applies a full body before scalar flags.\n" +
			"--env-value requires --env. --item-type controls the parsed type for --item (defaults to string).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if managed && unmanaged {
				return fmt.Errorf("--managed and --unmanaged are mutually exclusive")
			}
			id := args[0]
			client, err := withClient()
			if err != nil {
				return err
			}
			ctx := cliContext(cmd)
			c, err := client.Config().Get(ctx, id)
			if err != nil {
				return err
			}

			if file != "" {
				shape, ferr := loadConfigFile(file)
				if ferr != nil {
					return ferr
				}
				applyConfigFileToModel(c, shape)
			}
			if cmd.Flags().Changed("name") {
				c.Name = name
			}
			if cmd.Flags().Changed("parent") {
				p := parent
				if p == "" {
					c.Parent = nil
				} else {
					c.Parent = &p
				}
			}
			if managed {
				common := "common"
				c.Parent = &common
			}
			if unmanaged {
				c.Parent = nil
			}

			it := values.ItemType(strings.ToLower(itemType))
			if it == "" {
				it = values.ItemTypeString
			}
			for _, kv := range items {
				k, v, perr := values.SplitKeyValue(kv)
				if perr != nil {
					return fmt.Errorf("--item: %w", perr)
				}
				parsed, perr := values.ParseTyped(v, it)
				if perr != nil {
					return fmt.Errorf("--item %q: %w", k, perr)
				}
				switch parsed := parsed.(type) {
				case string:
					c.SetString(k, parsed, "")
				case float64:
					c.SetNumber(k, parsed, "")
				case bool:
					c.SetBoolean(k, parsed, "")
				default:
					c.SetJSON(k, parsed, "")
				}
			}
			for _, k := range removeItems {
				c.Remove(k, "")
			}

			if len(envValues) > 0 {
				env, eerr := requireEnv()
				if eerr != nil {
					return eerr
				}
				for _, kv := range envValues {
					k, v, perr := values.SplitKeyValue(kv)
					if perr != nil {
						return fmt.Errorf("--env-value: %w", perr)
					}
					parsed, perr := values.ParseTyped(v, it)
					if perr != nil {
						return fmt.Errorf("--env-value %q: %w", k, perr)
					}
					switch parsed := parsed.(type) {
					case string:
						c.SetString(k, parsed, env)
					case float64:
						c.SetNumber(k, parsed, env)
					case bool:
						c.SetBoolean(k, parsed, env)
					default:
						c.SetJSON(k, parsed, env)
					}
				}
			}

			if err := c.Save(ctx); err != nil {
				return err
			}
			return renderer(cmd).RenderConfig(c)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "display name")
	cmd.Flags().StringVar(&parent, "parent", "", "parent config id (empty string clears)")
	cmd.Flags().StringSliceVar(&items, "item", nil, "base item to set, repeatable: -i key=value")
	cmd.Flags().StringVar(&itemType, "item-type", "string", "type for --item / --env-value: string | number | bool | json")
	cmd.Flags().StringSliceVar(&removeItems, "remove-item", nil, "base item to remove, repeatable")
	cmd.Flags().StringSliceVar(&envValues, "env-value", nil, "env-scoped item, repeatable: --env-value key=value (needs --env)")
	cmd.Flags().BoolVar(&managed, "managed", false, "promote: set parent to \"common\"")
	cmd.Flags().BoolVar(&unmanaged, "unmanaged", false, "demote: clear parent")
	cmd.Flags().StringVarP(&file, "file", "f", "", "load full definition from JSON file (applied before scalar flags)")
	return cmd
}

func configDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <key>",
		Short: "Delete a configuration",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if !confirm(cmd, yes, fmt.Sprintf("Delete configuration %q?", id)) {
				return fmt.Errorf("aborted")
			}
			client, err := withClient()
			if err != nil {
				return err
			}
			if err := client.Config().Delete(cliContext(cmd), id); err != nil {
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

func loadConfigFile(path string) (configFileShape, error) {
	var shape configFileShape
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

func applyConfigFileToModel(c *smplkit.ConfigEntry, shape configFileShape) {
	if shape.Name != "" {
		c.Name = shape.Name
	}
	if shape.Description != nil {
		c.Description = shape.Description
	}
	if shape.Parent != nil {
		c.Parent = shape.Parent
	}
	if shape.Items != nil {
		c.Items = shape.Items
	}
	if shape.Environments != nil {
		c.Environments = shape.Environments
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func ptrString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
