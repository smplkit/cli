// Package acceptance exercises the built `smplkit` binary against the
// local smplkit platform (ADR-042). Tests are gated by the ACC=1
// environment variable so a stray `go test ./...` doesn't try to hit
// the platform.
//
// CI brings up the platform via docker compose under ci/ — same
// pattern terraform-provider-smplkit uses. Locally, run from a shell
// that already has the platform up (~/projects/.github/platform/start.sh).
//
// All tests share one ephemeral resource namespace per run; each test
// generates a unique id so they can run in parallel without colliding.
package acceptance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	smplkit "github.com/smplkit/go-sdk/v3"
)

const (
	defaultLocalScheme     = "http"
	defaultLocalBaseDomain = "localhost"
)

// binaryPath is the path to the freshly-built `smplkit` binary.
// Resolved once per `go test` run.
var (
	binaryPath     string
	binaryPathOnce sync.Once
	binaryPathErr  error
)

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

// accGate skips a test unless ACC=1 is set in the environment. Mirrors
// terraform-plugin-testing's TF_ACC convention so a `make accept`
// gate exists at the build level.
func accGate(t *testing.T) {
	t.Helper()
	if os.Getenv("ACC") != "1" {
		t.Skip("acceptance tests skipped (set ACC=1 to run)")
	}
	if os.Getenv("SMPLKIT_API_KEY") == "" {
		t.Skip("SMPLKIT_API_KEY not set; skipping acceptance test")
	}
}

func cliBinary(t *testing.T) string {
	t.Helper()
	binaryPathOnce.Do(func() {
		dir, err := os.MkdirTemp("", "smplkit-cli-bin-")
		if err != nil {
			binaryPathErr = err
			return
		}
		bin := filepath.Join(dir, "smplkit")
		cmd := exec.Command("go", "build", "-o", bin, "..")
		cmd.Env = os.Environ()
		out, berr := cmd.CombinedOutput()
		if berr != nil {
			binaryPathErr = fmt.Errorf("build smplkit: %v\n%s", berr, out)
			return
		}
		binaryPath = bin
	})
	if binaryPathErr != nil {
		t.Fatalf("build cli: %v", binaryPathErr)
	}
	return binaryPath
}

// run invokes the CLI with the supplied args and returns its stdout +
// combined stderr (in two buffers). The local-platform routing flags
// are injected for every call so tests don't have to repeat them.
func run(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	bin := cliBinary(t)
	base := []string{
		"--scheme", localScheme(),
		"--base-domain", localBaseDomain(),
	}
	cmd := exec.Command(bin, append(base, args...)...)
	cmd.Env = os.Environ()
	var sout, serr bytes.Buffer
	cmd.Stdout = &sout
	cmd.Stderr = &serr
	err = cmd.Run()
	return sout.String(), serr.String(), err
}

// mustRun fails the test on non-zero exit.
func mustRun(t *testing.T, args ...string) (stdout string) {
	t.Helper()
	out, serr, err := run(t, args...)
	if err != nil {
		t.Fatalf("smplkit %s failed: %v\nstdout: %s\nstderr: %s",
			strings.Join(args, " "), err, out, serr)
	}
	return out
}

func localScheme() string {
	if v := os.Getenv("SMPLKIT_SCHEME"); v != "" {
		return v
	}
	return defaultLocalScheme
}

func localBaseDomain() string {
	if v := os.Getenv("SMPLKIT_BASE_DOMAIN"); v != "" {
		return v
	}
	return defaultLocalBaseDomain
}

// managementClient builds an SDK client with the same routing the CLI
// uses, for the side-channel verification tests do at the end.
func managementClient(t *testing.T) *smplkit.SmplClient {
	t.Helper()
	cfg := smplkit.Config{
		APIKey:     os.Getenv("SMPLKIT_API_KEY"),
		Scheme:     localScheme(),
		BaseDomain: localBaseDomain(),
	}
	client, err := smplkit.NewClient(cfg)
	if err != nil {
		t.Fatalf("management client: %v", err)
	}
	return client
}

// uniqueID returns a per-test unique id rooted in the test name so
// failures are debuggable.
func uniqueID(t *testing.T, prefix string) string {
	t.Helper()
	return fmt.Sprintf("acc-%s-%d", prefix, time.Now().UnixNano())
}

// ─── Service CRUD ────────────────────────────────────────────────────

