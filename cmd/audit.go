package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	smplkit "github.com/smplkit/go-sdk/v3"
	"github.com/spf13/cobra"

	"github.com/smplkit/cli/internal/output"
)

// forwarderFileShape is the JSON/YAML body for `-f forwarder.json`.
// Mirrors output.ForwarderAttr but every field is omitempty so a
// `get -o json` snapshot can be replayed unchanged on `set -f -`.
//
// Enablement is per-environment (ADR-055): a forwarder delivers in an
// environment only when Environments[env].Enabled is true. There is no
// global enabled flag.
type forwarderFileShape struct {
	ID            string                           `json:"id,omitempty"`
	Name          string                           `json:"name,omitempty"`
	Description   *string                          `json:"description,omitempty"`
	Type          string                           `json:"type,omitempty"`
	Environments  map[string]forwarderEnvFileShape `json:"environments,omitempty"`
	Configuration *output.ForwarderHTTPConfigAttr  `json:"configuration,omitempty"`
	Filter        map[string]interface{}           `json:"filter,omitempty"`
	Transform     interface{}                      `json:"transform,omitempty"`
	TransformType *string                          `json:"transform_type,omitempty"`
	// ForwardSmplkitEvents opts the forwarder into receiving smplkit's own
	// platform change events in addition to your audit events.
	ForwardSmplkitEvents *bool `json:"forward_smplkit_events,omitempty"`
}

// forwarderEnvFileShape is the per-environment override carried in the
// `-f` file. A nil Configuration inherits the base configuration.
type forwarderEnvFileShape struct {
	Enabled       bool                            `json:"enabled"`
	Configuration *output.ForwarderHTTPConfigAttr `json:"configuration,omitempty"`
}

func registerAuditCmd(root *cobra.Command) {
	audit := &cobra.Command{
		Use:   "audit",
		Short: "Manage audit resources",
	}
	forwarder := &cobra.Command{
		Use:   "forwarder",
		Short: "Manage SIEM forwarders",
	}
	forwarder.AddCommand(forwarderListCmd())
	forwarder.AddCommand(forwarderGetCmd())
	forwarder.AddCommand(forwarderCreateCmd())
	forwarder.AddCommand(forwarderSetCmd())
	forwarder.AddCommand(forwarderDeleteCmd())
	audit.AddCommand(forwarder)
	root.AddCommand(audit)
}

func forwarderListCmd() *cobra.Command {
	var (
		limit      int
		all        bool
		filterType string
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List SIEM forwarders",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := withClient()
			if err != nil {
				return err
			}
			ctx := cliContext(cmd)
			ns := client.Audit().Forwarders()
			input := smplkit.ListForwardersInput{}
			if filterType != "" {
				input.ForwarderType = smplkit.ForwarderType(filterType)
			}
			var forwarders []smplkit.Forwarder
			if all {
				forwarders, err = fetchAllForwarders(ctx, ns, input, limit)
			} else {
				if limit > 0 {
					input.PageSize = limit
				}
				page, perr := ns.List(ctx, input)
				if perr != nil {
					return perr
				}
				forwarders = page.Forwarders
			}
			if err != nil {
				return err
			}
			return renderer(cmd).RenderForwarders(forwarders)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 0, "page size (server default when unset)")
	cmd.Flags().BoolVar(&all, "all", false, "fetch every page")
	cmd.Flags().StringVar(&filterType, "type", "", "filter by forwarder type")
	return cmd
}

func fetchAllForwarders(ctx context.Context, ns *smplkit.AuditForwarders, input smplkit.ListForwardersInput, limit int) ([]smplkit.Forwarder, error) {
	pageSize := 1000
	if limit > 0 && limit < pageSize {
		pageSize = limit
	}
	var out []smplkit.Forwarder
	for page := 1; ; page++ {
		input.PageNumber = page
		input.PageSize = pageSize
		resp, err := ns.List(ctx, input)
		if err != nil {
			return nil, err
		}
		out = append(out, resp.Forwarders...)
		if len(resp.Forwarders) < pageSize {
			return out, nil
		}
	}
}

func forwarderGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: "Get a forwarder by id",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := withClient()
			if err != nil {
				return err
			}
			f, err := client.Audit().Forwarders().Get(cliContext(cmd), args[0])
			if err != nil {
				return err
			}
			return renderer(cmd).RenderForwarder(f)
		},
	}
}

