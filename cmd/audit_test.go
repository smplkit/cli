package cmd

import (
	"strings"
	"testing"

	smplkit "github.com/smplkit/go-sdk/v3"
	"github.com/spf13/cobra"

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
	if hdrs["DD-API-KEY"] != "secret" {
		t.Errorf("DD-API-KEY = %q", hdrs["DD-API-KEY"])
	}
	if hdrs["X-Source"] != "cli=test" {
		t.Errorf("X-Source = %q (values must keep additional = chars)", hdrs["X-Source"])
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
	// Per-environment overrides are now flat leaves on the environment (ADR-056),
	// not a nested Configuration object.
	if prod.URL != "https://prod.example.com/ingest" {
		t.Errorf("url: %q", prod.URL)
	}
	if prod.Method != "PUT" || prod.SuccessStatus != "2xx" {
		t.Errorf("method/status: %+v", prod)
	}
	if prod.TlsVerify == nil || *prod.TlsVerify {
		t.Errorf("tls_verify should be false: %+v", prod.TlsVerify)
	}
	if len(prod.Headers) != 1 || prod.Headers["X-Env"] != "prod" {
		t.Errorf("headers: %+v", prod.Headers)
	}
}

func TestApplyEnvToggles_EnableDisablePreservingConfig(t *testing.T) {
	url := "https://override.example.com"
	fwd := &smplkit.Forwarder{
		Environments: map[string]*smplkit.ForwarderEnvironment{
			"production": {Enabled: false, URL: url},
		},
	}
	if err := applyEnvToggles(fwd, []string{"production", "staging"}, []string{"dev"}); err != nil {
		t.Fatalf("applyEnvToggles: %v", err)
	}
	prod := fwd.Environments["production"]
	if !prod.Enabled {
		t.Error("production should be enabled")
	}
	if prod.URL != url {
		t.Errorf("production override leaf must be preserved: %+v", prod)
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

// Both create and set expose --forward-smplkit-events as a bool flag
// defaulting to false.
func TestForwarderCmds_ForwardSmplkitEventsFlagWired(t *testing.T) {
	for _, tc := range []struct {
		name string
		cmd  func() *cobra.Command
	}{
		{"create", forwarderCreateCmd},
		{"set", forwarderSetCmd},
	} {
		cmd := tc.cmd()
		f := cmd.Flags().Lookup("forward-smplkit-events")
		if f == nil {
			t.Fatalf("%s: --forward-smplkit-events flag missing", tc.name)
		}
		if f.DefValue != "false" {
			t.Errorf("%s: default = %q, want false", tc.name, f.DefValue)
		}
		if err := cmd.ParseFlags([]string{"--forward-smplkit-events"}); err != nil {
			t.Fatalf("%s: ParseFlags: %v", tc.name, err)
		}
		v, err := cmd.Flags().GetBool("forward-smplkit-events")
		if err != nil {
			t.Fatalf("%s: GetBool: %v", tc.name, err)
		}
		if !v {
			t.Errorf("%s: flag should be true after --forward-smplkit-events", tc.name)
		}
	}
}

// buildForwarderForCreate sets ForwardSmplkitEvents when the flag is
// supplied, and from the -f file when the flag is omitted.
func TestBuildForwarderForCreate_ForwardSmplkitEvents(t *testing.T) {
	ns := (*smplkit.AuditForwarders)(nil)

	// Flag wins.
	fwd, err := buildForwarderForCreate(ns, "siem", nil, forwarderInputs{
		ftype:                "http",
		url:                  "https://example.com",
		forwardSmplkitEvents: true,
		forwardSmplkitSet:    true,
	})
	if err != nil {
		t.Fatalf("create (flag): %v", err)
	}
	if fwd.ForwardSmplkitEvents == nil || !*fwd.ForwardSmplkitEvents {
		t.Errorf("flag should set ForwardSmplkitEvents=true, got %v", fwd.ForwardSmplkitEvents)
	}

	// File value used when flag omitted.
	fileTrue := true
	shape := &forwarderFileShape{ForwardSmplkitEvents: &fileTrue}
	fwd2, err := buildForwarderForCreate(ns, "siem", shape, forwarderInputs{
		ftype: "http",
		url:   "https://example.com",
	})
	if err != nil {
		t.Fatalf("create (file): %v", err)
	}
	if fwd2.ForwardSmplkitEvents == nil || !*fwd2.ForwardSmplkitEvents {
		t.Errorf("file should set ForwardSmplkitEvents=true, got %v", fwd2.ForwardSmplkitEvents)
	}

	// Omitting both leaves it nil (prior behavior).
	fwd3, err := buildForwarderForCreate(ns, "siem", nil, forwarderInputs{
		ftype: "http",
		url:   "https://example.com",
	})
	if err != nil {
		t.Fatalf("create (default): %v", err)
	}
	if fwd3.ForwardSmplkitEvents != nil {
		t.Errorf("default should leave ForwardSmplkitEvents nil, got %v", *fwd3.ForwardSmplkitEvents)
	}
}

// applyForwarderFileToModel copies forward_smplkit_events from the file.
func TestApplyForwarderFileToModel_ForwardSmplkitEvents(t *testing.T) {
	forward := true
	shape := &forwarderFileShape{ForwardSmplkitEvents: &forward}
	fwd := &smplkit.Forwarder{ID: "siem", Name: "siem"}
	applyForwarderFileToModel(fwd, shape)
	if fwd.ForwardSmplkitEvents == nil || !*fwd.ForwardSmplkitEvents {
		t.Errorf("file should set ForwardSmplkitEvents=true, got %v", fwd.ForwardSmplkitEvents)
	}
}
