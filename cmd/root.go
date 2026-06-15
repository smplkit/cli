// Package cmd assembles the smplkit CLI command tree. Each noun lives
// in its own file (flag.go, config.go, …) and registers under root via
// init().
package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	smplkit "github.com/smplkit/go-sdk/v3"
	"github.com/spf13/cobra"

	"github.com/smplkit/cli/internal/cliconfig"
	"github.com/smplkit/cli/internal/clientfactory"
	"github.com/smplkit/cli/internal/output"
)

// globals is populated by the root command's persistent flags and
// consumed by every subcommand via the helpers below. It's a
// package-level singleton because cobra's binding is global anyway —
// hiding it behind a struct keeps the surface small.
var globals cliconfig.Globals

// Execute is the entrypoint called from main. version is the
// GoReleaser-stamped build string.
func Execute(version string) error {
	root := newRootCmd(version)
	return root.Execute()
}

func newRootCmd(version string) *cobra.Command {
	root := &cobra.Command{
		Use:   "smplkit",
		Short: "Command-line interface for the smplkit platform",
		Long: "smplkit is a thin imperative CLI over the smplkit Go SDK's management client.\n\n" +
			"Every command maps onto a Manage().<Ns>().<Verb> call. Credentials and base\n" +
			"endpoint are resolved by the SDK from ~/.smplkit, SMPLKIT_* env vars, or the\n" +
			"global flags below — same precedence the language SDKs use, so an existing\n" +
			"SDK profile works against the CLI with zero extra setup.",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	pflags := root.PersistentFlags()
	pflags.StringVar(&globals.APIKey, "api-key", "", "smplkit API key (overrides SMPLKIT_API_KEY / ~/.smplkit)")
	pflags.StringVar(&globals.Profile, "profile", "", "~/.smplkit profile name (overrides SMPLKIT_PROFILE; default \"default\")")
	pflags.StringVarP(&globals.Env, "env", "e", "", "smplkit environment (overrides SMPLKIT_ENVIRONMENT / ~/.smplkit). Required for env-scoped operations.")
	pflags.StringVarP((*string)(&globals.Output), "output", "o", string(cliconfig.OutputTable),
		"output format: table | json | yaml")
	pflags.BoolVar(&globals.Quiet, "quiet", false, "minimal output (identifiers only)")
	pflags.BoolVar(&globals.NoColor, "no-color", false, "disable ANSI color in output")
	// Hidden: local-testing escape hatches. The customer-facing path is
	// `~/.smplkit` + SMPLKIT_BASE_DOMAIN / SMPLKIT_SCHEME; these flags
	// let CI/dev override quickly without touching the env.
	pflags.StringVar(&globals.BaseDomain, "base-domain", "", "(hidden) override SDK base domain")
	pflags.StringVar(&globals.Scheme, "scheme", "", "(hidden) override SDK scheme (http/https)")
	_ = root.PersistentFlags().MarkHidden("base-domain")
	_ = root.PersistentFlags().MarkHidden("scheme")

	registerFlagCmd(root)
	registerConfigCmd(root)
	registerLoggerCmd(root)
	registerLogGroupCmd(root)
	registerEnvCmd(root)
	registerServiceCmd(root)
	registerAuditCmd(root)
	registerJobCmd(root)

	return root
}

// withClient builds a fresh SmplClient for a single command
// invocation. Cheap to construct — the SDK does no I/O at construction
// — so we don't bother sharing one across the process.
func withClient() (*smplkit.SmplClient, error) {
	return clientfactory.New(globals)
}

// renderer wraps the runtime output config for a given command's stdout.
func renderer(cmd *cobra.Command) output.Renderer {
	return output.NewRenderer(cmd.OutOrStdout(), globals.Output, globals.Quiet)
}

// cliContext returns a Context for SDK calls. Currently identical to
// cmd.Context() — the helper exists so the wiring is consistent and
// any future cancel/deadline behavior threads through one place.
func cliContext(cmd *cobra.Command) context.Context {
	return cmd.Context()
}

// requireEnv resolves the active environment or returns a clear,
// actionable error. Use this in every env-scoped subcommand.
func requireEnv() (string, error) {
	env, err := cliconfig.RequireEnv(globals)
	if err != nil {
		return "", err
	}
	return env, nil
}

// readFileFlag loads -f/--file content if set, returning nil bytes
// when the flag is empty.
func readFileFlag(path string) ([]byte, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return data, nil
}

// confirm prompts y/N on stdin unless yes is true. Returns true if the
// user confirms. Reads a single line from cmd.InOrStdin so tests can
// swap the input.
func confirm(cmd *cobra.Command, yes bool, prompt string) bool {
	if yes {
		return true
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s [y/N]: ", prompt)
	buf := make([]byte, 8)
	n, err := cmd.InOrStdin().Read(buf)
	if err != nil && !errors.Is(err, io.EOF) {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(string(buf[:n])))
	return answer == "y" || answer == "yes"
}
