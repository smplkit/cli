package cmd

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	smplkit "github.com/smplkit/go-sdk/v3"

	"github.com/smplkit/cli/internal/output"
)

// TestJobListCmd_InvalidKind verifies --kind rejects a value outside the
// recurring/manual/one_off set before any client call, so the error is a clean
// validation failure rather than a wasted round-trip.
func TestJobListCmd_InvalidKind(t *testing.T) {
	cmd := jobListCmd()
	cmd.SetArgs([]string{"--kind", "bogus"})
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	err := cmd.Execute()
	const want = "invalid --kind"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("--kind bogus: got err %v, want containing %q", err, want)
	}
}

// TestParseJobKind covers the --kind value mapping: each valid spelling maps
// to its SDK enum, and anything else is rejected.
func TestParseJobKind(t *testing.T) {
	cases := map[string]smplkit.JobKind{
		"recurring": smplkit.JobKindRecurring,
		"manual":    smplkit.JobKindManual,
		"one_off":   smplkit.JobKindOneOff,
	}
	for in, want := range cases {
		got, err := parseJobKind(in)
		if err != nil {
			t.Errorf("parseJobKind(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseJobKind(%q) = %q want %q", in, got, want)
		}
	}
	if _, err := parseJobKind("bogus"); err == nil {
		t.Error("expected error for an invalid --kind value")
	}
}

// TestJobListCmd_NoEnabledFilter verifies the removed --enabled/--disabled
// list filters are gone (next-fire and enablement are per-environment now;
// the filter[enabled] list filter no longer exists in the API).
func TestJobListCmd_NoEnabledFilter(t *testing.T) {
	cmd := jobListCmd()
	if cmd.Flags().Lookup("enabled") != nil || cmd.Flags().Lookup("disabled") != nil {
		t.Fatal("legacy --enabled/--disabled job-list flags must be removed")
	}
}

func TestParseJobHeaders(t *testing.T) {
	hdrs, err := parseJobHeaders([]string{
		"X-Source: cli",
		"Authorization: Bearer abc:def",
	})
	if err != nil {
		t.Fatalf("parseJobHeaders: %v", err)
	}
	if len(hdrs) != 2 {
		t.Fatalf("want 2 headers, got %d: %#v", len(hdrs), hdrs)
	}
	if hdrs["X-Source"] != "cli" {
		t.Errorf("X-Source: %q", hdrs["X-Source"])
	}
	// Split on the FIRST colon: the value keeps its own colons.
	if hdrs["Authorization"] != "Bearer abc:def" {
		t.Errorf("Authorization: %q", hdrs["Authorization"])
	}
}

func TestParseJobHeaders_Errors(t *testing.T) {
	if _, err := parseJobHeaders([]string{"no-colon"}); err == nil {
		t.Error("expected error for header without a colon")
	}
	if _, err := parseJobHeaders([]string{": value"}); err == nil {
		t.Error("expected error for header with an empty name")
	}
}

func TestParseJobMethod(t *testing.T) {
	cases := map[string]smplkit.JobHttpMethod{
		"post":   smplkit.JobHttpMethodPost,
		"GET":    smplkit.JobHttpMethodGet,
		" put ":  smplkit.JobHttpMethodPut,
		"Patch":  smplkit.JobHttpMethodPatch,
		"DELETE": smplkit.JobHttpMethodDelete,
	}
	for in, want := range cases {
		got, err := parseJobMethod(in)
		if err != nil {
			t.Errorf("parseJobMethod(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseJobMethod(%q) = %q want %q", in, got, want)
		}
	}
	if _, err := parseJobMethod("FETCH"); err == nil {
		t.Error("expected error for invalid method")
	}
}

func TestBuildJobHTTPConfig_DefaultsMethodToPost(t *testing.T) {
	cfg, err := buildJobHTTPConfig(nil, jobInputs{url: "https://x.test", urlSet: true})
	if err != nil {
		t.Fatalf("buildJobHTTPConfig: %v", err)
	}
	if cfg.Method != smplkit.JobHttpMethodPost {
		t.Errorf("method should default to POST, got %q", cfg.Method)
	}
	if cfg.URL != "https://x.test" {
		t.Errorf("url: %q", cfg.URL)
	}
}

func TestBuildJobHTTPConfig_RequiresURL(t *testing.T) {
	if _, err := buildJobHTTPConfig(nil, jobInputs{}); err == nil {
		t.Error("expected error when no --url and no file URL")
	}
}

