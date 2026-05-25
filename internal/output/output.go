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
type ForwarderAttr struct {
	ID            string                          `json:"id" yaml:"id"`
	Name          string                          `json:"name" yaml:"name"`
	Description   *string                         `json:"description,omitempty" yaml:"description,omitempty"`
	ForwarderType smplkit.ForwarderType           `json:"type" yaml:"type"`
	Enabled       bool                            `json:"enabled" yaml:"enabled"`
	Configuration ForwarderHTTPConfigAttr         `json:"configuration" yaml:"configuration"`
	Filter        map[string]interface{}          `json:"filter,omitempty" yaml:"filter,omitempty"`
	Transform     interface{}                     `json:"transform,omitempty" yaml:"transform,omitempty"`
	TransformType *smplkit.ForwarderTransformType `json:"transform_type,omitempty" yaml:"transform_type,omitempty"`
	CreatedAt     *time.Time                      `json:"created_at,omitempty" yaml:"created_at,omitempty"`
	UpdatedAt     *time.Time                      `json:"updated_at,omitempty" yaml:"updated_at,omitempty"`
	Version       *int                            `json:"version,omitempty" yaml:"version,omitempty"`
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

// ForwarderToAttr projects a Forwarder.
func ForwarderToAttr(f *smplkit.Forwarder) ForwarderAttr {
	cfg := ForwarderHTTPConfigAttr{
		URL:           f.Configuration.URL,
		Method:        f.Configuration.Method,
		SuccessStatus: f.Configuration.SuccessStatus,
		TLSVerify:     f.Configuration.TlsVerify,
		CACert:        f.Configuration.CaCert,
	}
	for _, h := range f.Configuration.Headers {
		cfg.Headers = append(cfg.Headers, ForwarderHeaderAttr{Name: h.Name, Value: h.Value})
	}
	return ForwarderAttr{
		ID:            f.ID,
		Name:          f.Name,
		Description:   f.Description,
		ForwarderType: f.ForwarderType,
		Enabled:       f.Enabled,
		Configuration: cfg,
		Filter:        f.Filter,
		Transform:     f.Transform,
		TransformType: f.TransformType,
		CreatedAt:     f.CreatedAt,
		UpdatedAt:     f.UpdatedAt,
		Version:       f.Version,
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
	fmt.Fprintln(tw, "ID\tNAME\tTYPE\tENABLED\tURL")
	for _, f := range fs {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%t\t%s\n",
			f.ID, f.Name, string(f.ForwarderType), f.Enabled, f.Configuration.URL)
	}
	return tw.Flush()
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
