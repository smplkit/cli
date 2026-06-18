// Package output renders SDK models for the CLI.
//
// Three modes:
//
//   - table — aligned columns via text/tabwriter, human-readable.
//   - json  — JSON-encoded attribute payload (NOT the JSON:API envelope).
//   - yaml  — YAML-encoded attribute payload.
//
// `--quiet` collapses any list/get/create/set/delete output down to the
// resource's bare identifier so the CLI can be piped into xargs / loops.
//
// The table renderers each know their resource shape directly — the
// SDK models are small enough that a column-by-column mapping is
// clearer than a reflective renderer that has to special-case every
// type.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	smplkit "github.com/smplkit/go-sdk/v3"
	"gopkg.in/yaml.v3"

	"github.com/smplkit/cli/internal/cliconfig"
)

// Renderer carries the global output settings into each render call.
type Renderer struct {
	Out    io.Writer
	Format cliconfig.OutputFormat
	Quiet  bool
}

// NewRenderer returns a Renderer with sensible defaults applied.
func NewRenderer(out io.Writer, format cliconfig.OutputFormat, quiet bool) Renderer {
	if format == "" {
		format = cliconfig.OutputTable
	}
	return Renderer{Out: out, Format: format, Quiet: quiet}
}

// renderJSONOrYAML emits the attribute payload in the chosen encoding.
// Returns false when the format isn't json/yaml so the caller can fall
// through to a table renderer.
func (r Renderer) renderJSONOrYAML(v interface{}) (bool, error) {
	switch r.Format {
	case cliconfig.OutputJSON:
		enc := json.NewEncoder(r.Out)
		enc.SetIndent("", "  ")
		return true, enc.Encode(v)
	case cliconfig.OutputYAML:
		b, err := yaml.Marshal(v)
		if err != nil {
			return true, err
		}
		_, err = r.Out.Write(b)
		return true, err
	}
	return false, nil
}

// renderIdentifiers emits one id per line. Used when --quiet is on.
func (r Renderer) renderIdentifiers(ids []string) error {
	for _, id := range ids {
		if _, err := fmt.Fprintln(r.Out, id); err != nil {
			return err
		}
	}
	return nil
}

// ── Flag ─────────────────────────────────────────────────────────────

// FlagAttr is the JSON/YAML shape the CLI exposes for a flag — the SDK
// model's user-facing fields, no JSON:API envelope, no client back-
// reference. Pointer fields are preserved so `null` distinguishes
// "absent" from "explicit empty string".
type FlagAttr struct {
	ID           string                 `json:"id" yaml:"id"`
	Name         string                 `json:"name" yaml:"name"`
	Type         string                 `json:"type" yaml:"type"`
	Default      interface{}            `json:"default" yaml:"default"`
	Description  *string                `json:"description,omitempty" yaml:"description,omitempty"`
	Values       []FlagValueAttr        `json:"values,omitempty" yaml:"values,omitempty"`
	Environments map[string]interface{} `json:"environments,omitempty" yaml:"environments,omitempty"`
	CreatedAt    *time.Time             `json:"created_at,omitempty" yaml:"created_at,omitempty"`
	UpdatedAt    *time.Time             `json:"updated_at,omitempty" yaml:"updated_at,omitempty"`
}

// FlagValueAttr is the JSON/YAML shape for a constrained value entry.
type FlagValueAttr struct {
	Name  string      `json:"name" yaml:"name"`
	Value interface{} `json:"value" yaml:"value"`
}

// FlagToAttr projects a smplkit.Flag onto its attribute shape.
func FlagToAttr(f *smplkit.Flag) FlagAttr {
	out := FlagAttr{
		ID:           f.ID,
		Name:         f.Name,
		Type:         f.Type,
		Default:      f.Default,
		Description:  f.Description,
		Environments: f.Environments,
		CreatedAt:    f.CreatedAt,
		UpdatedAt:    f.UpdatedAt,
	}
	if f.Values != nil {
		out.Values = make([]FlagValueAttr, 0, len(*f.Values))
		for _, v := range *f.Values {
			out.Values = append(out.Values, FlagValueAttr{Name: v.Name, Value: v.Value})
		}
	}
	return out
}

// RenderFlag writes a single flag.
func (r Renderer) RenderFlag(f *smplkit.Flag) error {
	if r.Quiet {
		return r.renderIdentifiers([]string{f.ID})
	}
	if done, err := r.renderJSONOrYAML(FlagToAttr(f)); done {
		return err
	}
	return r.renderFlagTable([]*smplkit.Flag{f})
}

// RenderFlags writes a list of flags.
func (r Renderer) RenderFlags(fs []*smplkit.Flag) error {
	if r.Quiet {
		ids := make([]string, 0, len(fs))
		for _, f := range fs {
			ids = append(ids, f.ID)
		}
		return r.renderIdentifiers(ids)
	}
	if r.Format != cliconfig.OutputTable {
		attrs := make([]FlagAttr, 0, len(fs))
		for _, f := range fs {
			attrs = append(attrs, FlagToAttr(f))
		}
		if done, err := r.renderJSONOrYAML(attrs); done {
			return err
		}
	}
	return r.renderFlagTable(fs)
}