func forwarderCreateCmd() *cobra.Command {
	var (
		file                 string
		fType                string
		name                 string
		url                  string
		headers              []string
		filterRaw            string
		transformRaw         string
		transformType        string
		enableEnvs           []string
		disableEnvs          []string
		description          string
		method               string
		successStatus        string
		forwardSmplkitEvents bool
	)
	cmd := &cobra.Command{
		Use:   "create <key>",
		Short: "Create a SIEM forwarder",
		Long: "Creates a forwarder. -f forwarder.json carries the full definition (recommended\n" +
			"for Datadog/Splunk/Honeycomb/etc. since the URL/headers vary). For Custom HTTP,\n" +
			"the scalar flags are usually enough: --type http --url ... --header k=v.\n" +
			"Where both are supplied, scalar flags override file values.\n\n" +
			"Enablement is per-environment: a forwarder delivers only in environments\n" +
			"turned on via --enable-env <env> (repeatable) or the file's `environments`\n" +
			"map. Without any enabled environment the forwarder delivers nowhere.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			client, err := withClient()
			if err != nil {
				return err
			}
			ns := client.Audit().Forwarders()

			shape, err := loadForwarderFile(file)
			if err != nil {
				return err
			}

			fwd, err := buildForwarderForCreate(ns, id, shape, forwarderInputs{
				ftype:                fType,
				name:                 name,
				url:                  url,
				headers:              headers,
				filterRaw:            filterRaw,
				transformRaw:         transformRaw,
				transformType:        transformType,
				enableEnvs:           enableEnvs,
				disableEnvs:          disableEnvs,
				description:          description,
				method:               method,
				successStatus:        successStatus,
				methodSet:            cmd.Flags().Changed("method"),
				successSet:           cmd.Flags().Changed("success-status"),
				forwardSmplkitEvents: forwardSmplkitEvents,
				forwardSmplkitSet:    cmd.Flags().Changed("forward-smplkit-events"),
			})
			if err != nil {
				return err
			}

			if err := fwd.Save(cliContext(cmd)); err != nil {
				return err
			}
			return renderer(cmd).RenderForwarder(fwd)
		},
	}
	addForwarderScalarFlags(cmd, &file, &fType, &name, &url, &headers,
		&filterRaw, &transformRaw, &transformType, &enableEnvs, &disableEnvs,
		&description, &method, &successStatus, &forwardSmplkitEvents)
	return cmd
}

func forwarderSetCmd() *cobra.Command {
	var (
		file                 string
		name                 string
		url                  string
		headers              []string
		filterRaw            string
		transformRaw         string
		transformType        string
		enableEnvs           []string
		disableEnvs          []string
		description          string
		method               string
		successStatus        string
		forwardSmplkitEvents bool
		// Unused for set — kept here so the helper signature stays the
		// same as create. (--type can't be changed via set.)
		fType string
	)
	cmd := &cobra.Command{
		Use:   "set <key>",
		Short: "Update a SIEM forwarder (read-modify-write)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			client, err := withClient()
			if err != nil {
				return err
			}
			ns := client.Audit().Forwarders()
			ctx := cliContext(cmd)
			fwd, err := ns.Get(ctx, id)
			if err != nil {
				return err
			}

			if file != "" {
				shape, ferr := loadForwarderFile(file)
				if ferr != nil {
					return ferr
				}
				applyForwarderFileToModel(fwd, shape)
			}

			inputs := forwarderInputs{
				name:                 name,
				url:                  url,
				headers:              headers,
				filterRaw:            filterRaw,
				transformRaw:         transformRaw,
				transformType:        transformType,
				enableEnvs:           enableEnvs,
				disableEnvs:          disableEnvs,
				description:          description,
				method:               method,
				successStatus:        successStatus,
				methodSet:            cmd.Flags().Changed("method"),
				successSet:           cmd.Flags().Changed("success-status"),
				forwardSmplkitEvents: forwardSmplkitEvents,
				forwardSmplkitSet:    cmd.Flags().Changed("forward-smplkit-events"),
			}
			if err := applyForwarderInputsToModel(fwd, inputs, cmd); err != nil {
				return err
			}

			if err := fwd.Save(ctx); err != nil {
				return err
			}
			return renderer(cmd).RenderForwarder(fwd)
		},
	}
	addForwarderScalarFlags(cmd, &file, &fType, &name, &url, &headers,
		&filterRaw, &transformRaw, &transformType, &enableEnvs, &disableEnvs,
		&description, &method, &successStatus, &forwardSmplkitEvents)
	// --type lives on the flag set so help text mirrors create, but
	// changing the type after creation is rejected server-side.
	_ = cmd.Flags().MarkHidden("type")
	return cmd
}

func forwarderDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <key>",
		Short: "Delete a SIEM forwarder",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if !confirm(cmd, yes, fmt.Sprintf("Delete forwarder %q?", id)) {
				return fmt.Errorf("aborted")
			}
			client, err := withClient()
			if err != nil {
				return err
			}
			if err := client.Audit().Forwarders().Delete(cliContext(cmd), id); err != nil {
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

// forwarderInputs bundles the scalar inputs so create/set share one
// path through buildForwarderForCreate / applyForwarderInputsToModel.
type forwarderInputs struct {
	ftype         string
	name          string
	url           string
	headers       []string
	filterRaw     string
	transformRaw  string
	transformType string
	enableEnvs    []string
	disableEnvs   []string
	description   string
	method        string
	methodSet     bool
	successStatus string
	successSet    bool
	// forwardSmplkitEvents carries the --forward-smplkit-events flag.
	// forwardSmplkitSet reports whether the flag was supplied so set
	// (read-modify-write) leaves the field untouched when omitted.
	forwardSmplkitEvents bool
	forwardSmplkitSet    bool
}

func addForwarderScalarFlags(cmd *cobra.Command, file, fType, name, url *string,
	headers *[]string, filterRaw, transformRaw, transformType *string,
	enableEnvs, disableEnvs *[]string, description, method, successStatus *string,
	forwardSmplkitEvents *bool) {

	cmd.Flags().StringVarP(file, "file", "f", "", "load full definition from JSON file")
	cmd.Flags().StringVar(fType, "type", "", "forwarder type: datadog|elastic|honeycomb|http|new_relic|splunk_hec|sumo_logic")
	cmd.Flags().StringVar(name, "name", "", "display name")
	cmd.Flags().StringVar(url, "url", "", "destination URL")
	cmd.Flags().StringSliceVar(headers, "header", nil, "HTTP header (repeatable): -H key=value")
	cmd.Flags().StringVar(filterRaw, "filter", "", "JSON Logic filter from JSON or @file")
	cmd.Flags().StringVar(transformRaw, "transform", "", "JSONata template (or @file)")
	cmd.Flags().StringVar(transformType, "transform-type", "", "transform engine: JSONATA")
	cmd.Flags().StringSliceVar(enableEnvs, "enable-env", nil, "enable delivery in an environment (repeatable)")
	cmd.Flags().StringSliceVar(disableEnvs, "disable-env", nil, "disable delivery in an environment (repeatable)")
	cmd.Flags().StringVar(description, "description", "", "free-text description")
	cmd.Flags().StringVar(method, "method", "", "HTTP method: GET|POST|PUT|PATCH|DELETE (server default: POST)")
	cmd.Flags().StringVar(successStatus, "success-status", "", "success HTTP status: exact (\"200\") or class (\"2xx\")")
	cmd.Flags().BoolVar(forwardSmplkitEvents, "forward-smplkit-events", false, "also forward smplkit platform change events (smpl.*) through this forwarder")
}

func loadForwarderFile(path string) (*forwarderFileShape, error) {
	data, err := readFileFlag(path)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}
	var shape forwarderFileShape
	if err := json.Unmarshal(data, &shape); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &shape, nil
}

