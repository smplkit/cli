package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	smplkit "github.com/smplkit/go-sdk/v3"

	"github.com/smplkit/cli/internal/cliconfig"
)

func TestRenderer_Flag_JSON(t *testing.T) {
	desc := "demo"
	f := &smplkit.Flag{
		ID:           "dark-mode",
		Name:         "Dark Mode",
		Type:         "BOOLEAN",
		Default:      false,
		Description:  &desc,
		Environments: map[string]interface{}{"production": map[string]interface{}{"enabled": true}},
	}

	var buf bytes.Buffer
	r := NewRenderer(&buf, cliconfig.OutputJSON, false)
	if err := r.RenderFlag(f); err != nil {
		t.Fatalf("RenderFlag: %v", err)
	}

	var got FlagAttr
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ID != "dark-mode" || got.Type != "BOOLEAN" || got.Description == nil || *got.Description != "demo" {
		t.Errorf("got %+v", got)
	}
	if _, ok := got.Environments["production"]; !ok {
		t.Error("missing production env")
	}
}

func TestRenderer_Flag_Quiet(t *testing.T) {
	f := &smplkit.Flag{ID: "x"}
	var buf bytes.Buffer
	r := NewRenderer(&buf, cliconfig.OutputJSON, true)
	if err := r.RenderFlag(f); err != nil {
		t.Fatalf("RenderFlag: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "x" {
		t.Errorf("quiet: got %q", got)
	}
}

func TestRenderer_Flags_TableHasHeader(t *testing.T) {
	flags := []*smplkit.Flag{
		{ID: "a", Name: "A", Type: "STRING", Default: "hi"},
		{ID: "b", Name: "B", Type: "BOOLEAN", Default: true},
	}
	var buf bytes.Buffer
	r := NewRenderer(&buf, cliconfig.OutputTable, false)
	if err := r.RenderFlags(flags); err != nil {
		t.Fatalf("RenderFlags: %v", err)
	}
	out := buf.String()
	if !strings.HasPrefix(out, "ID") {
		t.Errorf("expected header, got %q", out)
	}
	if !strings.Contains(out, "STRING") || !strings.Contains(out, "BOOLEAN") {
		t.Errorf("missing rows: %q", out)
	}
}

func TestRenderer_Environment_YAML(t *testing.T) {
	color := "#10b981"
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	e := &smplkit.Environment{
		ID: "staging", Name: "Staging", Color: &color,
		Classification: smplkit.EnvironmentClassificationStandard,
		CreatedAt:      &created,
	}
	var buf bytes.Buffer
	r := NewRenderer(&buf, cliconfig.OutputYAML, false)
	if err := r.RenderEnvironment(e); err != nil {
		t.Fatalf("RenderEnvironment: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "id: staging") {
		t.Errorf("YAML missing id: %q", out)
	}
	if !strings.Contains(out, "classification: STANDARD") {
		t.Errorf("YAML missing classification: %q", out)
	}
}

func TestRenderer_Forwarder_Table(t *testing.T) {
	forward := true
	fwd := smplkit.Forwarder{
		ID:            "siem",
		Name:          "SIEM",
		ForwarderType: smplkit.ForwarderTypeHTTP,
		Environments: map[string]smplkit.ForwarderEnvironment{
			"production": {Enabled: true},
			"staging":    {Enabled: false},
		},
		ForwardSmplkitEvents: &forward,
		Configuration:        smplkit.HttpConfiguration{URL: "https://example.com"},
	}
	var buf bytes.Buffer
	r := NewRenderer(&buf, cliconfig.OutputTable, false)
	if err := r.RenderForwarders([]smplkit.Forwarder{fwd}); err != nil {
		t.Fatalf("RenderForwarders: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "siem") || !strings.Contains(out, "https://example.com") {
		t.Errorf("table missing rows: %q", out)
	}
	if !strings.Contains(out, "ENABLED ENVS") {
		t.Errorf("table missing enabled-envs header: %q", out)
	}
	if !strings.Contains(out, "SMPL EVENTS") {
		t.Errorf("table missing smpl-events header: %q", out)
	}
	// forward_smplkit_events=true renders as "true" in its column.
	if !strings.Contains(out, "true") {
		t.Errorf("smpl-events column should show true: %q", out)
	}
	// Only enabled environments appear in the column.
	if !strings.Contains(out, "production") || strings.Contains(out, "staging") {
		t.Errorf("enabled-envs column should list production only: %q", out)
	}
}

func TestRenderer_Forwarder_Table_SmplEventsFalseWhenNil(t *testing.T) {
	fwd := smplkit.Forwarder{
		ID:            "siem",
		Name:          "SIEM",
		ForwarderType: smplkit.ForwarderTypeHTTP,
		Configuration: smplkit.HttpConfiguration{URL: "https://example.com"},
	}
	var buf bytes.Buffer
	r := NewRenderer(&buf, cliconfig.OutputTable, false)
	if err := r.RenderForwarder(&fwd); err != nil {
		t.Fatalf("RenderForwarder: %v", err)
	}
	if !strings.Contains(buf.String(), "false") {
		t.Errorf("nil ForwardSmplkitEvents should render as false: %q", buf.String())
	}
}

func TestRenderer_Forwarder_JSON_SmplEvents(t *testing.T) {
	forward := true
	fwd := smplkit.Forwarder{
		ID:                   "siem",
		Name:                 "SIEM",
		ForwarderType:        smplkit.ForwarderTypeHTTP,
		ForwardSmplkitEvents: &forward,
		Configuration:        smplkit.HttpConfiguration{URL: "https://example.com"},
	}
	var buf bytes.Buffer
	r := NewRenderer(&buf, cliconfig.OutputJSON, false)
	if err := r.RenderForwarder(&fwd); err != nil {
		t.Fatalf("RenderForwarder: %v", err)
	}
	var got ForwarderAttr
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ForwardSmplkitEvents == nil || !*got.ForwardSmplkitEvents {
		t.Errorf("forward_smplkit_events not projected: %+v", got.ForwardSmplkitEvents)
	}
	if !strings.Contains(buf.String(), "forward_smplkit_events") {
		t.Errorf("JSON key forward_smplkit_events missing: %q", buf.String())
	}
}

func TestRenderer_Forwarder_JSON_Environments(t *testing.T) {
	override := smplkit.HttpConfiguration{URL: "https://prod.example.com"}
	fwd := smplkit.Forwarder{
		ID:            "siem",
		Name:          "SIEM",
		ForwarderType: smplkit.ForwarderTypeHTTP,
		Environments: map[string]smplkit.ForwarderEnvironment{
			"production": {Enabled: true, Configuration: &override},
		},
		Configuration: smplkit.HttpConfiguration{URL: "https://base.example.com"},
	}
	var buf bytes.Buffer
	r := NewRenderer(&buf, cliconfig.OutputJSON, false)
	if err := r.RenderForwarder(&fwd); err != nil {
		t.Fatalf("RenderForwarder: %v", err)
	}
	var got ForwarderAttr
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	prod, ok := got.Environments["production"]
	if !ok || !prod.Enabled {
		t.Fatalf("production env missing or not enabled: %+v", got.Environments)
	}
	if prod.Configuration == nil || prod.Configuration.URL != "https://prod.example.com" {
		t.Errorf("per-env configuration override not projected: %+v", prod.Configuration)
	}
}

func TestScalarString(t *testing.T) {
	cases := []struct {
		in   interface{}
		want string
	}{
		{nil, ""},
		{"x", "x"},
		{true, "true"},
		{false, "false"},
		{3.0, "3"},
		{3.5, "3.5"},
		{map[string]interface{}{"a": 1}, `{"a":1}`},
	}
	for _, c := range cases {
		got := scalarString(c.in)
		if got != c.want {
			t.Errorf("scalarString(%#v) = %q want %q", c.in, got, c.want)
		}
	}
}