func (r Renderer) renderFlagTable(fs []*smplkit.Flag) error {
	tw := newTabWriter(r.Out)
	fmt.Fprintln(tw, "ID\tNAME\tTYPE\tDEFAULT\tENVIRONMENTS")
	for _, f := range fs {
		envs := envKeys(f.Environments)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			f.ID, f.Name, f.Type, scalarString(f.Default), strings.Join(envs, ","))
	}
	return tw.Flush()
}

// ── ConfigEntry ──────────────────────────────────────────────────────

// ConfigAttr is the JSON/YAML shape for a configuration resource.
type ConfigAttr struct {
	ID           string                            `json:"id" yaml:"id"`
	Name         string                            `json:"name" yaml:"name"`
	Description  *string                           `json:"description,omitempty" yaml:"description,omitempty"`
	Parent       *string                           `json:"parent,omitempty" yaml:"parent,omitempty"`
	Items        map[string]interface{}            `json:"items,omitempty" yaml:"items,omitempty"`
	Environments map[string]map[string]interface{} `json:"environments,omitempty" yaml:"environments,omitempty"`
	CreatedAt    *time.Time                        `json:"created_at,omitempty" yaml:"created_at,omitempty"`
	UpdatedAt    *time.Time                        `json:"updated_at,omitempty" yaml:"updated_at,omitempty"`
}

// ConfigToAttr projects a ConfigEntry onto its attribute shape.
func ConfigToAttr(c *smplkit.ConfigEntry) ConfigAttr {
	return ConfigAttr{
		ID:           c.ID,
		Name:         c.Name,
		Description:  c.Description,
		Parent:       c.Parent,
		Items:        c.Items,
		Environments: c.Environments,
		CreatedAt:    c.CreatedAt,
		UpdatedAt:    c.UpdatedAt,
	}
}

// RenderConfig writes a single ConfigEntry.
func (r Renderer) RenderConfig(c *smplkit.ConfigEntry) error {
	if r.Quiet {
		return r.renderIdentifiers([]string{c.ID})
	}
	if done, err := r.renderJSONOrYAML(ConfigToAttr(c)); done {
		return err
	}
	return r.renderConfigTable([]*smplkit.ConfigEntry{c})
}

// RenderConfigs writes a list of ConfigEntry.
func (r Renderer) RenderConfigs(cs []*smplkit.ConfigEntry) error {
	if r.Quiet {
		ids := make([]string, 0, len(cs))
		for _, c := range cs {
			ids = append(ids, c.ID)
		}
		return r.renderIdentifiers(ids)
	}
	if r.Format != cliconfig.OutputTable {
		attrs := make([]ConfigAttr, 0, len(cs))
		for _, c := range cs {
			attrs = append(attrs, ConfigToAttr(c))
		}
		if done, err := r.renderJSONOrYAML(attrs); done {
			return err
		}
	}
	return r.renderConfigTable(cs)
}

func (r Renderer) renderConfigTable(cs []*smplkit.ConfigEntry) error {
	tw := newTabWriter(r.Out)
	fmt.Fprintln(tw, "ID\tNAME\tPARENT\tITEMS\tENV OVERRIDES")
	for _, c := range cs {
		parent := ""
		if c.Parent != nil {
			parent = *c.Parent
		}
		envs := envKeysStrMap(c.Environments)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\n",
			c.ID, c.Name, parent, len(c.Items), strings.Join(envs, ","))
	}
	return tw.Flush()
}

// ── Logger ───────────────────────────────────────────────────────────

// LoggerAttr is the JSON/YAML shape for a logger.
type LoggerAttr struct {
	ID           string                   `json:"id" yaml:"id"`
	Name         string                   `json:"name" yaml:"name"`
	Level        *smplkit.LogLevel        `json:"level,omitempty" yaml:"level,omitempty"`
	Group        *string                  `json:"group,omitempty" yaml:"group,omitempty"`
	Managed      bool                     `json:"managed" yaml:"managed"`
	Sources      []map[string]interface{} `json:"sources,omitempty" yaml:"sources,omitempty"`
	Environments map[string]interface{}   `json:"environments,omitempty" yaml:"environments,omitempty"`
	CreatedAt    *time.Time               `json:"created_at,omitempty" yaml:"created_at,omitempty"`
	UpdatedAt    *time.Time               `json:"updated_at,omitempty" yaml:"updated_at,omitempty"`
}

// LoggerToAttr projects a Logger onto its attribute shape.
func LoggerToAttr(l *smplkit.Logger) LoggerAttr {
	return LoggerAttr{
		ID:           l.ID,
		Name:         l.Name,
		Level:        l.Level,
		Group:        l.Group,
		Managed:      l.Managed,
		Sources:      l.Sources,
		Environments: l.Environments,
		CreatedAt:    l.CreatedAt,
		UpdatedAt:    l.UpdatedAt,
	}
}