func buildForwarderForCreate(ns *smplkit.AuditForwarders, id string, shape *forwarderFileShape, in forwarderInputs) (*smplkit.Forwarder, error) {
	effType := in.ftype
	if effType == "" && shape != nil {
		effType = shape.Type
	}
	if effType == "" {
		return nil, fmt.Errorf("missing --type (or `type` in -f file)")
	}
	ft, err := parseForwarderType(effType)
	if err != nil {
		return nil, err
	}

	effName := in.name
	if effName == "" && shape != nil {
		effName = shape.Name
	}
	if effName == "" {
		effName = id
	}

	cfg, err := buildHTTPConfig(shape, in)
	if err != nil {
		return nil, err
	}
	if cfg.URL == "" {
		return nil, fmt.Errorf("missing --url (or `configuration.url` in -f file)")
	}

	opts := []smplkit.ForwarderOption{}
	envs, eerr := buildForwarderEnvironments(shape, in)
	if eerr != nil {
		return nil, eerr
	}
	if len(envs) > 0 {
		opts = append(opts, smplkit.WithForwarderEnvironments(envs))
	}
	if in.description != "" {
		opts = append(opts, smplkit.WithForwarderDescription(in.description))
	} else if shape != nil && shape.Description != nil {
		opts = append(opts, smplkit.WithForwarderDescription(*shape.Description))
	}
	if filter, ferr := pickFilter(shape, in.filterRaw); ferr != nil {
		return nil, ferr
	} else if filter != nil {
		opts = append(opts, smplkit.WithForwarderFilter(filter))
	}
	tValue, tType, terr := pickTransform(shape, in.transformRaw, in.transformType)
	if terr != nil {
		return nil, terr
	}
	if tValue != nil {
		opts = append(opts, smplkit.WithForwarderTransform(tType, tValue))
	}
	if in.forwardSmplkitSet {
		opts = append(opts, smplkit.WithForwardSmplkitEvents(in.forwardSmplkitEvents))
	} else if shape != nil && shape.ForwardSmplkitEvents != nil {
		opts = append(opts, smplkit.WithForwardSmplkitEvents(*shape.ForwardSmplkitEvents))
	}

	return ns.New(id, effName, ft, cfg, opts...), nil
}

// fileEnvShapeToConfig converts the file/CLI HTTP-config attribute shape
// into the SDK's HttpConfiguration used for per-environment overrides.
func fileEnvShapeToConfig(c *output.ForwarderHTTPConfigAttr) *smplkit.HttpConfiguration {
	if c == nil {
		return nil
	}
	cfg := smplkit.HttpConfiguration{
		URL:           c.URL,
		Method:        c.Method,
		SuccessStatus: c.SuccessStatus,
		TlsVerify:     c.TLSVerify,
		CaCert:        c.CACert,
	}
	for _, h := range c.Headers {
		cfg.Headers = append(cfg.Headers, smplkit.HttpHeader{Name: h.Name, Value: h.Value})
	}
	return &cfg
}

// fileEnvsToModel converts the file shape's environments map into the
// SDK's per-environment override map.
func fileEnvsToModel(envs map[string]forwarderEnvFileShape) map[string]smplkit.ForwarderEnvironment {
	if envs == nil {
		return nil
	}
	out := make(map[string]smplkit.ForwarderEnvironment, len(envs))
	for k, e := range envs {
		out[k] = smplkit.ForwarderEnvironment{
			Enabled:       e.Enabled,
			Configuration: fileEnvShapeToConfig(e.Configuration),
		}
	}
	return out
}

// buildForwarderEnvironments assembles the per-environment override map
// for a create from the -f file's `environments` plus the --enable-env /
// --disable-env scalar flags (flags win where they overlap the file).
func buildForwarderEnvironments(shape *forwarderFileShape, in forwarderInputs) (map[string]smplkit.ForwarderEnvironment, error) {
	var envs map[string]smplkit.ForwarderEnvironment
	if shape != nil && shape.Environments != nil {
		envs = fileEnvsToModel(shape.Environments)
	}
	if len(in.enableEnvs) == 0 && len(in.disableEnvs) == 0 {
		return envs, nil
	}
	if envs == nil {
		envs = make(map[string]smplkit.ForwarderEnvironment)
	}
	tmp := &smplkit.Forwarder{Environments: envs}
	if err := applyEnvToggles(tmp, in.enableEnvs, in.disableEnvs); err != nil {
		return nil, err
	}
	return tmp.Environments, nil
}

// applyEnvToggles flips the enabled bit for the named environments on a
// forwarder's Environments map, preserving any existing per-environment
// configuration override. --enable-env and --disable-env may not name
// the same environment.
func applyEnvToggles(f *smplkit.Forwarder, enableEnvs, disableEnvs []string) error {
	if len(enableEnvs) == 0 && len(disableEnvs) == 0 {
		return nil
	}
	for _, e := range enableEnvs {
		for _, d := range disableEnvs {
			if e == d {
				return fmt.Errorf("environment %q given to both --enable-env and --disable-env", e)
			}
		}
	}
	if f.Environments == nil {
		f.Environments = make(map[string]smplkit.ForwarderEnvironment)
	}
	setEnabled := func(env string, enabled bool) {
		cur := f.Environments[env]
		cur.Enabled = enabled
		f.Environments[env] = cur
	}
	for _, env := range enableEnvs {
		setEnabled(env, true)
	}
	for _, env := range disableEnvs {
		setEnabled(env, false)
	}
	return nil
}

