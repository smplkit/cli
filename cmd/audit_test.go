package cmd

import (
	"strings"
	"testing"

	smplkit "github.com/smplkit/go-sdk/v3"

	"github.com/smplkit/cli/internal/output"
)

func TestParseForwarderType_Variants(t *testing.T) {
	cases := []struct {
		raw  string
		want smplkit.ForwarderType
	}{
		{"datadog", smplkit.ForwarderTypeDatadog},
		{"DATADOG", smplkit.ForwarderTypeDatadog},
		{"http", smplkit.ForwarderTypeHTTP},
		{"splunk_hec", smplkit.ForwarderTypeSplunkHEC},
	}
	for _, c := range cases {
		got, err := parseForwarderType(c.raw)
		if err != nil {
			t.Errorf("%q: %v", c.raw, err)
			continue
		}
		if got != c.want {
			t.Errorf("%q: got %q want %q", c.raw, got, c.want)
		}
	}
	if _, err := parseForwarderType("not_a_type"); err == nil {
		t.Errorf("expected error for unknown type")
	}
}

func TestParseHeaders(t *testing.T) {
	hdrs, err := parseHeaders([]string{"DD-API-KEY=secret", "X-Source=cli=test"})
	if err != nil {
		t.Fatalf("parseHeaders: %v", err)
	}
	if len(hdrs) != 2 {
		t.Fatalf("got %d, want 2", len(hdrs))
	}
	if hdrs[0].Name != "DD-API-KEY" || hdrs[0].Value != "secret" {
		t.Errorf("hdrs[0] = %+v", hdrs[0])
	}
	if hdrs[1].Name != "X-Source" || hdrs[1].Value != "cli=test" {
		t.Errorf("hdrs[1] = %+v (values must keep additional = chars)", hdrs[1])
	}
	if _, err := parseHeaders([]string{"nokv"}); err == nil {
		t.Error("expected error for missing =")
	}
}

func TestLoadTransformValue_JSONataIsStringVerbatim(t *testing.T) {
	got, err := loadTransformValue(`account.id`, "JSONATA")
	if err != nil {
		t.Fatalf("loadTransformValue: %v", err)
	}
	if s, ok := got.(string); !ok || s != "account.id" {
		t.Errorf("expected verbatim string, got %#v", got)
	}
}

func TestApplyForwarderFileToModel_OverridesWhenSet(t *testing.T) {
	desc := "edited"
	shape := &forwarderFileShape{
		Name:        "renamed",
		Description: &desc,
		Environments: map[string]forwarderEnvFileShape{
			"production": {Enabled: true},
			"staging":    {Enabled: false},
		},
	}
	fwd := &smplkit.Forwarder{
		ID:   "siem",
		Name: "siem",
	}
	applyForwarderFileToModel(fwd, shape)
	if fwd.Name != "renamed" || fwd.Description == nil || *fwd.Description != "edited" {
		t.Errorf("unexpected: %+v", fwd)
	}
	if !fwd.Environments["production"].Enabled {
		t.Errorf("production should be enabled: %+v", fwd.Environments)
	}
	if fwd.Environments["staging"].Enabled {
		t.Errorf("staging should be disabled: %+v", fwd.Environments)
	}
}

func TestApplyForwarderFileToModel_PerEnvConfigOverride(t *testing.T) {
	tlsVerify := false
	shape := &forwarderFileShape{
		Environments: map[string]forwarderEnvFileShape{
			"production": {
				Enabled: true,
				Configuration: &output.ForwarderHTTPConfigAttr{
					URL:           "https://prod.example.com/ingest",
					Method:        "PUT",
					SuccessStatus: "2xx",
					TLSVerify:     &tlsVerify,
					Headers: []output.ForwarderHeaderAttr{
						{Name: "X-Env", Value: "prod"},
					},
				},
			},
		},
	}
	fwd := &smplkit.Forwarder{ID: "siem", Name: "siem"}
	applyForwarderFileToModel(fwd, shape)

	prod, ok := fwd.Environments["production"]
	if !ok || !prod.Enabled {
		t.Fatalf("production env missing/disabled: %+v", fwd.Environments)
	}
	if prod.Configuration == nil {
		t.Fatal("per-env configuration override should be set")
	}
	if prod.Configuration.URL != "https://prod.example.com/ingest" {
		t.Errorf("url: %q", prod.Configuration.URL)
	}
	if prod.Configuration.Method != "PUT" || prod.Configuration.SuccessStatus != "2xx" {
		t.Errorf("method/status: %+v", prod.Configuration)
	}
	if prod.Configuration.TlsVerify == nil || *prod.Configuration.TlsVerify {
		t.Errorf("tls_verify should be false: %+v", prod.Configuration.TlsVerify)
	}
	if len(prod.Configuration.Headers) != 1 || prod.Configuration.Headers[0].Name != "X-Env" {
		t.Errorf("headers: %+v", prod.Configuration.Headers)
	}
}