// RenderLogger writes a single logger.
func (r Renderer) RenderLogger(l *smplkit.Logger) error {
	if r.Quiet {
		return r.renderIdentifiers([]string{l.ID})
	}
	if done, err := r.renderJSONOrYAML(LoggerToAttr(l)); done {
		return err
	}
	return r.renderLoggerTable([]*smplkit.Logger{l})
}

// RenderLoggers writes a list of loggers.
func (r Renderer) RenderLoggers(ls []*smplkit.Logger) error {
	if r.Quiet {
		ids := make([]string, 0, len(ls))
		for _, l := range ls {
			ids = append(ids, l.ID)
		}
		return r.renderIdentifiers(ids)
	}
	if r.Format != cliconfig.OutputTable {
		attrs := make([]LoggerAttr, 0, len(ls))
		for _, l := range ls {
			attrs = append(attrs, LoggerToAttr(l))
		}
		if done, err := r.renderJSONOrYAML(attrs); done {
			return err
		}
	}
	return r.renderLoggerTable(ls)
}

func (r Renderer) renderLoggerTable(ls []*smplkit.Logger) error {
	tw := newTabWriter(r.Out)
	fmt.Fprintln(tw, "ID\tNAME\tLEVEL\tGROUP\tMANAGED\tENV OVERRIDES")
	for _, l := range ls {
		level := ""
		if l.Level != nil {
			level = string(*l.Level)
		}
		group := ""
		if l.Group != nil {
			group = *l.Group
		}
		envs := envKeys(l.Environments)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%t\t%s\n",
			l.ID, l.Name, level, group, l.Managed, strings.Join(envs, ","))
	}
	return tw.Flush()
}

// ── LogGroup ─────────────────────────────────────────────────────────

// LogGroupAttr is the JSON/YAML shape for a log group.
type LogGroupAttr struct {
	ID           string                 `json:"id" yaml:"id"`
	Name         string                 `json:"name" yaml:"name"`
	Level        *smplkit.LogLevel      `json:"level,omitempty" yaml:"level,omitempty"`
	Parent       *string                `json:"parent,omitempty" yaml:"parent,omitempty"`
	Environments map[string]interface{} `json:"environments,omitempty" yaml:"environments,omitempty"`
	CreatedAt    *time.Time             `json:"created_at,omitempty" yaml:"created_at,omitempty"`
	UpdatedAt    *time.Time             `json:"updated_at,omitempty" yaml:"updated_at,omitempty"`
}

// LogGroupToAttr projects a LogGroup onto its attribute shape.
func LogGroupToAttr(g *smplkit.LogGroup) LogGroupAttr {
	return LogGroupAttr{
		ID:           g.ID,
		Name:         g.Name,
		Level:        g.Level,
		Parent:       g.Group,
		Environments: g.Environments,
		CreatedAt:    g.CreatedAt,
		UpdatedAt:    g.UpdatedAt,
	}
}

// RenderLogGroup writes a single log group.
func (r Renderer) RenderLogGroup(g *smplkit.LogGroup) error {
	if r.Quiet {
		return r.renderIdentifiers([]string{g.ID})
	}
	if done, err := r.renderJSONOrYAML(LogGroupToAttr(g)); done {
		return err
	}
	return r.renderLogGroupTable([]*smplkit.LogGroup{g})
}

// RenderLogGroups writes a list of log groups.
func (r Renderer) RenderLogGroups(gs []*smplkit.LogGroup) error {
	if r.Quiet {
		ids := make([]string, 0, len(gs))
		for _, g := range gs {
			ids = append(ids, g.ID)
		}
		return r.renderIdentifiers(ids)
	}
	if r.Format != cliconfig.OutputTable {
		attrs := make([]LogGroupAttr, 0, len(gs))
		for _, g := range gs {
			attrs = append(attrs, LogGroupToAttr(g))
		}
		if done, err := r.renderJSONOrYAML(attrs); done {
			return err
		}
	}
	return r.renderLogGroupTable(gs)
}

func (r Renderer) renderLogGroupTable(gs []*smplkit.LogGroup) error {
	tw := newTabWriter(r.Out)
	fmt.Fprintln(tw, "ID\tNAME\tLEVEL\tPARENT\tENV OVERRIDES")
	for _, g := range gs {
		level := ""
		if g.Level != nil {
			level = string(*g.Level)
		}
		parent := ""
		if g.Group != nil {
			parent = *g.Group
		}
		envs := envKeys(g.Environments)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			g.ID, g.Name, level, parent, strings.Join(envs, ","))
	}
	return tw.Flush()
}

// ── Environment ──────────────────────────────────────────────────────

// EnvironmentAttr is the JSON/YAML shape for an environment.
type EnvironmentAttr struct {
	ID             string                            `json:"id" yaml:"id"`
	Name           string                            `json:"name" yaml:"name"`
	Color          *string                           `json:"color,omitempty" yaml:"color,omitempty"`
	Classification smplkit.EnvironmentClassification `json:"classification" yaml:"classification"`
	CreatedAt      *time.Time                        `json:"created_at,omitempty" yaml:"created_at,omitempty"`
	UpdatedAt      *time.Time                        `json:"updated_at,omitempty" yaml:"updated_at,omitempty"`
}