func TestBuildJobHTTPConfig_ScalarOverridesFile(t *testing.T) {
	shape := &jobFileShape{
		Configuration: &output.JobHTTPConfigAttr{
			URL:    "https://file.test",
			Method: "GET",
			Headers: []output.JobHeaderAttr{
				{Name: "X-File", Value: "1"},
			},
		},
	}
	in := jobInputs{
		url:       "https://flag.test",
		urlSet:    true,
		headers:   []string{"X-Flag: 2"},
		headerSet: true,
	}
	cfg, err := buildJobHTTPConfig(shape, in)
	if err != nil {
		t.Fatalf("buildJobHTTPConfig: %v", err)
	}
	if cfg.URL != "https://flag.test" {
		t.Errorf("scalar --url should win: %q", cfg.URL)
	}
	// --method not set, so the file's GET is preserved (not clobbered by POST).
	if cfg.Method != smplkit.JobHttpMethodGet {
		t.Errorf("file method should be preserved when --method unset: %q", cfg.Method)
	}
	// Scalar headers replace the file's headers entirely.
	if len(cfg.Headers) != 1 || cfg.Headers["X-Flag"] != "2" {
		t.Errorf("scalar headers should replace file headers: %#v", cfg.Headers)
	}
}

func TestBuildJobForCreate_NameDefaultsToID(t *testing.T) {
	ns := testJobsClient(t)
	job, err := buildJobForCreate(ns, "my-job", nil, jobInputs{
		schedule:    "0 0 * * *",
		scheduleSet: true,
		url:         "https://x.test",
		urlSet:      true,
	})
	if err != nil {
		t.Fatalf("buildJobForCreate: %v", err)
	}
	if job.Name != "my-job" {
		t.Errorf("name should default to id, got %q", job.Name)
	}
	if job.Schedule != "0 0 * * *" {
		t.Errorf("schedule: %q", job.Schedule)
	}
	if job.Configuration.Method != smplkit.JobHttpMethodPost {
		t.Errorf("method default POST: %q", job.Configuration.Method)
	}
}

// TestBuildJobForCreate_ManualWhenNoSchedule verifies an empty schedule now
// produces a manual job (no schedule, never auto-fires) rather than an error —
// the old "missing --schedule" guard is gone. Kind is derived server-side, so
// it stays nil on the unsaved job; the empty Schedule is what marks it manual.
func TestBuildJobForCreate_ManualWhenNoSchedule(t *testing.T) {
	ns := testJobsClient(t)
	job, err := buildJobForCreate(ns, "j", nil, jobInputs{url: "https://x.test", urlSet: true})
	if err != nil {
		t.Fatalf("buildJobForCreate: %v", err)
	}
	if job.Schedule != "" {
		t.Errorf("a manual job should have an empty schedule, got %q", job.Schedule)
	}
}

// TestNewJobForSchedule_Classifier pins the schedule → constructor mapping:
// empty → manual (no schedule), "now" → one-off scheduled near now, an
// RFC-3339 datetime → one-off at that instant, and a cron string → recurring
// (schedule preserved verbatim).
func TestNewJobForSchedule_Classifier(t *testing.T) {
	ns := testJobsClient(t)
	cfg := smplkit.HttpConfig{URL: "https://x.test"}

	manual := newJobForSchedule(ns, "j", "Job", "", cfg)
	if manual.Schedule != "" {
		t.Errorf("manual job schedule should be empty, got %q", manual.Schedule)
	}

	now := newJobForSchedule(ns, "j", "Job", "now", cfg)
	if _, err := time.Parse(time.RFC3339, now.Schedule); err != nil {
		t.Errorf(`"now" should resolve to an RFC-3339 instant, got %q (%v)`, now.Schedule, err)
	}

	const when = "2099-01-01T00:00:00Z"
	oneOff := newJobForSchedule(ns, "j", "Job", when, cfg)
	if oneOff.Schedule != when {
		t.Errorf("datetime schedule should round-trip, got %q want %q", oneOff.Schedule, when)
	}

	const cron = "0 0 * * *"
	recurring := newJobForSchedule(ns, "j", "Job", cron, cfg)
	if recurring.Schedule != cron {
		t.Errorf("cron schedule should be preserved verbatim, got %q want %q", recurring.Schedule, cron)
	}
}

