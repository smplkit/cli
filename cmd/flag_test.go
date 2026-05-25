package cmd

import (
	"reflect"
	"testing"

	smplkit "github.com/smplkit/go-sdk/v3"
)

func TestApplyRules_ReplacesEnvScopedRules(t *testing.T) {
	f := &smplkit.Flag{
		ID: "x",
		Environments: map[string]interface{}{
			"production": map[string]interface{}{
				"enabled": true,
				"default": false,
				"rules":   []interface{}{map[string]interface{}{"description": "old"}},
			},
		},
	}
	raw := `[{"description":"new","logic":{"==":[1,1]},"value":true}]`
	if err := applyRules(f, "production", raw); err != nil {
		t.Fatalf("applyRules: %v", err)
	}
	env, _ := f.Environments["production"].(map[string]interface{})
	if env["enabled"] != true {
		t.Errorf("enabled flag was clobbered: %#v", env["enabled"])
	}
	if env["default"] != false {
		t.Errorf("default was clobbered: %#v", env["default"])
	}
	rules, ok := env["rules"].([]interface{})
	if !ok || len(rules) != 1 {
		t.Fatalf("rules wrong: %#v", env["rules"])
	}
	got, ok := rules[0].(map[string]interface{})
	if !ok || got["description"] != "new" {
		t.Errorf("rule shape: %#v", rules[0])
	}
}

func TestApplyRules_RequiresEnv(t *testing.T) {
	f := &smplkit.Flag{ID: "x"}
	if err := applyRules(f, "", `[]`); err == nil {
		t.Fatal("expected error without --env")
	}
}

func TestApplyRules_RejectsInvalidJSON(t *testing.T) {
	f := &smplkit.Flag{ID: "x"}
	if err := applyRules(f, "production", `not json`); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestApplyFlagFileToModel_AppliesAllSetFields(t *testing.T) {
	desc := "described"
	shape := flagFileShape{
		Name:        "Display",
		Description: &desc,
		Default:     "abc",
		Environments: map[string]interface{}{
			"staging": map[string]interface{}{"enabled": true},
		},
	}
	f := &smplkit.Flag{ID: "x", Type: "STRING"}
	applyFlagFileToModel(f, shape)
	if f.Name != "Display" || f.Default != "abc" || f.Description == nil || *f.Description != "described" {
		t.Errorf("unexpected model: %+v", f)
	}
	if !reflect.DeepEqual(f.Environments, shape.Environments) {
		t.Errorf("environments mismatch: %#v", f.Environments)
	}
}