// EnvironmentToAttr projects an Environment.
func EnvironmentToAttr(e *smplkit.Environment) EnvironmentAttr {
	return EnvironmentAttr{
		ID:             e.ID,
		Name:           e.Name,
		Color:          e.Color,
		Classification: e.Classification,
		CreatedAt:      e.CreatedAt,
		UpdatedAt:      e.UpdatedAt,
	}
}

// RenderEnvironment writes a single environment.
func (r Renderer) RenderEnvironment(e *smplkit.Environment) error {
	if r.Quiet {
		return r.renderIdentifiers([]string{e.ID})
	}
	if done, err := r.renderJSONOrYAML(EnvironmentToAttr(e)); done {
		return err
	}
	return r.renderEnvironmentTable([]*smplkit.Environment{e})
}

// RenderEnvironments writes a list of environments.
func (r Renderer) RenderEnvironments(es []*smplkit.Environment) error {
	if r.Quiet {
		ids := make([]string, 0, len(es))
		for _, e := range es {
			ids = append(ids, e.ID)
		}
		return r.renderIdentifiers(ids)
	}
	if r.Format != cliconfig.OutputTable {
		attrs := make([]EnvironmentAttr, 0, len(es))
		for _, e := range es {
			attrs = append(attrs, EnvironmentToAttr(e))
		}
		if done, err := r.renderJSONOrYAML(attrs); done {
			return err
		}
	}
	return r.renderEnvironmentTable(es)
}

func (r Renderer) renderEnvironmentTable(es []*smplkit.Environment) error {
	tw := newTabWriter(r.Out)
	fmt.Fprintln(tw, "ID\tNAME\tCOLOR\tCLASSIFICATION")
	for _, e := range es {
		color := ""
		if e.Color != nil {
			color = *e.Color
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
			e.ID, e.Name, color, string(e.Classification))
	}
	return tw.Flush()
}

// ── Service ──────────────────────────────────────────────────────────

// ServiceAttr is the JSON/YAML shape for a service.
type ServiceAttr struct {
	ID        string     `json:"id" yaml:"id"`
	Name      string     `json:"name" yaml:"name"`
	CreatedAt *time.Time `json:"created_at,omitempty" yaml:"created_at,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty" yaml:"updated_at,omitempty"`
}

// ServiceToAttr projects a Service.
func ServiceToAttr(s *smplkit.Service) ServiceAttr {
	return ServiceAttr{
		ID:        s.ID,
		Name:      s.Name,
		CreatedAt: s.CreatedAt,
		UpdatedAt: s.UpdatedAt,
	}
}

// RenderService writes a single service.
func (r Renderer) RenderService(s *smplkit.Service) error {
	if r.Quiet {
		return r.renderIdentifiers([]string{s.ID})
	}
	if done, err := r.renderJSONOrYAML(ServiceToAttr(s)); done {
		return err
	}
	return r.renderServiceTable([]*smplkit.Service{s})
}

// RenderServices writes a list of services.
func (r Renderer) RenderServices(ss []*smplkit.Service) error {
	if r.Quiet {
		ids := make([]string, 0, len(ss))
		for _, s := range ss {
			ids = append(ids, s.ID)
		}
		return r.renderIdentifiers(ids)
	}
	if r.Format != cliconfig.OutputTable {
		attrs := make([]ServiceAttr, 0, len(ss))
		for _, s := range ss {
			attrs = append(attrs, ServiceToAttr(s))
		}
		if done, err := r.renderJSONOrYAML(attrs); done {
			return err
		}
	}
	return r.renderServiceTable(ss)
}

func (r Renderer) renderServiceTable(ss []*smplkit.Service) error {
	tw := newTabWriter(r.Out)
	fmt.Fprintln(tw, "ID\tNAME")
	for _, s := range ss {
		fmt.Fprintf(tw, "%s\t%s\n", s.ID, s.Name)
	}
	return tw.Flush()
}

// ── Audit Forwarder ──────────────────────────────────────────────────

// ForwarderAttr is the JSON/YAML shape for an audit forwarder.
//
// Enablement is per-environment (ADR-055): a forwarder delivers in an
// environment only when that environment has an entry in Environments
// with enabled=true. There is no global on/off switch — the SDK's base
// Enabled field is read-only and always false, so it is not surfaced.
type ForwarderAttr struct {
	ID            string                          `json:"id" yaml:"id"`
	Name          string                          `json:"name" yaml:"name"`
	Description   *string                         `json:"description,omitempty" yaml:"description,omitempty"`
	ForwarderType smplkit.ForwarderType           `json:"type" yaml:"type"`
	Environments  map[string]ForwarderEnvAttr     `json:"environments,omitempty" yaml:"environments,omitempty"`
	Configuration ForwarderHTTPConfigAttr         `json:"configuration" yaml:"configuration"`
	Filter        map[string]interface{}          `json:"filter,omitempty" yaml:"filter,omitempty"`
	Transform     interface{}                     `json:"transform,omitempty" yaml:"transform,omitempty"`
	TransformType *smplkit.ForwarderTransformType `json:"transform_type,omitempty" yaml:"transform_type,omitempty"`
	// ForwardSmplkitEvents, when true, also delivers smplkit's own
	// platform change events (flag, configuration, and similar changes)
	// through this forwarder. Nil/false (the default) means they are not
	// forwarded.
	ForwardSmplkitEvents *bool      `json:"forward_smplkit_events,omitempty" yaml:"forward_smplkit_events,omitempty"`
	CreatedAt            *time.Time `json:"created_at,omitempty" yaml:"created_at,omitempty"`
	UpdatedAt            *time.Time `json:"updated_at,omitempty" yaml:"updated_at,omitempty"`
	Version              *int       `json:"version,omitempty" yaml:"version,omitempty"`
}

