package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	smplkit "github.com/smplkit/go-sdk/v3"
	"github.com/spf13/cobra"

	"github.com/smplkit/cli/internal/output"
	"github.com/smplkit/cli/internal/paginate"
	"github.com/smplkit/cli/internal/values"
)

// flagFileShape mirrors output.FlagAttr but with omittable fields so a
// `get -o json | jq ... | smplkit flag set -f -` round-trip is clean.
// Reused for both create and set.
type flagFileShape struct {
	ID           string                 `json:"id,omitempty"`
	Name         string                 `json:"name,omitempty"`
	Type         string                 `json:"type,omitempty"`
	Default      interface{}            `json:"default,omitempty"`
	Description  *string                `json:"description,omitempty"`
	Values       []output.FlagValueAttr `json:"values,omitempty"`
	Environments map[string]interface{} `json:"environments,omitempty"`
}

func registerFlagCmd(root *cobra.Command) {
	flag := &cobra.Command{
		Use:   "flag",
		Short: "Manage feature flags",
	}

	flag.AddCommand(flagListCmd())
	flag.AddCommand(flagGetCmd())
	flag.AddCommand(flagCreateCmd())
	flag.AddCommand(flagSetCmd())
	flag.AddCommand(flagDeleteCmd())

	root.AddCommand(flag)
}

func flagListCmd() *cobra.Command {
	var (
		limit int
		all   bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List flags",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := withClient()
			if err != nil {
				return err
			}
			ctx := cliContext(cmd)
			ns := client.Flags()
			var flags []*smplkit.Flag
			if all {
				flags, err = paginate.All(ctx, ns.List, limit)
			} else {
				flags, err = paginate.Single(ctx, ns.List, limit)
			}
			if err != nil {
				return err
			}
			return renderer(cmd).RenderFlags(flags)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 0, "page size (server default when unset)")
	cmd.Flags().BoolVar(&all, "all", false, "fetch every page")
	return cmd
}

func flagGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: "Get a flag by id",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := withClient()
			if err != nil {
				return err
			}
			f, err := client.Flags().Get(cliContext(cmd), args[0])
			if err != nil {
				return err
			}
			return renderer(cmd).RenderFlag(f)
		},
	}
}

func flagCreateCmd() *cobra.Command {
	var (
		flagType    string
		defaultRaw  string
		name        string
		description string
		file        string
	)
	cmd := &cobra.Command{
		Use:   "create <key>",
		Short: "Create a flag",
		Long: "Create a flag. Supply --type and --default for the simple case, or -f flag.json\n" +
			"(produced by `smplkit flag get -o json`) to round-trip a full definition.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			client, err := withClient()
			if err != nil {
				return err
			}
			ns := client.Flags()

			fileShape, err := loadFlagFile(file)
			if err != nil {
				return err
			}

			// Determine effective type / default: scalar flags override file.
			effType := strings.ToUpper(strings.TrimSpace(flagType))
			if effType == "" {
				effType = strings.ToUpper(fileShape.Type)
			}
			if effType == "" {
				return fmt.Errorf("missing --type (or `type` in -f file)")
			}

			var rawDefault interface{}
			switch {
			case cmd.Flags().Changed("default"):
				parsed, perr := values.ParseFlagDefault(defaultRaw, effType)
				if perr != nil {
					return perr
				}
				rawDefault = parsed
			case fileShape.Default != nil:
				rawDefault = fileShape.Default
			default:
				return fmt.Errorf("missing --default (or `default` in -f file)")
			}

			f, err := newFlagFromType(ns, id, effType, rawDefault)
			if err != nil {
				return err
			}

			effName := name
			if effName == "" {
				effName = fileShape.Name
			}
			if effName != "" {
				f.Name = effName
			}
			if cmd.Flags().Changed("description") {
				d := description
				f.Description = &d
			} else if fileShape.Description != nil {
				f.Description = fileShape.Description
			}
			if len(fileShape.Values) > 0 {
				vs := make([]smplkit.FlagValue, 0, len(fileShape.Values))
				for _, v := range fileShape.Values {
					vs = append(vs, smplkit.FlagValue{Name: v.Name, Value: v.Value})
				}
				f.Values = &vs
			}
			if len(fileShape.Environments) > 0 {
				f.Environments = fileShape.Environments
			}

			if err := f.Save(cliContext(cmd)); err != nil {
				return err
			}
			return renderer(cmd).RenderFlag(f)
		},
	}
	cmd.Flags().StringVar(&flagType, "type", "", "flag type: bool | string | number | json")
	cmd.Flags().StringVar(&defaultRaw, "default", "", "default value (parsed per --type)")
	cmd.Flags().StringVar(&name, "name", "", "display name (defaults to a humanized key)")
	cmd.Flags().StringVar(&description, "description", "", "description")
	cmd.Flags().StringVarP(&file, "file", "f", "", "load definition from JSON file (round-trips `get -o json`)")
	return cmd
}