func TestBuildJobForCreate_AppliesFileOptions(t *testing.T) {
	ns := testJobsClient(t)
	desc := "control-tower housekeeping"
	// A non-default ConcurrencyPolicy so the WithJobConcurrencyPolicy branch
	// is genuinely exercised (the SDK's New() already defaults to "ALLOW",
	// so asserting "ALLOW" would be vacuous). "FORBID" is a test-only
	// sentinel; the server accepts only "ALLOW" today.
	shape := &jobFileShape{
		Name:              "From File",
		Schedule:          "0 4 * * *", // schedule falls back to the file
		Description:       &desc,
		ConcurrencyPolicy: "FORBID",
		Environments: map[string]jobEnvFileShape{
			"production": {Enabled: true},
			"staging":    {Enabled: false},
		},
		Configuration: &output.JobHTTPConfigAttr{URL: "https://file.test"},
	}
	job, err := buildJobForCreate(ns, "j", shape, jobInputs{})
	if err != nil {
		t.Fatalf("buildJobForCreate: %v", err)
	}
	if job.Name != "From File" {
		t.Errorf("name from file: %q", job.Name)
	}
	if job.Schedule != "0 4 * * *" {
		t.Errorf("schedule should fall back to the file: %q", job.Schedule)
	}
	if !job.Environments["production"].Enabled {
		t.Error("production should be enabled from the file's environments map")
	}
	if job.Environments["staging"].Enabled {
		t.Error("staging should be disabled from the file's environments map")
	}
	if job.Description == nil || *job.Description != desc {
		t.Errorf("description from file: %v", job.Description)
	}
	if job.ConcurrencyPolicy != "FORBID" {
		t.Errorf("concurrency policy from file should be applied: %q", job.ConcurrencyPolicy)
	}
	if job.Configuration.URL != "https://file.test" {
		t.Errorf("url from file: %q", job.Configuration.URL)
	}
}

func TestApplyJobInputsToModel_PreservesUnspecified(t *testing.T) {
	body := "ping"
	desc := "nightly"
	tls := false
	// Seed every field that has NO scalar flag — these live or die entirely
	// by applyJobInputsToModel/applyJobFileToModel leaving them alone, so the
	// apply Long text's "preserve every unspecified field" promise rests on
	// them surviving a schedule-only apply.
	existing := &smplkit.Job{
		ID:                "j",
		Name:              "Original",
		Description:       &desc,
		Schedule:          "0 0 * * *",
		Environments:      map[string]*smplkit.JobEnvironment{"production": {Enabled: true}},
		ConcurrencyPolicy: "ALLOW",
		Configuration: smplkit.HttpConfig{
			URL:           "https://orig.test",
			Method:        smplkit.JobHttpMethodPost,
			Body:          &body,
			SuccessStatus: "204",
			Timeout:       45,
			TlsVerify:     &tls,
			Headers:       map[string]string{"Authorization": "Bearer secret"},
		},
	}
	// Only change the schedule. Everything else must survive — this is the
	// drift-reconciling apply path's core guarantee.
	in := jobInputs{schedule: "*/5 * * * *", scheduleSet: true}
	if err := applyJobInputsToModel(existing, nil, in); err != nil {
		t.Fatalf("applyJobInputsToModel: %v", err)
	}
	if existing.Schedule != "*/5 * * * *" {
		t.Errorf("schedule not updated: %q", existing.Schedule)
	}
	if existing.Name != "Original" {
		t.Errorf("name should be preserved: %q", existing.Name)
	}
	if existing.Description == nil || *existing.Description != "nightly" {
		t.Errorf("description should be preserved: %v", existing.Description)
	}
	if !existing.Environments["production"].Enabled {
		t.Error("environments enablement should be preserved when no env flags are passed")
	}
	if existing.ConcurrencyPolicy != "ALLOW" {
		t.Errorf("concurrency policy should be preserved: %q", existing.ConcurrencyPolicy)
	}
	if existing.Configuration.URL != "https://orig.test" {
		t.Errorf("url should be preserved: %q", existing.Configuration.URL)
	}
	if existing.Configuration.SuccessStatus != "204" {
		t.Errorf("success status should be preserved: %q", existing.Configuration.SuccessStatus)
	}
	if existing.Configuration.Timeout != 45 {
		t.Errorf("timeout should be preserved: %d", existing.Configuration.Timeout)
	}
	if existing.Configuration.TlsVerify == nil || *existing.Configuration.TlsVerify {
		t.Errorf("tls_verify=false should be preserved: %v", existing.Configuration.TlsVerify)
	}
	if len(existing.Configuration.Headers) != 1 ||
		existing.Configuration.Headers["Authorization"] != "Bearer secret" {
		t.Errorf("header value should be preserved: %#v", existing.Configuration.Headers)
	}
	if existing.Configuration.Body == nil || *existing.Configuration.Body != "ping" {
		t.Errorf("body should be preserved: %v", existing.Configuration.Body)
	}
}

func TestApplyJobInputsToModel_ScalarOverridesFile(t *testing.T) {
	existing := &smplkit.Job{
		ID:            "j",
		Name:          "Original",
		Configuration: smplkit.HttpConfig{URL: "https://orig.test"},
	}
	shape := &jobFileShape{
		Name:          "From File",
		Configuration: &output.JobHTTPConfigAttr{URL: "https://file.test"},
	}
	in := jobInputs{name: "From Flag", nameSet: true}
	if err := applyJobInputsToModel(existing, shape, in); err != nil {
		t.Fatalf("applyJobInputsToModel: %v", err)
	}
	// File applied first, then scalar flag overrides name.
	if existing.Name != "From Flag" {
		t.Errorf("scalar name should win: %q", existing.Name)
	}
	// URL came from the file (no scalar override).
	if existing.Configuration.URL != "https://file.test" {
		t.Errorf("file url should be applied: %q", existing.Configuration.URL)
	}
}