// ForwarderEnvAttr is the JSON/YAML shape for a per-environment override.
// A nil Configuration inherits the forwarder's base configuration for
// that environment.
type ForwarderEnvAttr struct {
	Enabled       bool                     `json:"enabled" yaml:"enabled"`
	Configuration *ForwarderHTTPConfigAttr `json:"configuration,omitempty" yaml:"configuration,omitempty"`
}

// ForwarderHTTPConfigAttr is the JSON/YAML shape for the HTTP destination
// configuration (currently the only supported transport family).
type ForwarderHTTPConfigAttr struct {
	URL           string                `json:"url" yaml:"url"`
	Method        smplkit.HttpMethod    `json:"method,omitempty" yaml:"method,omitempty"`
	Headers       []ForwarderHeaderAttr `json:"headers,omitempty" yaml:"headers,omitempty"`
	SuccessStatus string                `json:"success_status,omitempty" yaml:"success_status,omitempty"`
	TLSVerify     *bool                 `json:"tls_verify,omitempty" yaml:"tls_verify,omitempty"`
	CACert        *string               `json:"ca_cert,omitempty" yaml:"ca_cert,omitempty"`
}

// ForwarderHeaderAttr is the JSON/YAML shape for an HTTP header pair.
type ForwarderHeaderAttr struct {
	Name  string `json:"name" yaml:"name"`
	Value string `json:"value" yaml:"value"`
}

// httpConfigToAttr projects an SDK HttpConfiguration onto its attribute
// shape. Shared by the base configuration and per-environment overrides.
func httpConfigToAttr(c smplkit.HttpConfiguration) ForwarderHTTPConfigAttr {
	cfg := ForwarderHTTPConfigAttr{
		URL:           c.URL,
		Method:        c.Method,
		SuccessStatus: c.SuccessStatus,
		TLSVerify:     c.TlsVerify,
		CACert:        c.CaCert,
	}
	for _, h := range c.Headers {
		cfg.Headers = append(cfg.Headers, ForwarderHeaderAttr{Name: h.Name, Value: h.Value})
	}
	return cfg
}

// ForwarderToAttr projects a Forwarder.
func ForwarderToAttr(f *smplkit.Forwarder) ForwarderAttr {
	var envs map[string]ForwarderEnvAttr
	if len(f.Environments) > 0 {
		envs = make(map[string]ForwarderEnvAttr, len(f.Environments))
		for k, e := range f.Environments {
			attr := ForwarderEnvAttr{Enabled: e.Enabled}
			if e.Configuration != nil {
				cfg := httpConfigToAttr(*e.Configuration)
				attr.Configuration = &cfg
			}
			envs[k] = attr
		}
	}
	return ForwarderAttr{
		ID:                   f.ID,
		Name:                 f.Name,
		Description:          f.Description,
		ForwarderType:        f.ForwarderType,
		Environments:         envs,
		Configuration:        httpConfigToAttr(f.Configuration),
		Filter:               f.Filter,
		Transform:            f.Transform,
		TransformType:        f.TransformType,
		ForwardSmplkitEvents: f.ForwardSmplkitEvents,
		CreatedAt:            f.CreatedAt,
		UpdatedAt:            f.UpdatedAt,
		Version:              f.Version,
	}
}

// RenderForwarder writes a single forwarder.
func (r Renderer) RenderForwarder(f *smplkit.Forwarder) error {
	if r.Quiet {
		return r.renderIdentifiers([]string{f.ID})
	}
	if done, err := r.renderJSONOrYAML(ForwarderToAttr(f)); done {
		return err
	}
	return r.renderForwarderTable([]smplkit.Forwarder{*f})
}

// RenderForwarders writes a list of forwarders.
func (r Renderer) RenderForwarders(fs []smplkit.Forwarder) error {
	if r.Quiet {
		ids := make([]string, 0, len(fs))
		for _, f := range fs {
			ids = append(ids, f.ID)
		}
		return r.renderIdentifiers(ids)
	}
	if r.Format != cliconfig.OutputTable {
		attrs := make([]ForwarderAttr, 0, len(fs))
		for i := range fs {
			attrs = append(attrs, ForwarderToAttr(&fs[i]))
		}
		if done, err := r.renderJSONOrYAML(attrs); done {
			return err
		}
	}
	return r.renderForwarderTable(fs)
}

