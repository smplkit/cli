// Package cliconfig collects the per-invocation knobs the CLI reads
// from global flags and resolves the active environment for env-scoped
// commands.
//
// Credential / endpoint resolution stays inside the SDK: every field on
// Config is left at its zero value unless the user explicitly
// supplied the matching flag. That preserves the SDK's documented
// precedence (defaults → ~/.smplkit → SMPLKIT_* → explicit).
//
// Environment resolution is the one thing the CLI re-implements,
// because the CLI deliberately leaves Config.Environment unset — the
// management surface itself never needs it, but env-scoped commands
// (`flag set --enabled --env production`) do. We mirror the SDK's
// precedence (flag → env var → profile → common) using the same INI
// rules so the lookup behaves identically to what the SDK would do for
// the runtime client.
package cliconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// OutputFormat is the rendering mode for read commands.
type OutputFormat string

// Supported output formats.
const (
	OutputTable OutputFormat = "table"
	OutputJSON  OutputFormat = "json"
	OutputYAML  OutputFormat = "yaml"
)

// Globals carries the values bound from the root command's persistent
// flags. Unset fields hold the empty string / false so the SDK's own
// resolution chain runs.
type Globals struct {
	APIKey     string
	Profile    string
	BaseDomain string
	Scheme     string

	Env     string
	Output  OutputFormat
	Quiet   bool
	NoColor bool
}

// ResolveEnvironment returns the environment to scope an env-aware
// command to, following the same precedence the SDK uses when it
// resolves Config.Environment for the runtime client:
//
//  1. the --env flag (g.Env)
//  2. the SMPLKIT_ENVIRONMENT env var
//  3. the resolved ~/.smplkit profile (selected profile overlaid on
//     [common])
//
// An empty return with a nil error means no environment was supplied —
// callers should error out with a clear "set --env, SMPLKIT_ENVIRONMENT,
// or environment in ~/.smplkit".
func ResolveEnvironment(g Globals) (string, error) {
	if v := strings.TrimSpace(g.Env); v != "" {
		return v, nil
	}
	if v := strings.TrimSpace(os.Getenv("SMPLKIT_ENVIRONMENT")); v != "" {
		return v, nil
	}
	return readProfileEnvironment(g.Profile)
}

// readProfileEnvironment reads ~/.smplkit and returns the `environment`
// key from the selected profile (or [common] if the profile doesn't set
// one). Profile selection mirrors the SDK: explicit Profile, then
// SMPLKIT_PROFILE, then "default".
func readProfileEnvironment(explicitProfile string) (string, error) {
	profile := strings.TrimSpace(explicitProfile)
	if profile == "" {
		profile = strings.TrimSpace(os.Getenv("SMPLKIT_PROFILE"))
	}
	if profile == "" {
		profile = "default"
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", nil
	}
	path := filepath.Join(home, ".smplkit")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read %s: %w", path, err)
	}

	sections := parseINI(string(data))

	// `[common]` provides the baseline; the selected profile overlays
	// it. Mirrors applyFileSection / applyFileMap in the SDK.
	envValue := sections["common"]["environment"]
	if profile != "common" {
		if v, ok := sections[profile]["environment"]; ok && v != "" {
			envValue = v
		}
	}
	return strings.TrimSpace(envValue), nil
}

// parseINI is the same minimal INI parser the SDK uses — quoted text,
// comments, sections, key=value. Kept here so we don't depend on any
// non-exported helper inside the SDK.
func parseINI(content string) map[string]map[string]string {
	sections := map[string]map[string]string{}
	current := ""
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			current = trimmed[1 : len(trimmed)-1]
			if _, ok := sections[current]; !ok {
				sections[current] = map[string]string{}
			}
			continue
		}
		if current == "" {
			continue
		}
		eq := strings.Index(trimmed, "=")
		if eq == -1 {
			continue
		}
		key := strings.TrimSpace(trimmed[:eq])
		value := strings.TrimSpace(trimmed[eq+1:])
		sections[current][key] = value
	}
	return sections
}

// EnvRequiredError is returned by helpers when an environment is needed
// but none was resolved. cmd/ converts it into the user-facing message.
type EnvRequiredError struct{}

func (EnvRequiredError) Error() string {
	return "no environment set. Provide one of:\n" +
		"  • --env <name> (or -e <name>)\n" +
		"  • SMPLKIT_ENVIRONMENT=<name>\n" +
		"  • an `environment = <name>` line in ~/.smplkit"
}

// RequireEnv returns the resolved environment or an EnvRequiredError.
func RequireEnv(g Globals) (string, error) {
	env, err := ResolveEnvironment(g)
	if err != nil {
		return "", err
	}
	if env == "" {
		return "", EnvRequiredError{}
	}
	return env, nil
}