func TestApplyJobInputsToModel_UpdatesHeaderValue(t *testing.T) {
	existing := &smplkit.Job{
		ID: "j",
		Configuration: smplkit.HttpConfig{
			URL:     "https://orig.test",
			Headers: map[string]string{"Authorization": "Bearer OLD"},
		},
	}
	in := jobInputs{headers: []string{"Authorization: Bearer NEW"}, headerSet: true}
	if err := applyJobInputsToModel(existing, nil, in); err != nil {
		t.Fatalf("applyJobInputsToModel: %v", err)
	}
	if len(existing.Configuration.Headers) != 1 ||
		existing.Configuration.Headers["Authorization"] != "Bearer NEW" {
		t.Errorf("rotated header value should reconcile: %#v", existing.Configuration.Headers)
	}
}

func TestApplyJobInputsToModel_HeaderReplaceIsFullSet(t *testing.T) {
	// --header replaces the COMPLETE header set (documented in the apply
	// help, consistent with the audit forwarder noun). Rotating one header
	// of several therefore drops the others — callers must re-supply all.
	// This test pins that contract so the behavior can't change silently.
	existing := &smplkit.Job{
		ID: "j",
		Configuration: smplkit.HttpConfig{
			URL: "https://orig.test",
			Headers: map[string]string{
				"Authorization": "Bearer OLD",
				"Content-Type":  "application/json",
			},
		},
	}
	in := jobInputs{headers: []string{"Authorization: Bearer NEW"}, headerSet: true}
	if err := applyJobInputsToModel(existing, nil, in); err != nil {
		t.Fatalf("applyJobInputsToModel: %v", err)
	}
	if len(existing.Configuration.Headers) != 1 ||
		existing.Configuration.Headers["Authorization"] != "Bearer NEW" {
		t.Errorf("--header should replace the full set: %#v", existing.Configuration.Headers)
	}
}

func TestApplyJobFileToModel_AppliesSetFieldsOnly(t *testing.T) {
	tls := false
	existing := &smplkit.Job{
		ID:       "j",
		Name:     "Original",
		Schedule: "0 0 * * *",
		Configuration: smplkit.HttpConfig{
			URL:           "https://orig.test",
			Method:        smplkit.JobHttpMethodPost,
			SuccessStatus: "2xx",
		},
	}
	shape := &jobFileShape{
		Schedule: "*/10 * * * *",
		Configuration: &output.JobHTTPConfigAttr{
			Method:    "GET",
			TLSVerify: &tls,
		},
	}
	applyJobFileToModel(existing, shape)
	if existing.Schedule != "*/10 * * * *" {
		t.Errorf("schedule from file: %q", existing.Schedule)
	}
	if existing.Configuration.Method != smplkit.JobHttpMethodGet {
		t.Errorf("method from file: %q", existing.Configuration.Method)
	}
	// URL and SuccessStatus untouched (not present in the file).
	if existing.Configuration.URL != "https://orig.test" {
		t.Errorf("url should be preserved: %q", existing.Configuration.URL)
	}
	if existing.Configuration.SuccessStatus != "2xx" {
		t.Errorf("success status should be preserved: %q", existing.Configuration.SuccessStatus)
	}
	if existing.Configuration.TlsVerify == nil || *existing.Configuration.TlsVerify {
		t.Errorf("tls_verify=false from file should be applied: %v", existing.Configuration.TlsVerify)
	}
}

func TestBuildJobHTTPConfig_InvalidMethod(t *testing.T) {
	in := jobInputs{url: "https://x.test", urlSet: true, method: "FETCH", methodSet: true}
	if _, err := buildJobHTTPConfig(nil, in); err == nil {
		t.Error("expected error for invalid --method")
	}
}

func TestApplyJobInputsToModel_MethodBodyAndURL(t *testing.T) {
	existing := &smplkit.Job{
		ID:            "j",
		Configuration: smplkit.HttpConfig{URL: "https://orig.test", Method: smplkit.JobHttpMethodPost},
	}
	in := jobInputs{
		url:       "https://new.test",
		urlSet:    true,
		method:    "put",
		methodSet: true,
		body:      "payload",
		bodySet:   true,
	}
	if err := applyJobInputsToModel(existing, nil, in); err != nil {
		t.Fatalf("applyJobInputsToModel: %v", err)
	}
	if existing.Configuration.URL != "https://new.test" {
		t.Errorf("url: %q", existing.Configuration.URL)
	}
	if existing.Configuration.Method != smplkit.JobHttpMethodPut {
		t.Errorf("method: %q", existing.Configuration.Method)
	}
	if existing.Configuration.Body == nil || *existing.Configuration.Body != "payload" {
		t.Errorf("body: %v", existing.Configuration.Body)
	}
}