func (r Renderer) renderForwarderTable(fs []smplkit.Forwarder) error {
	tw := newTabWriter(r.Out)
	fmt.Fprintln(tw, "ID\tNAME\tTYPE\tENABLED ENVS\tSMPL EVENTS\tURL")
	for _, f := range fs {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%t\t%s\n",
			f.ID, f.Name, string(f.ForwarderType),
			strings.Join(enabledEnvKeys(f.Environments), ","),
			f.ForwardSmplkitEvents != nil && *f.ForwardSmplkitEvents,
			f.Configuration.URL)
	}
	return tw.Flush()
}

// enabledEnvKeys returns the sorted environment keys whose entry is
// enabled. A forwarder delivers only in these environments.
func enabledEnvKeys(m map[string]smplkit.ForwarderEnvironment) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k, e := range m {
		if e.Enabled {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// ── Job ──────────────────────────────────────────────────────────────

// JobAttr is the JSON/YAML shape the CLI exposes for a scheduled job —
// the SDK model's user-facing fields, no JSON:API envelope, no client
// back-reference. Header values round-trip plaintext, so a
// `get -o json` snapshot replayed through `apply -f` preserves
// credentials.
type JobAttr struct {
	ID          string  `json:"id" yaml:"id"`
	Name        string  `json:"name" yaml:"name"`
	Description *string `json:"description,omitempty" yaml:"description,omitempty"`
	// Enabled is the read-only roll-up: true when the job is enabled in at
	// least one environment. Derived server-side from Environments, so it is
	// surfaced on reads but ignored on writes — set enablement per
	// environment via Environments.
	Enabled bool `json:"enabled" yaml:"enabled"`
	// Recurring is read-only: true for a cron schedule, false for a one-off
	// (datetime / "now") schedule. Nil for an unsaved job.
	Recurring         *bool                 `json:"recurring,omitempty" yaml:"recurring,omitempty"`
	Type              string                `json:"type,omitempty" yaml:"type,omitempty"`
	Schedule          string                `json:"schedule" yaml:"schedule"`
	ConcurrencyPolicy string                `json:"concurrency_policy,omitempty" yaml:"concurrency_policy,omitempty"`
	Environments      map[string]JobEnvAttr `json:"environments,omitempty" yaml:"environments,omitempty"`
	Configuration     JobHTTPConfigAttr     `json:"configuration" yaml:"configuration"`
	NextRunAt         *time.Time            `json:"next_run_at,omitempty" yaml:"next_run_at,omitempty"`
	CreatedAt         *time.Time            `json:"created_at,omitempty" yaml:"created_at,omitempty"`
	UpdatedAt         *time.Time            `json:"updated_at,omitempty" yaml:"updated_at,omitempty"`
	Version           *int                  `json:"version,omitempty" yaml:"version,omitempty"`
}

// JobEnvAttr is the JSON/YAML shape for a job's per-environment override: a
// recurring job fires in an environment only when that environment's entry
// has enabled=true, and an optional configuration that fully replaces the
// base configuration in that environment (omit it to inherit the base).
type JobEnvAttr struct {
	Enabled       bool               `json:"enabled" yaml:"enabled"`
	Configuration *JobHTTPConfigAttr `json:"configuration,omitempty" yaml:"configuration,omitempty"`
}

// JobHTTPConfigAttr is the JSON/YAML shape for the HTTP request a job
// fires when it runs.
type JobHTTPConfigAttr struct {
	URL           string          `json:"url" yaml:"url"`
	Method        string          `json:"method,omitempty" yaml:"method,omitempty"`
	Headers       []JobHeaderAttr `json:"headers,omitempty" yaml:"headers,omitempty"`
	Body          *string         `json:"body,omitempty" yaml:"body,omitempty"`
	SuccessStatus string          `json:"success_status,omitempty" yaml:"success_status,omitempty"`
	Timeout       int             `json:"timeout,omitempty" yaml:"timeout,omitempty"`
	TLSVerify     *bool           `json:"tls_verify,omitempty" yaml:"tls_verify,omitempty"`
	CACert        *string         `json:"ca_cert,omitempty" yaml:"ca_cert,omitempty"`
}

// JobHeaderAttr is the JSON/YAML shape for an HTTP header pair.
type JobHeaderAttr struct {
	Name  string `json:"name" yaml:"name"`
	Value string `json:"value" yaml:"value"`
}

// jobHTTPConfigToAttr projects an SDK HttpConfig onto its attribute shape.
func jobHTTPConfigToAttr(c smplkit.HttpConfig) JobHTTPConfigAttr {
	cfg := JobHTTPConfigAttr{
		URL:           c.URL,
		Method:        string(c.Method),
		Body:          c.Body,
		SuccessStatus: c.SuccessStatus,
		Timeout:       c.Timeout,
		TLSVerify:     c.TlsVerify,
		CACert:        c.CaCert,
	}
	for _, h := range c.Headers {
		cfg.Headers = append(cfg.Headers, JobHeaderAttr{Name: h.Name, Value: h.Value})
	}
	return cfg
}

// JobToAttr projects a smplkit.Job onto its attribute shape.
func JobToAttr(j *smplkit.Job) JobAttr {
	a := JobAttr{
		ID:                j.ID,
		Name:              j.Name,
		Description:       j.Description,
		Enabled:           j.Enabled,
		Recurring:         j.Recurring,
		Type:              j.Type,
		Schedule:          j.Schedule,
		ConcurrencyPolicy: j.ConcurrencyPolicy,
		Configuration:     jobHTTPConfigToAttr(j.Configuration),
		NextRunAt:         j.NextRunAt,
		CreatedAt:         j.CreatedAt,
		UpdatedAt:         j.UpdatedAt,
		Version:           j.Version,
	}
	if len(j.Environments) > 0 {
		envs := make(map[string]JobEnvAttr, len(j.Environments))
		for k, e := range j.Environments {
			ea := JobEnvAttr{Enabled: e.Enabled}
			if e.Configuration != nil {
				c := jobHTTPConfigToAttr(*e.Configuration)
				ea.Configuration = &c
			}
			envs[k] = ea
		}
		a.Environments = envs
	}
	return a
}

// RenderJob writes a single job.
func (r Renderer) RenderJob(j *smplkit.Job) error {
	if r.Quiet {
		return r.renderIdentifiers([]string{j.ID})
	}
	if done, err := r.renderJSONOrYAML(JobToAttr(j)); done {
		return err
	}
	return r.renderJobTable([]*smplkit.Job{j})
}

// RenderJobs writes a list of jobs.
func (r Renderer) RenderJobs(js []*smplkit.Job) error {
	if r.Quiet {
		ids := make([]string, 0, len(js))
		for _, j := range js {
			ids = append(ids, j.ID)
		}
		return r.renderIdentifiers(ids)
	}
	if r.Format != cliconfig.OutputTable {
		attrs := make([]JobAttr, 0, len(js))
		for _, j := range js {
			attrs = append(attrs, JobToAttr(j))
		}
		if done, err := r.renderJSONOrYAML(attrs); done {
			return err
		}
	}
	return r.renderJobTable(js)
}

func (r Renderer) renderJobTable(js []*smplkit.Job) error {
	tw := newTabWriter(r.Out)
	fmt.Fprintln(tw, "ID\tNAME\tSCHEDULE\tENABLED ENVS\tMETHOD\tURL\tNEXT RUN")
	for _, j := range js {
		method := string(j.Configuration.Method)
		if method == "" {
			method = "POST"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			j.ID, j.Name, j.Schedule, strings.Join(enabledJobEnvKeys(j.Environments), ","),
			method, j.Configuration.URL, formatTime(j.NextRunAt))
	}
	return tw.Flush()
}

// enabledJobEnvKeys returns the sorted environment keys in which the job is
// enabled. A recurring job fires only in these environments.
func enabledJobEnvKeys(m map[string]smplkit.JobEnvironment) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k, e := range m {
		if e.Enabled {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// ── Run ──────────────────────────────────────────────────────────────

// RunAttr is the JSON/YAML shape for a single job run (read-only). It is a
// complete projection of the SDK Run model; timing and forensics fields
// are omitempty since a freshly-triggered run has not populated them yet.
type RunAttr struct {
	ID         string `json:"id" yaml:"id"`
	Job        string `json:"job" yaml:"job"`
	JobVersion *int   `json:"job_version,omitempty" yaml:"job_version,omitempty"`
	// Environment is the environment this run executed in. A scheduled run
	// inherits the firing job-environment; a manual run uses the environment
	// it was triggered in; a rerun copies its source run's environment.
	Environment       string                 `json:"environment" yaml:"environment"`
	Trigger           string                 `json:"trigger" yaml:"trigger"`
	RerunOf           *string                `json:"rerun_of,omitempty" yaml:"rerun_of,omitempty"`
	Status            string                 `json:"status" yaml:"status"`
	ScheduledFor      *time.Time             `json:"scheduled_for,omitempty" yaml:"scheduled_for,omitempty"`
	StartedAt         *time.Time             `json:"started_at,omitempty" yaml:"started_at,omitempty"`
	FinishedAt        *time.Time             `json:"finished_at,omitempty" yaml:"finished_at,omitempty"`
	PendingDurationMs *int                   `json:"pending_duration_ms,omitempty" yaml:"pending_duration_ms,omitempty"`
	RunDurationMs     *int                   `json:"run_duration_ms,omitempty" yaml:"run_duration_ms,omitempty"`
	TotalDurationMs   *int                   `json:"total_duration_ms,omitempty" yaml:"total_duration_ms,omitempty"`
	FailureReason     *string                `json:"failure_reason,omitempty" yaml:"failure_reason,omitempty"`
	Error             *string                `json:"error,omitempty" yaml:"error,omitempty"`
	Request           map[string]interface{} `json:"request,omitempty" yaml:"request,omitempty"`
	Result            map[string]interface{} `json:"result,omitempty" yaml:"result,omitempty"`
	CreatedAt         *time.Time             `json:"created_at,omitempty" yaml:"created_at,omitempty"`
}

// RunToAttr projects a smplkit.Run onto its attribute shape.
func RunToAttr(run *smplkit.Run) RunAttr {
	return RunAttr{
		ID:                run.ID,
		Job:               run.Job,
		JobVersion:        run.JobVersion,
		Environment:       run.Environment,
		Trigger:           run.Trigger,
		RerunOf:           run.RerunOf,
		Status:            run.Status,
		ScheduledFor:      run.ScheduledFor,
		StartedAt:         run.StartedAt,
		FinishedAt:        run.FinishedAt,
		PendingDurationMs: run.PendingDurationMs,
		RunDurationMs:     run.RunDurationMs,
		TotalDurationMs:   run.TotalDurationMs,
		FailureReason:     run.FailureReason,
		Error:             run.Error,
		Request:           run.Request,
		Result:            run.Result,
		CreatedAt:         run.CreatedAt,
	}
}

// RenderRun writes a single run.
func (r Renderer) RenderRun(run *smplkit.Run) error {
	if r.Quiet {
		return r.renderIdentifiers([]string{run.ID})
	}
	if done, err := r.renderJSONOrYAML(RunToAttr(run)); done {
		return err
	}
	return r.renderRunTable([]*smplkit.Run{run})
}

// RenderRuns writes a list of runs.
func (r Renderer) RenderRuns(runs []*smplkit.Run) error {
	if r.Quiet {
		ids := make([]string, 0, len(runs))
		for _, run := range runs {
			ids = append(ids, run.ID)
		}
		return r.renderIdentifiers(ids)
	}
	if r.Format != cliconfig.OutputTable {
		attrs := make([]RunAttr, 0, len(runs))
		for _, run := range runs {
			attrs = append(attrs, RunToAttr(run))
		}
		if done, err := r.renderJSONOrYAML(attrs); done {
			return err
		}
	}
	return r.renderRunTable(runs)
}

func (r Renderer) renderRunTable(runs []*smplkit.Run) error {
	tw := newTabWriter(r.Out)
	fmt.Fprintln(tw, "ID\tJOB\tENV\tTRIGGER\tSTATUS\tCREATED")
	for _, run := range runs {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			run.ID, run.Job, run.Environment, run.Trigger, run.Status, formatTime(run.CreatedAt))
	}
	return tw.Flush()
}

// ── Usage ────────────────────────────────────────────────────────────

// UsageAttr is the JSON/YAML shape for the account's current-period jobs
// usage (read-only). A complete projection of the SDK Usage model; -1 in
// the *_included / *_limit fields means the plan imposes no cap.
type UsageAttr struct {
	Period          string `json:"period" yaml:"period"`
	RunsUsed        int    `json:"runs_used" yaml:"runs_used"`
	RunsIncluded    int    `json:"runs_included" yaml:"runs_included"`
	ActiveJobs      int    `json:"active_jobs" yaml:"active_jobs"`
	ActiveJobsLimit int    `json:"active_jobs_limit" yaml:"active_jobs_limit"`
}

// UsageToAttr projects a smplkit.Usage onto its attribute shape.
func UsageToAttr(u *smplkit.Usage) UsageAttr {
	return UsageAttr{
		Period:          u.Period,
		RunsUsed:        u.RunsUsed,
		RunsIncluded:    u.RunsIncluded,
		ActiveJobs:      u.ActiveJobs,
		ActiveJobsLimit: u.ActiveJobsLimit,
	}
}

// RenderUsage writes the current-period jobs usage.
func (r Renderer) RenderUsage(u *smplkit.Usage) error {
	if r.Quiet {
		return r.renderIdentifiers([]string{u.Period})
	}
	if done, err := r.renderJSONOrYAML(UsageToAttr(u)); done {
		return err
	}
	tw := newTabWriter(r.Out)
	fmt.Fprintln(tw, "PERIOD\tRUNS USED\tRUNS INCLUDED\tACTIVE JOBS\tACTIVE JOBS LIMIT")
	fmt.Fprintf(tw, "%s\t%d\t%s\t%d\t%s\n",
		u.Period, u.RunsUsed, formatLimit(u.RunsIncluded),
		u.ActiveJobs, formatLimit(u.ActiveJobsLimit))
	return tw.Flush()
}

// formatLimit renders a plan allotment for a table cell: -1 (no cap) reads
// as "unlimited"; any other value is the number itself.
func formatLimit(n int) string {
	if n < 0 {
		return "unlimited"
	}
	return fmt.Sprintf("%d", n)
}

// ── Helpers ──────────────────────────────────────────────────────────

func newTabWriter(out io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
}

// envKeys returns the sorted keys of a map[string]interface{}.
func envKeys(m map[string]interface{}) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// envKeysStrMap returns the sorted keys of a map[string]map[string]interface{}.
func envKeysStrMap(m map[string]map[string]interface{}) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// formatTime renders an optional timestamp for a table cell, empty when
// nil. Uses RFC3339 so the value is unambiguous and machine-parseable.
func formatTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format(time.RFC3339)
}

// scalarString renders a primitive value for a table cell. Falls back
// to JSON encoding for compound values.
func scalarString(v interface{}) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case float64:
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%f", x), "0"), ".")
	case int, int64:
		return fmt.Sprintf("%d", x)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(b)
	}
}