func flagSetCmd() *cobra.Command {
	var (
		name        string
		description string
		enabled     bool
		disabled    bool
		valueRaw    string
		rulesRaw    string
		file        string
	)
	cmd := &cobra.Command{
		Use:   "set <key>",
		Short: "Update a flag (read-modify-write)",
		Long: "Mutates a flag in place: GET → apply --name/--description (base), --enabled/--disabled/--value/--rules\n" +
			"(env-scoped, requires --env) → PUT the full resource back. -f flag.json applies a full body before\n" +
			"the scalar flags so a `get -o json` snapshot can be edited and replayed.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			client, err := withClient()
			if err != nil {
				return err
			}
			ctx := cliContext(cmd)
			f, err := client.Flags().Get(ctx, id)
			if err != nil {
				return err
			}

			if file != "" {
				shape, ferr := loadFlagFile(file)
				if ferr != nil {
					return ferr
				}
				applyFlagFileToModel(f, shape)
			}

			if cmd.Flags().Changed("name") {
				f.Name = name
			}
			if cmd.Flags().Changed("description") {
				d := description
				f.Description = &d
			}

			envScoped := enabled || disabled ||
				cmd.Flags().Changed("value") || cmd.Flags().Changed("rules")

			var env string
			if envScoped {
				env, err = requireEnv()
				if err != nil {
					return err
				}
			}

			if enabled && disabled {
				return fmt.Errorf("--enabled and --disabled are mutually exclusive")
			}
			if enabled {
				f.EnableRules(env)
			}
			if disabled {
				f.DisableRules(env)
			}

			if cmd.Flags().Changed("value") {
				parsed, verr := values.ParseFlagDefault(valueRaw, f.Type)
				if verr != nil {
					return verr
				}
				f.SetDefault(parsed, env)
			}

			if cmd.Flags().Changed("rules") {
				if err := applyRules(f, env, rulesRaw); err != nil {
					return err
				}
			}

			if err := f.Save(ctx); err != nil {
				return err
			}
			return renderer(cmd).RenderFlag(f)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "display name (base)")
	cmd.Flags().StringVar(&description, "description", "", "description (base)")
	cmd.Flags().BoolVar(&enabled, "enabled", false, "enable rules (env-scoped; needs --env)")
	cmd.Flags().BoolVar(&disabled, "disabled", false, "disable rules / kill switch (env-scoped; needs --env)")
	cmd.Flags().StringVar(&valueRaw, "value", "", "set the env-scoped default value (parsed per flag type)")
	cmd.Flags().StringVar(&rulesRaw, "rules", "", "set env-scoped rules from JSON or @file (replaces the whole rules array)")
	cmd.Flags().StringVarP(&file, "file", "f", "", "apply a full definition from JSON file (round-trips `get -o json`)")
	return cmd
}

func flagDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <key>",
		Short: "Delete a flag",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if !confirm(cmd, yes, fmt.Sprintf("Delete flag %q?", id)) {
				return fmt.Errorf("aborted")
			}
			client, err := withClient()
			if err != nil {
				return err
			}
			if err := client.Flags().Delete(cliContext(cmd), id); err != nil {
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

// newFlagFromType dispatches to the right typed factory.
func newFlagFromType(ns *smplkit.FlagsManagement, id, flagType string, raw interface{}) (*smplkit.Flag, error) {
	switch strings.ToUpper(flagType) {
	case "BOOLEAN", "BOOL":
		v, ok := raw.(bool)
		if !ok {
			return nil, fmt.Errorf("default for BOOLEAN flag must be bool, got %T", raw)
		}
		return ns.NewBooleanFlag(id, v), nil
	case "STRING":
		s, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("default for STRING flag must be string, got %T", raw)
		}
		return ns.NewStringFlag(id, s), nil
	case "NUMERIC", "NUMBER":
		switch v := raw.(type) {
		case float64:
			return ns.NewNumberFlag(id, v), nil
		case int:
			return ns.NewNumberFlag(id, float64(v)), nil
		case int64:
			return ns.NewNumberFlag(id, float64(v)), nil
		}
		return nil, fmt.Errorf("default for NUMERIC flag must be number, got %T", raw)
	case "JSON":
		m, ok := raw.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("default for JSON flag must be object, got %T", raw)
		}
		return ns.NewJsonFlag(id, m), nil
	}
	return nil, fmt.Errorf("unsupported flag type %q (expected bool|string|number|json)", flagType)
}

func loadFlagFile(path string) (flagFileShape, error) {
	var shape flagFileShape
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

func applyFlagFileToModel(f *smplkit.Flag, shape flagFileShape) {
	if shape.Name != "" {
		f.Name = shape.Name
	}
	if shape.Description != nil {
		f.Description = shape.Description
	}
	if shape.Default != nil {
		f.Default = shape.Default
	}
	if len(shape.Values) > 0 {
		vs := make([]smplkit.FlagValue, 0, len(shape.Values))
		for _, v := range shape.Values {
			vs = append(vs, smplkit.FlagValue{Name: v.Name, Value: v.Value})
		}
		f.Values = &vs
	}
	if shape.Environments != nil {
		f.Environments = shape.Environments
	}
}

func applyRules(f *smplkit.Flag, env, raw string) error {
	if env == "" {
		return fmt.Errorf("--rules is env-scoped; --env is required")
	}
	body, err := values.AtFileOrLiteral(raw)
	if err != nil {
		return err
	}
	var parsed []map[string]interface{}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		return fmt.Errorf("--rules must be a JSON array of rule objects: %w", err)
	}
	// Replace the rules array on the named environment, keeping any
	// existing `enabled` and `default` keys intact.
	envs := map[string]interface{}{}
	for k, v := range f.Environments {
		envs[k] = v
	}
	envData, _ := envs[env].(map[string]interface{})
	if envData == nil {
		envData = map[string]interface{}{"enabled": true}
	}
	cp := map[string]interface{}{}
	for k, v := range envData {
		cp[k] = v
	}
	rules := make([]interface{}, 0, len(parsed))
	for _, r := range parsed {
		rules = append(rules, r)
	}
	cp["rules"] = rules
	envs[env] = cp
	f.Environments = envs
	return nil
}