func TestApplyJobInputsToModel_InvalidMethod(t *testing.T) {
	existing := &smplkit.Job{ID: "j", Configuration: smplkit.HttpConfig{URL: "https://x.test"}}
	in := jobInputs{method: "FETCH", methodSet: true}
	if err := applyJobInputsToModel(existing, nil, in); err == nil {
		t.Error("expected error for invalid --method on apply")
	}
}

func TestApplyJobFileToModel_AllConfigFields(t *testing.T) {
	tls := true
	ca := "-----BEGIN CERT-----"
	body := "from-file"
	existing := &smplkit.Job{
		ID:                "j",
		ConcurrencyPolicy: "ALLOW",
		Configuration: smplkit.HttpConfig{
			URL:     "https://orig.test",
			Headers: map[string]string{"X-Old": "1"},
		},
	}
	shape := &jobFileShape{
		ConcurrencyPolicy: "ALLOW",
		Configuration: &output.JobHTTPConfigAttr{
			URL:           "https://file.test",
			SuccessStatus: "204",
			Timeout:       45,
			Body:          &body,
			TLSVerify:     &tls,
			CACert:        &ca,
			Headers:       []output.JobHeaderAttr{{Name: "X-New", Value: "2"}},
		},
	}
	applyJobFileToModel(existing, shape)
	c := existing.Configuration
	if c.URL != "https://file.test" || c.SuccessStatus != "204" || c.Timeout != 45 {
		t.Errorf("scalar config fields: %+v", c)
	}
	if c.Body == nil || *c.Body != "from-file" {
		t.Errorf("body: %v", c.Body)
	}
	if c.TlsVerify == nil || !*c.TlsVerify {
		t.Errorf("tls_verify: %v", c.TlsVerify)
	}
	if c.CaCert == nil || *c.CaCert != ca {
		t.Errorf("ca_cert: %v", c.CaCert)
	}
	// File headers replace the existing ones.
	if len(c.Headers) != 1 || c.Headers["X-New"] != "2" {
		t.Errorf("file headers should replace existing: %#v", c.Headers)
	}
}

func TestIsJobNotFound(t *testing.T) {
	// The apply command relies on this to choose create-vs-update. A 404
	// (direct or wrapped) must read as not-found; anything else must not.
	nf := &smplkit.NotFoundError{Base: smplkit.Error{StatusCode: 404}}
	if !isJobNotFound(nf) {
		t.Error("a *NotFoundError should be detected")
	}
	if !isJobNotFound(fmt.Errorf("jobs Get: %w", nf)) {
		t.Error("a wrapped *NotFoundError should be detected")
	}
	if isJobNotFound(&smplkit.ValidationError{Base: smplkit.Error{StatusCode: 422}}) {
		t.Error("a ValidationError must not read as not-found (apply must not create on it)")
	}
	if isJobNotFound(errors.New("dial tcp: connection refused")) {
		t.Error("a plain error must not read as not-found")
	}
	if isJobNotFound(nil) {
		t.Error("nil must not read as not-found")
	}
}

func TestApplyJobEnvToggles_EnableDisablePreserveConfig(t *testing.T) {
	// A pre-existing override carries a per-environment configuration; a
	// --disable-env on that environment must flip enabled without dropping
	// the configuration, and --enable-env on a fresh environment creates it.
	job := &smplkit.Job{
		Environments: map[string]*smplkit.JobEnvironment{
			"production": {Enabled: true, URL: "https://prod.test"},
		},
	}
	if err := applyJobEnvToggles(job, []string{"staging"}, []string{"production"}); err != nil {
		t.Fatalf("applyJobEnvToggles: %v", err)
	}
	if job.Environments["production"].Enabled {
		t.Error("production should be disabled after --disable-env")
	}
	// The per-environment override leaf (ADR-056) must survive the enablement flip.
	if url := job.Environments["production"].URL; url != "https://prod.test" {
		t.Errorf("production override leaf should be preserved: %q", url)
	}
	if !job.Environments["staging"].Enabled {
		t.Error("staging should be enabled after --enable-env")
	}
}

func TestApplyJobEnvToggles_Conflict(t *testing.T) {
	if err := applyJobEnvToggles(&smplkit.Job{}, []string{"production"}, []string{"production"}); err == nil {
		t.Fatal("expected an error when an environment is both enabled and disabled")
	}
}

func TestApplyJobEnvToggles_NoFlagsNoOp(t *testing.T) {
	job := &smplkit.Job{}
	if err := applyJobEnvToggles(job, nil, nil); err != nil {
		t.Fatalf("applyJobEnvToggles: %v", err)
	}
	if job.Environments != nil {
		t.Errorf("no toggles should not allocate an environments map: %v", job.Environments)
	}
}