func TestAccService_CRUD(t *testing.T) {
	accGate(t)
	id := uniqueID(t, "svc")

	t.Cleanup(func() { _ = deleteSilent(t, "service", id) })

	mustRun(t, "service", "create", id, "--name", "Acc Service")
	out := mustRun(t, "service", "get", id, "-o", "json")
	var svc map[string]interface{}
	if err := json.Unmarshal([]byte(out), &svc); err != nil {
		t.Fatalf("parse json: %v\n%s", err, out)
	}
	if svc["name"] != "Acc Service" {
		t.Errorf("name not persisted: %v", svc)
	}

	mustRun(t, "service", "set", id, "--name", "Acc Service v2")
	out = mustRun(t, "service", "get", id, "-o", "json")
	_ = json.Unmarshal([]byte(out), &svc)
	if svc["name"] != "Acc Service v2" {
		t.Errorf("rename not persisted: %v", svc)
	}

	listOut := mustRun(t, "service", "list", "--quiet", "--all")
	if !strings.Contains(listOut, id) {
		t.Errorf("listed services missing %q:\n%s", id, listOut)
	}

	mustRun(t, "service", "delete", id, "--yes")
}

// ─── Environment CRUD ────────────────────────────────────────────────

func TestAccEnvironment_CRUD(t *testing.T) {
	accGate(t)
	id := uniqueID(t, "env")
	t.Cleanup(func() { _ = deleteSilent(t, "env", id) })

	// Free a managed slot if needed (ADR-051): a fresh free-tier
	// account is born at 2/2. The terraform-provider acceptance suite
	// runs the same prep step; mirror it here for parity.
	freeManagedEnvironmentSlot(t)

	mustRun(t, "env", "create", id, "--name", "Acc Env", "--color", "#10b981")
	out := mustRun(t, "env", "get", id, "-o", "json")
	var env map[string]interface{}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("parse: %v\n%s", err, out)
	}
	if env["color"] != "#10b981" {
		t.Errorf("color not persisted: %v", env)
	}
	if env["classification"] != "STANDARD" {
		t.Errorf("classification should be STANDARD: %v", env)
	}

	mustRun(t, "env", "set", id, "--color", "#ef4444")
	out = mustRun(t, "env", "get", id, "-o", "json")
	_ = json.Unmarshal([]byte(out), &env)
	if env["color"] != "#ef4444" {
		t.Errorf("color update not persisted: %v", env)
	}

	mustRun(t, "env", "delete", id, "--yes")
}

// freeManagedEnvironmentSlot deletes the seeded `development`
// environment so a managed slot opens up for the test. Idempotent.
func freeManagedEnvironmentSlot(t *testing.T) {
	t.Helper()
	client := managementClient(t)
	if err := client.Platform().Environments().Delete(context.Background(), "development"); err != nil {
		var nf *smplkit.NotFoundError
		if !errors.As(err, &nf) {
			t.Logf("dev env prep: %v (continuing)", err)
		}
	}
}

// ─── Flag CRUD ───────────────────────────────────────────────────────

func TestAccFlag_CRUD(t *testing.T) {
	accGate(t)
	id := uniqueID(t, "flag")
	t.Cleanup(func() { _ = deleteSilent(t, "flag", id) })

	mustRun(t, "flag", "create", id,
		"--type", "bool",
		"--default", "false",
		"--name", "Acc Flag",
		"--description", "acceptance flag")
	out := mustRun(t, "flag", "get", id, "-o", "json")
	var f map[string]interface{}
	if err := json.Unmarshal([]byte(out), &f); err != nil {
		t.Fatalf("parse: %v\n%s", err, out)
	}
	if f["type"] != "BOOLEAN" {
		t.Errorf("type: %v", f)
	}

	mustRun(t, "flag", "set", id, "--description", "updated")
	out = mustRun(t, "flag", "get", id, "-o", "json")
	_ = json.Unmarshal([]byte(out), &f)
	if f["description"] != "updated" {
		t.Errorf("description not persisted: %v", f)
	}

	listOut := mustRun(t, "flag", "list", "--quiet", "--all")
	if !strings.Contains(listOut, id) {
		t.Errorf("listed flags missing %q", id)
	}

	mustRun(t, "flag", "delete", id, "--yes")
}

// ─── Config CRUD ─────────────────────────────────────────────────────

func TestAccConfig_CRUD(t *testing.T) {
	accGate(t)
	id := uniqueID(t, "cfg")
	t.Cleanup(func() { _ = deleteSilent(t, "config", id) })

	mustRun(t, "config", "create", id, "--name", "Acc Config")
	mustRun(t, "config", "set", id, "--item", "k1=hello", "--item-type", "string")
	mustRun(t, "config", "set", id, "--item", "k2=42", "--item-type", "number")

	out := mustRun(t, "config", "get", id, "-o", "json")
	var c map[string]interface{}
	if err := json.Unmarshal([]byte(out), &c); err != nil {
		t.Fatalf("parse: %v\n%s", err, out)
	}
	items, _ := c["items"].(map[string]interface{})
	if items["k1"] != "hello" {
		t.Errorf("k1: %v", items)
	}
	if items["k2"] != 42.0 {
		t.Errorf("k2: %v", items)
	}

	mustRun(t, "config", "set", id, "--remove-item", "k1")
	out = mustRun(t, "config", "get", id, "-o", "json")
	_ = json.Unmarshal([]byte(out), &c)
	items, _ = c["items"].(map[string]interface{})
	if _, ok := items["k1"]; ok {
		t.Errorf("k1 should be removed: %v", items)
	}

	mustRun(t, "config", "delete", id, "--yes")
}