func applyForwarderFileToModel(f *smplkit.Forwarder, shape *forwarderFileShape) {
	if shape == nil {
		return
	}
	if shape.Name != "" {
		f.Name = shape.Name
	}
	if shape.Description != nil {
		f.Description = shape.Description
	}
	if shape.Environments != nil {
		f.Environments = fileEnvsToModel(shape.Environments)
	}
	if shape.Filter != nil {
		f.Filter = shape.Filter
	}
	if shape.Transform != nil {
		f.Transform = shape.Transform
	}
	if shape.TransformType != nil {
		tt := smplkit.ForwarderTransformType(*shape.TransformType)
		f.TransformType = &tt
	}
	if shape.ForwardSmplkitEvents != nil {
		v := *shape.ForwardSmplkitEvents
		f.ForwardSmplkitEvents = &v
	}
	if shape.Configuration != nil {
		f.Configuration.URL = firstNonEmpty(shape.Configuration.URL, f.Configuration.URL)
		if shape.Configuration.Method != "" {
			f.Configuration.Method = shape.Configuration.Method
		}
		if shape.Configuration.SuccessStatus != "" {
			f.Configuration.SuccessStatus = shape.Configuration.SuccessStatus
		}
		if shape.Configuration.TLSVerify != nil {
			f.Configuration.TlsVerify = shape.Configuration.TLSVerify
		}
		if shape.Configuration.CACert != nil {
			f.Configuration.CaCert = shape.Configuration.CACert
		}
		if shape.Configuration.Headers != nil {
			f.Configuration.Headers = nil
			for _, h := range shape.Configuration.Headers {
				f.Configuration.Headers = append(f.Configuration.Headers,
					smplkit.HttpHeader{Name: h.Name, Value: h.Value})
			}
		}
	}
}

func applyForwarderInputsToModel(f *smplkit.Forwarder, in forwarderInputs, cmd *cobra.Command) error {
	if cmd.Flags().Changed("name") {
		f.Name = in.name
	}
	if cmd.Flags().Changed("description") {
		d := in.description
		f.Description = &d
	}
	if in.forwardSmplkitSet {
		v := in.forwardSmplkitEvents
		f.ForwardSmplkitEvents = &v
	}
	if err := applyEnvToggles(f, in.enableEnvs, in.disableEnvs); err != nil {
		return err
	}
	if cmd.Flags().Changed("url") {
		f.Configuration.URL = in.url
	}
	if in.methodSet {
		f.Configuration.Method = smplkit.HttpMethod(strings.ToUpper(in.method))
	}
	if in.successSet {
		f.Configuration.SuccessStatus = in.successStatus
	}
	if cmd.Flags().Changed("header") {
		hdrs, err := parseHeaders(in.headers)
		if err != nil {
			return err
		}
		f.Configuration.Headers = hdrs
	}
	if cmd.Flags().Changed("filter") {
		if in.filterRaw == "" {
			f.Filter = nil
		} else {
			body, err := loadJSONLogicValue(in.filterRaw)
			if err != nil {
				return err
			}
			f.Filter = body
		}
	}
	if cmd.Flags().Changed("transform") || cmd.Flags().Changed("transform-type") {
		if in.transformRaw == "" {
			f.Transform = nil
			f.TransformType = nil
		} else {
			tt := in.transformType
			if tt == "" {
				tt = "JSONATA"
			}
			tValue, err := loadTransformValue(in.transformRaw, tt)
			if err != nil {
				return err
			}
			f.Transform = tValue
			ttv := smplkit.ForwarderTransformType(strings.ToUpper(tt))
			f.TransformType = &ttv
		}
	}
	return nil
}

