package cliconfig

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveEnvironment_FlagWins(t *testing.T) {
	t.Setenv("SMPLKIT_ENVIRONMENT", "from-env")
	t.Setenv("HOME", t.TempDir())

	env, err := ResolveEnvironment(Globals{Env: "from-flag"})
	if err != nil {
		t.Fatalf("ResolveEnvironment: %v", err)
	}
	if env != "from-flag" {
		t.Fatalf("expected from-flag, got %q", env)
	}
}

func TestResolveEnvironment_EnvVarOverProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SMPLKIT_ENVIRONMENT", "from-env")
	writeINI(t, home, "[default]\nenvironment = from-profile\n")

	env, err := ResolveEnvironment(Globals{})
	if err != nil {
		t.Fatalf("ResolveEnvironment: %v", err)
	}
	if env != "from-env" {
		t.Fatalf("expected from-env, got %q", env)
	}
}

func TestResolveEnvironment_ProfileOverCommon(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SMPLKIT_ENVIRONMENT", "")
	writeINI(t, home,
		"[common]\nenvironment = base\n\n[work]\nenvironment = work-env\n")

	env, err := ResolveEnvironment(Globals{Profile: "work"})
	if err != nil {
		t.Fatalf("ResolveEnvironment: %v", err)
	}
	if env != "work-env" {
		t.Fatalf("expected work-env, got %q", env)
	}
}

func TestResolveEnvironment_FallsBackToCommon(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SMPLKIT_ENVIRONMENT", "")
	writeINI(t, home, "[common]\nenvironment = common-env\n[default]\napi_key = abc\n")

	env, err := ResolveEnvironment(Globals{})
	if err != nil {
		t.Fatalf("ResolveEnvironment: %v", err)
	}
	if env != "common-env" {
		t.Fatalf("expected common-env, got %q", env)
	}
}

func TestResolveEnvironment_Empty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SMPLKIT_ENVIRONMENT", "")
	// no ~/.smplkit at all

	env, err := ResolveEnvironment(Globals{})
	if err != nil {
		t.Fatalf("ResolveEnvironment: %v", err)
	}
	if env != "" {
		t.Fatalf("expected empty, got %q", env)
	}
}

func TestRequireEnv_Errors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SMPLKIT_ENVIRONMENT", "")

	_, err := RequireEnv(Globals{})
	var want EnvRequiredError
	if !errors.As(err, &want) {
		t.Fatalf("expected EnvRequiredError, got %T (%v)", err, err)
	}
}

func TestParseINI_Basics(t *testing.T) {
	content := `
[common]
api_key = ak
environment = base

# comment
; another comment
[work]
api_key = bk
environment = work-env
`
	sections := parseINI(content)
	if got := sections["common"]["api_key"]; got != "ak" {
		t.Errorf("common.api_key = %q", got)
	}
	if got := sections["work"]["environment"]; got != "work-env" {
		t.Errorf("work.environment = %q", got)
	}
	if _, ok := sections["common"]["comment"]; ok {
		t.Errorf("comment line should not have produced a key")
	}
}

func writeINI(t *testing.T, home, body string) {
	t.Helper()
	path := filepath.Join(home, ".smplkit")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write ini: %v", err)
	}
}