// ─── Log group CRUD ──────────────────────────────────────────────────

func TestAccLogGroup_CRUD(t *testing.T) {
	accGate(t)
	id := uniqueID(t, "lg")
	t.Cleanup(func() { _ = deleteSilent(t, "log-group", id) })

	mustRun(t, "log-group", "create", id, "--name", "Acc LG", "--level", "INFO")
	out := mustRun(t, "log-group", "get", id, "-o", "json")
	var g map[string]interface{}
	if err := json.Unmarshal([]byte(out), &g); err != nil {
		t.Fatalf("parse: %v\n%s", err, out)
	}
	if g["level"] != "INFO" {
		t.Errorf("level: %v", g)
	}
	mustRun(t, "log-group", "set", id, "--level", "DEBUG")
	out = mustRun(t, "log-group", "get", id, "-o", "json")
	_ = json.Unmarshal([]byte(out), &g)
	if g["level"] != "DEBUG" {
		t.Errorf("level update: %v", g)
	}
	mustRun(t, "log-group", "delete", id, "--yes")
}

// ─── Audit forwarder CRUD ────────────────────────────────────────────

func TestAccAuditForwarder_CRUD(t *testing.T) {
	accGate(t)
	id := uniqueID(t, "fwd")
	t.Cleanup(func() { _ = deleteSilent(t, "audit", "forwarder", id) })

	// Enablement is per-environment (ADR-055). `production` is a seeded,
	// managed environment present on every fresh account and cannot be
	// deleted, so it's a stable target for --enable-env / --disable-env.
	const env = "production"

	mustRun(t, "audit", "forwarder", "create", id,
		"--type", "http",
		"--name", "Acc Forwarder",
		"--url", "https://example.com/ingest",
		"--header", "X-Source=cli",
		"--enable-env", env,
		"--forward-smplkit-events")

	out := mustRun(t, "audit", "forwarder", "get", id, "-o", "json")
	var f map[string]interface{}
	if err := json.Unmarshal([]byte(out), &f); err != nil {
		t.Fatalf("parse: %v\n%s", err, out)
	}
	if !forwarderEnvEnabled(f, env) {
		t.Errorf("expected %s enabled true: %v", env, f)
	}
	if v, _ := f["forward_smplkit_events"].(bool); !v {
		t.Errorf("expected forward_smplkit_events true after create: %v", f)
	}

	// Toggle the opt-in back off via set, and disable the env.
	mustRun(t, "audit", "forwarder", "set", id,
		"--disable-env", env,
		"--forward-smplkit-events=false")
	out = mustRun(t, "audit", "forwarder", "get", id, "-o", "json")
	_ = json.Unmarshal([]byte(out), &f)
	if forwarderEnvEnabled(f, env) {
		t.Errorf("expected %s enabled false after --disable-env: %v", env, f)
	}
	// forward_smplkit_events=false is omitempty in JSON output (nil/false),
	// so its absence (or explicit false) both mean "off".
	if v, _ := f["forward_smplkit_events"].(bool); v {
		t.Errorf("expected forward_smplkit_events false after set: %v", f)
	}

	mustRun(t, "audit", "forwarder", "delete", id, "--yes")
}

// forwarderEnvEnabled reports whether the forwarder JSON has the named
// environment enabled in its `environments` map.
func forwarderEnvEnabled(f map[string]interface{}, env string) bool {
	envs, ok := f["environments"].(map[string]interface{})
	if !ok {
		return false
	}
	entry, ok := envs[env].(map[string]interface{})
	if !ok {
		return false
	}
	enabled, _ := entry["enabled"].(bool)
	return enabled
}

// ─── Logger ──────────────────────────────────────────────────────────
//
// Loggers are created by the runtime SDK on first observation. The
// acceptance suite can't synthesize one without spinning up an SDK
// client *and* registering a service, which the management-only CLI
// deliberately can't do. The unit-test coverage and the SDK's own
// acceptance suite cover logger CRUD; this gate is a no-op rather
// than wedge the suite waiting on infrastructure outside its layer.

// ─── Helpers ─────────────────────────────────────────────────────────

// deleteSilent runs a `<noun…> delete <id> --yes` cleanup, ignoring
// errors (cleanup races with the test's own delete are expected).
func deleteSilent(t *testing.T, parts ...string) error {
	t.Helper()
	args := make([]string, 0, len(parts)+2)
	args = append(args, parts[:len(parts)-1]...)
	args = append(args, "delete", parts[len(parts)-1], "--yes")
	_, _, err := run(t, args...)
	return err
}