func TestBuildJobEnvironments_FileAndToggles(t *testing.T) {
	// File seeds production+staging; --enable-env flips staging on and
	// --disable-env flips production off; flags win over the file.
	shape := &jobFileShape{
		Environments: map[string]jobEnvFileShape{
			"production": {Enabled: true},
			"staging":    {Enabled: false},
		},
	}
	in := jobInputs{enableEnvs: []string{"staging"}, disableEnvs: []string{"production"}}
	envs, err := buildJobEnvironments(shape, in)
	if err != nil {
		t.Fatalf("buildJobEnvironments: %v", err)
	}
	if envs["production"].Enabled {
		t.Error("production should be disabled (flag wins over file)")
	}
	if !envs["staging"].Enabled {
		t.Error("staging should be enabled (flag wins over file)")
	}
}

func TestBuildJobEnvironments_TogglesOnly(t *testing.T) {
	envs, err := buildJobEnvironments(nil, jobInputs{enableEnvs: []string{"production"}})
	if err != nil {
		t.Fatalf("buildJobEnvironments: %v", err)
	}
	if !envs["production"].Enabled {
		t.Errorf("production should be enabled from --enable-env alone: %+v", envs)
	}
}

func TestApplyJobInputsToModel_EnvToggles(t *testing.T) {
	existing := &smplkit.Job{
		ID:            "j",
		Environments:  map[string]*smplkit.JobEnvironment{"production": {Enabled: true}},
		Configuration: smplkit.HttpConfig{URL: "https://orig.test"},
	}
	in := jobInputs{enableEnvs: []string{"staging"}, disableEnvs: []string{"production"}}
	if err := applyJobInputsToModel(existing, nil, in); err != nil {
		t.Fatalf("applyJobInputsToModel: %v", err)
	}
	if existing.Environments["production"].Enabled {
		t.Error("production should be disabled after --disable-env on apply")
	}
	if !existing.Environments["staging"].Enabled {
		t.Error("staging should be enabled after --enable-env on apply")
	}
}

func TestApplyJobInputsToModel_EnvToggleConflict(t *testing.T) {
	existing := &smplkit.Job{ID: "j", Configuration: smplkit.HttpConfig{URL: "https://x.test"}}
	in := jobInputs{enableEnvs: []string{"production"}, disableEnvs: []string{"production"}}
	if err := applyJobInputsToModel(existing, nil, in); err == nil {
		t.Error("expected error when an env is both enabled and disabled on apply")
	}
}

func TestApplyJobFileToModel_FullReplacesEnvironments(t *testing.T) {
	// A file `environments` map full-replaces the model's environments —
	// matching the audit forwarder noun's file semantics, with per-env
	// configuration overrides carried through.
	existing := &smplkit.Job{
		ID:            "j",
		Environments:  map[string]*smplkit.JobEnvironment{"production": {Enabled: true}},
		Configuration: smplkit.HttpConfig{URL: "https://orig.test"},
	}
	shape := &jobFileShape{
		Environments: map[string]jobEnvFileShape{
			"staging": {Enabled: true, Configuration: &output.JobHTTPConfigAttr{URL: "https://staging.test"}},
		},
	}
	applyJobFileToModel(existing, shape)
	if _, ok := existing.Environments["production"]; ok {
		t.Error("production should be gone after a full-replace from the file")
	}
	if !existing.Environments["staging"].Enabled {
		t.Error("staging should be enabled from the file")
	}
	// The nested per-env configuration flattens onto the override's URL leaf.
	if url := existing.Environments["staging"].URL; url != "https://staging.test" {
		t.Errorf("staging per-env override leaf should be applied: %q", url)
	}
}

// TestBuildJobForCreate_TimezoneFromFlag verifies the --timezone flag lands
// on the base job when supplied, mirroring --schedule.
func TestBuildJobForCreate_TimezoneFromFlag(t *testing.T) {
	ns := testJobsClient(t)
	job, err := buildJobForCreate(ns, "j", nil, jobInputs{
		schedule:    "0 0 * * *",
		scheduleSet: true,
		timezone:    "America/New_York",
		timezoneSet: true,
		url:         "https://x.test",
		urlSet:      true,
	})
	if err != nil {
		t.Fatalf("buildJobForCreate: %v", err)
	}
	if job.Timezone != "America/New_York" {
		t.Errorf("timezone from flag: %q", job.Timezone)
	}
}

// TestBuildJobForCreate_TimezoneFromFile verifies the base timezone falls back
// to the -f file when the flag is absent (flag wins, else file).
func TestBuildJobForCreate_TimezoneFromFile(t *testing.T) {
	ns := testJobsClient(t)
	shape := &jobFileShape{
		Schedule:      "0 4 * * *",
		Timezone:      "Europe/London",
		Configuration: &output.JobHTTPConfigAttr{URL: "https://file.test"},
	}
	job, err := buildJobForCreate(ns, "j", shape, jobInputs{})
	if err != nil {
		t.Fatalf("buildJobForCreate: %v", err)
	}
	if job.Timezone != "Europe/London" {
		t.Errorf("timezone should fall back to the file: %q", job.Timezone)
	}
}