func TestApplyEnvToggles_EnableDisablePreservingConfig(t *testing.T) {
	url := "https://override.example.com"
	fwd := &smplkit.Forwarder{
		Environments: map[string]smplkit.ForwarderEnvironment{
			"production": {Enabled: false, Configuration: &smplkit.HttpConfiguration{URL: url}},
		},
	}
	if err := applyEnvToggles(fwd, []string{"production", "staging"}, []string{"dev"}); err != nil {
		t.Fatalf("applyEnvToggles: %v", err)
	}
	prod := fwd.Environments["production"]
	if !prod.Enabled {
		t.Error("production should be enabled")
	}
	if prod.Configuration == nil || prod.Configuration.URL != url {
		t.Errorf("production config override must be preserved: %+v", prod.Configuration)
	}
	if !fwd.Environments["staging"].Enabled {
		t.Error("staging should be enabled (new entry)")
	}
	if fwd.Environments["dev"].Enabled {
		t.Error("dev should be disabled")
	}
}

func TestApplyEnvToggles_RejectsOverlap(t *testing.T) {
	fwd := &smplkit.Forwarder{}
	err := applyEnvToggles(fwd, []string{"production"}, []string{"production"})
	if err == nil {
		t.Fatal("expected error when an env is both enabled and disabled")
	}
	if !strings.Contains(err.Error(), "production") {
		t.Errorf("error should name the offending env, got %v", err)
	}
}

func TestBuildForwarderEnvironments_FileAndFlagsMerge(t *testing.T) {
	shape := &forwarderFileShape{
		Environments: map[string]forwarderEnvFileShape{
			"production": {Enabled: true},
			"staging":    {Enabled: true},
		},
	}
	envs, err := buildForwarderEnvironments(shape, forwarderInputs{
		disableEnvs: []string{"staging"},
		enableEnvs:  []string{"dev"},
	})
	if err != nil {
		t.Fatalf("buildForwarderEnvironments: %v", err)
	}
	if !envs["production"].Enabled {
		t.Error("production (file) should stay enabled")
	}
	if envs["staging"].Enabled {
		t.Error("staging should be disabled by flag")
	}
	if !envs["dev"].Enabled {
		t.Error("dev should be enabled by flag")
	}
}

func TestParseForwarderType_ErrorIncludesValidList(t *testing.T) {
	_, err := parseForwarderType("smtp")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "splunk_hec") {
		t.Errorf("error should list valid types, got %v", err)
	}
}

// `audit forwarder set` exposes per-environment enablement via the
// repeatable --enable-env / --disable-env flags (ADR-055); the old
// global --enabled / --disabled toggles are gone. Verify the new flags
// are wired and parse as a string slice.
func TestForwarderSetCmd_EnvFlagsWired(t *testing.T) {
	cmd := forwarderSetCmd()
	if cmd.Flags().Lookup("enabled") != nil || cmd.Flags().Lookup("disabled") != nil {
		t.Fatal("legacy --enabled/--disabled flags must be removed")
	}
	if err := cmd.ParseFlags([]string{"--enable-env", "production", "--disable-env", "staging"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	enable, err := cmd.Flags().GetStringSlice("enable-env")
	if err != nil {
		t.Fatalf("GetStringSlice enable-env: %v", err)
	}
	disable, err := cmd.Flags().GetStringSlice("disable-env")
	if err != nil {
		t.Fatalf("GetStringSlice disable-env: %v", err)
	}
	if len(enable) != 1 || enable[0] != "production" {
		t.Errorf("enable-env = %v", enable)
	}
	if len(disable) != 1 || disable[0] != "staging" {
		t.Errorf("disable-env = %v", disable)
	}
}