func buildHTTPConfig(shape *forwarderFileShape, in forwarderInputs) (smplkit.HttpConfiguration, error) {
	cfg := smplkit.HttpConfiguration{}
	if shape != nil && shape.Configuration != nil {
		cfg.URL = shape.Configuration.URL
		cfg.Method = shape.Configuration.Method
		cfg.SuccessStatus = shape.Configuration.SuccessStatus
		cfg.TlsVerify = shape.Configuration.TLSVerify
		cfg.CaCert = shape.Configuration.CACert
		for _, h := range shape.Configuration.Headers {
			cfg.Headers = append(cfg.Headers, smplkit.HttpHeader{Name: h.Name, Value: h.Value})
		}
	}
	if in.url != "" {
		cfg.URL = in.url
	}
	if in.methodSet && in.method != "" {
		cfg.Method = smplkit.HttpMethod(strings.ToUpper(in.method))
	}
	if in.successSet && in.successStatus != "" {
		cfg.SuccessStatus = in.successStatus
	}
	if len(in.headers) > 0 {
		hdrs, err := parseHeaders(in.headers)
		if err != nil {
			return cfg, err
		}
		// Scalar headers replace any file-provided ones — predictable
		// for one-off Datadog/Splunk keys where the file is a stale
		// snapshot the user is overwriting from the command line.
		cfg.Headers = hdrs
	}
	return cfg, nil
}

func parseHeaders(raws []string) ([]smplkit.HttpHeader, error) {
	out := make([]smplkit.HttpHeader, 0, len(raws))
	for _, raw := range raws {
		eq := strings.Index(raw, "=")
		if eq == -1 {
			return nil, fmt.Errorf("--header must be key=value, got %q", raw)
		}
		out = append(out, smplkit.HttpHeader{
			Name:  strings.TrimSpace(raw[:eq]),
			Value: raw[eq+1:],
		})
	}
	return out, nil
}

func parseForwarderType(raw string) (smplkit.ForwarderType, error) {
	upper := strings.ToUpper(raw)
	for _, t := range smplkit.ForwarderTypes {
		if strings.ToUpper(string(t)) == upper {
			return t, nil
		}
	}
	// Allow the wire-shape canonical lowercase form for ergonomics
	// (matches the JSON output exactly).
	lower := strings.ToLower(raw)
	for _, t := range smplkit.ForwarderTypes {
		if strings.ToLower(string(t)) == lower {
			return t, nil
		}
	}
	return "", fmt.Errorf("invalid forwarder --type %q. Valid: datadog, elastic, honeycomb, http, new_relic, splunk_hec, sumo_logic", raw)
}

func pickFilter(shape *forwarderFileShape, raw string) (map[string]interface{}, error) {
	if raw != "" {
		return loadJSONLogicValue(raw)
	}
	if shape != nil && shape.Filter != nil {
		return shape.Filter, nil
	}
	return nil, nil
}

func pickTransform(shape *forwarderFileShape, raw, transformType string) (interface{}, smplkit.ForwarderTransformType, error) {
	if raw == "" {
		if shape == nil || shape.Transform == nil {
			return nil, "", nil
		}
		tt := smplkit.ForwarderTransformTypeJSONata
		if shape.TransformType != nil {
			tt = smplkit.ForwarderTransformType(strings.ToUpper(*shape.TransformType))
		}
		return shape.Transform, tt, nil
	}
	tt := transformType
	if tt == "" {
		tt = "JSONATA"
	}
	v, err := loadTransformValue(raw, tt)
	if err != nil {
		return nil, "", err
	}
	return v, smplkit.ForwarderTransformType(strings.ToUpper(tt)), nil
}

func loadJSONLogicValue(raw string) (map[string]interface{}, error) {
	body, err := atFileOrLiteralCLI(raw)
	if err != nil {
		return nil, err
	}
	var v map[string]interface{}
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		return nil, fmt.Errorf("invalid JSON Logic: %w", err)
	}
	return v, nil
}

func loadTransformValue(raw, transformType string) (interface{}, error) {
	body, err := atFileOrLiteralCLI(raw)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(transformType, "JSONATA") {
		// JSONata expressions are strings — return verbatim, no JSON
		// decoding (a literal `$` parses fine but `account.id` does
		// not).
		return body, nil
	}
	// Future engines may carry structured shapes; fall back to JSON.
	var v interface{}
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		return body, nil // accept verbatim string if it's not valid JSON
	}
	return v, nil
}

// atFileOrLiteralCLI calls values.AtFileOrLiteral but is colocated here
// to keep this file's imports tight. cmd/values is already imported in
// the other noun files.
func atFileOrLiteralCLI(raw string) (string, error) {
	if strings.HasPrefix(raw, "@") {
		data, err := readFileFlag(strings.TrimPrefix(raw, "@"))
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	return raw, nil
}