// TestJobEnvFileToModel_CarriesTimezone verifies the per-environment timezone
// override round-trips from the file shape onto each environment, mirroring the
// per-env schedule.
func TestJobEnvFileToModel_CarriesTimezone(t *testing.T) {
	envs := jobEnvFileToModel(map[string]jobEnvFileShape{
		"production": {Enabled: true, Schedule: "0 1 * * *", Timezone: "Asia/Tokyo"},
	})
	if got := envs["production"].Timezone; got != "Asia/Tokyo" {
		t.Errorf("per-env timezone should carry through: %q", got)
	}
	if got := envs["production"].Schedule; got != "0 1 * * *" {
		t.Errorf("per-env schedule should carry through: %q", got)
	}
}

// TestApplyJobInputsToModel_TimezoneScalar verifies --timezone updates the base
// timezone on the apply (read-modify-write) path and leaves it untouched when
// unset.
func TestApplyJobInputsToModel_TimezoneScalar(t *testing.T) {
	existing := &smplkit.Job{
		ID:            "j",
		Schedule:      "0 0 * * *",
		Timezone:      "UTC",
		Configuration: smplkit.HttpConfig{URL: "https://orig.test"},
	}
	// Changing only the timezone leaves the schedule intact.
	in := jobInputs{timezone: "America/Chicago", timezoneSet: true}
	if err := applyJobInputsToModel(existing, nil, in); err != nil {
		t.Fatalf("applyJobInputsToModel: %v", err)
	}
	if existing.Timezone != "America/Chicago" {
		t.Errorf("timezone not updated: %q", existing.Timezone)
	}
	if existing.Schedule != "0 0 * * *" {
		t.Errorf("schedule should be preserved on a timezone-only apply: %q", existing.Schedule)
	}

	// A second apply with no timezone flag must leave the timezone untouched.
	if err := applyJobInputsToModel(existing, nil, jobInputs{name: "X", nameSet: true}); err != nil {
		t.Fatalf("applyJobInputsToModel: %v", err)
	}
	if existing.Timezone != "America/Chicago" {
		t.Errorf("timezone should survive an apply that does not set it: %q", existing.Timezone)
	}
}

// TestApplyJobFileToModel_Timezone verifies a -f file's base timezone is
// applied when set.
func TestApplyJobFileToModel_Timezone(t *testing.T) {
	existing := &smplkit.Job{
		ID:            "j",
		Schedule:      "0 0 * * *",
		Configuration: smplkit.HttpConfig{URL: "https://orig.test"},
	}
	applyJobFileToModel(existing, &jobFileShape{Timezone: "Australia/Sydney"})
	if existing.Timezone != "Australia/Sydney" {
		t.Errorf("timezone from file: %q", existing.Timezone)
	}
}

// TestBuildJobForCreate_RetryPolicyFromFlag verifies --retry-policy lands on
// the base job when supplied (via WithJobRetryPolicy).
func TestBuildJobForCreate_RetryPolicyFromFlag(t *testing.T) {
	ns := testJobsClient(t)
	job, err := buildJobForCreate(ns, "j", nil, jobInputs{
		schedule:       "0 0 * * *",
		scheduleSet:    true,
		retryPolicy:    "aggressive",
		retryPolicySet: true,
		url:            "https://x.test",
		urlSet:         true,
	})
	if err != nil {
		t.Fatalf("buildJobForCreate: %v", err)
	}
	if job.RetryPolicy != "aggressive" {
		t.Errorf("retry policy from flag: %q", job.RetryPolicy)
	}
}

// TestBuildJobForCreate_RetryPolicyFromFile verifies the base retry policy
// falls back to the -f file when the flag is absent (flag wins, else file).
func TestBuildJobForCreate_RetryPolicyFromFile(t *testing.T) {
	ns := testJobsClient(t)
	shape := &jobFileShape{
		Schedule:      "0 4 * * *",
		RetryPolicy:   "from-file",
		Configuration: &output.JobHTTPConfigAttr{URL: "https://file.test"},
	}
	job, err := buildJobForCreate(ns, "j", shape, jobInputs{})
	if err != nil {
		t.Fatalf("buildJobForCreate: %v", err)
	}
	if job.RetryPolicy != "from-file" {
		t.Errorf("retry policy should fall back to the file: %q", job.RetryPolicy)
	}
}

