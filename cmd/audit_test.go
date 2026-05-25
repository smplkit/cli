package cmd

import (
	"strings"
	"testing"

	smplkit "github.com/smplkit/go-sdk/v3"
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
	enabled := false
	desc := "edited"
	shape := &forwarderFileShape{
		Name:        "renamed",
		Description: &desc,
		Enabled:     &enabled,
	}
	fwd := &smplkit.Forwarder{
		ID:      "siem",
		Name:    "siem",
		Enabled: true,
	}
	applyForwarderFileToModel(fwd, shape)
	if fwd.Name != "renamed" || fwd.Enabled != false || fwd.Description == nil || *fwd.Description != "edited" {
		t.Errorf("unexpected: %+v", fwd)
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