// TestApplyJobInputsToModel_RetryPolicyScalar verifies --retry-policy updates
// the base policy on the apply path and leaves it untouched when unset.
func TestApplyJobInputsToModel_RetryPolicyScalar(t *testing.T) {
	existing := &smplkit.Job{
		ID:            "j",
		RetryPolicy:   "old",
		Configuration: smplkit.HttpConfig{URL: "https://orig.test"},
	}
	in := jobInputs{retryPolicy: "new", retryPolicySet: true}
	if err := applyJobInputsToModel(existing, nil, in); err != nil {
		t.Fatalf("applyJobInputsToModel: %v", err)
	}
	if existing.RetryPolicy != "new" {
		t.Errorf("retry policy not updated: %q", existing.RetryPolicy)
	}
	// A second apply without the flag must leave it untouched.
	if err := applyJobInputsToModel(existing, nil, jobInputs{name: "X", nameSet: true}); err != nil {
		t.Fatalf("applyJobInputsToModel: %v", err)
	}
	if existing.RetryPolicy != "new" {
		t.Errorf("retry policy should survive an apply that does not set it: %q", existing.RetryPolicy)
	}
}

// TestApplyJobFileToModel_RetryPolicy verifies a -f file's base retry policy is
// applied when set.
func TestApplyJobFileToModel_RetryPolicy(t *testing.T) {
	existing := &smplkit.Job{ID: "j", Configuration: smplkit.HttpConfig{URL: "https://orig.test"}}
	applyJobFileToModel(existing, &jobFileShape{RetryPolicy: "from-file"})
	if existing.RetryPolicy != "from-file" {
		t.Errorf("retry policy from file: %q", existing.RetryPolicy)
	}
}

// TestJobEnvFileToModel_CarriesRetryPolicy verifies the per-environment retry
// policy override round-trips from the file shape onto each environment.
func TestJobEnvFileToModel_CarriesRetryPolicy(t *testing.T) {
	envs := jobEnvFileToModel(map[string]jobEnvFileShape{
		"production": {Enabled: true, RetryPolicy: "prod-policy"},
	})
	if got := envs["production"].RetryPolicy; got != "prod-policy" {
		t.Errorf("per-env retry policy should carry through: %q", got)
	}
}

// TestParseRunTrigger covers the --trigger value mapping: each valid spelling
// (case-insensitive) maps to its SDK enum, and anything else is rejected.
func TestParseRunTrigger(t *testing.T) {
	cases := map[string]smplkit.RunTrigger{
		"MANUAL":   smplkit.RunTriggerManual,
		"rerun":    smplkit.RunTriggerRerun,
		" retry ":  smplkit.RunTriggerRetry,
		"Schedule": smplkit.RunTriggerSchedule,
	}
	for in, want := range cases {
		got, err := parseRunTrigger(in)
		if err != nil {
			t.Errorf("parseRunTrigger(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseRunTrigger(%q) = %q want %q", in, got, want)
		}
	}
	if _, err := parseRunTrigger("CRON"); err == nil {
		t.Error("expected error for an invalid --trigger value")
	}
}

// TestParseRunTriggers verifies the list helper validates each entry and
// rejects an invalid one.
func TestParseRunTriggers(t *testing.T) {
	got, err := parseRunTriggers([]string{"retry", "MANUAL"})
	if err != nil {
		t.Fatalf("parseRunTriggers: %v", err)
	}
	if len(got) != 2 || got[0] != smplkit.RunTriggerRetry || got[1] != smplkit.RunTriggerManual {
		t.Errorf("parseRunTriggers: %#v", got)
	}
	if _, err := parseRunTriggers([]string{"retry", "bogus"}); err == nil {
		t.Error("expected error when a trigger in the list is invalid")
	}
}

// TestJobRunsListCmd_TriggerAndLastRunOnly verifies the new --trigger and
// --last-run-only flags are wired, and that an invalid --trigger is rejected
// before any client call.
func TestJobRunsListCmd_TriggerAndLastRunOnly(t *testing.T) {
	cmd := jobRunsListCmd()
	if cmd.Flags().Lookup("trigger") == nil {
		t.Error("runs list command missing --trigger flag")
	}
	if cmd.Flags().Lookup("last-run-only") == nil {
		t.Error("runs list command missing --last-run-only flag")
	}
	cmd.SetArgs([]string{"--trigger", "BOGUS"})
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	err := cmd.Execute()
	const want = "invalid --trigger"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("--trigger BOGUS: got err %v, want containing %q", err, want)
	}
}

// testJobsClient builds a JobsClient for the pure builder helpers. The SDK
// does no I/O at construction, so a dummy API key is enough; ns.New only
// assembles an in-memory *Job.
func testJobsClient(t *testing.T) *smplkit.JobsClient {
	t.Helper()
	ns, err := smplkit.NewJobsClient(smplkit.Config{APIKey: "test-key"})
	if err != nil {
		t.Fatalf("NewJobsClient: %v", err)
	}
	return ns
}
