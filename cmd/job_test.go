package cmd

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	smplkit "github.com/smplkit/go-sdk/v3"

	"github.com/smplkit/cli/internal/output"
)

// TestJobListCmd_MutuallyExclusiveFilters verifies the list filter pairs
// reject conflicting flags before any client call, so the error is a clean
// validation failure rather than a wasted round-trip.
func TestJobListCmd_MutuallyExclusiveFilters(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"--enabled", "--disabled"}, "--enabled and --disabled are mutually exclusive"},
		{[]string{"--recurring", "--one-off"}, "--recurring and --one-off are mutually exclusive"},
	}
	for _, c := range cases {
		cmd := jobListCmd()
		cmd.SetArgs(c.args)
		cmd.SilenceUsage = true
		cmd.SilenceErrors = true
		err := cmd.Execute()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("args %v: got err %v, want containing %q", c.args, err, c.want)
		}
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
	if hdrs[0].Name != "X-Source" || hdrs[0].Value != "cli" {
		t.Errorf("header 0: %#v", hdrs[0])
	}
	// Split on the FIRST colon: the value keeps its own colons.
	if hdrs[1].Name != "Authorization" || hdrs[1].Value != "Bearer abc:def" {
		t.Errorf("header 1: %#v", hdrs[1])
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
	if len(cfg.Headers) != 1 || cfg.Headers[0].Name != "X-Flag" {
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

func TestBuildJobForCreate_RequiresSchedule(t *testing.T) {
	ns := testJobsClient(t)
	if _, err := buildJobForCreate(ns, "j", nil, jobInputs{url: "https://x.test", urlSet: true}); err == nil {
		t.Error("expected error without a schedule")
	}
}

func TestBuildJobForCreate_AppliesFileOptions(t *testing.T) {
	ns := testJobsClient(t)
	enabled := false
	desc := "control-tower housekeeping"
	// A non-default ConcurrencyPolicy so the WithJobConcurrencyPolicy branch
	// is genuinely exercised (the SDK's New() already defaults to "ALLOW",
	// so asserting "ALLOW" would be vacuous). "FORBID" is a test-only
	// sentinel; the server accepts only "ALLOW" today.
	shape := &jobFileShape{
		Name:              "From File",
		Schedule:          "0 4 * * *", // schedule falls back to the file
		Enabled:           &enabled,
		Description:       &desc,
		ConcurrencyPolicy: "FORBID",
		Configuration:     &output.JobHTTPConfigAttr{URL: "https://file.test"},
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
	if job.Enabled {
		t.Error("enabled=false from file should be applied")
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
		Enabled:           false,
		ConcurrencyPolicy: "ALLOW",
		Configuration: smplkit.HttpConfig{
			URL:           "https://orig.test",
			Method:        smplkit.JobHttpMethodPost,
			Body:          &body,
			SuccessStatus: "204",
			Timeout:       45,
			TlsVerify:     &tls,
			Headers: []smplkit.HttpHeader{
				{Name: "Authorization", Value: "Bearer secret"},
			},
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
	if existing.Enabled {
		t.Error("enabled=false should be preserved (not flipped to the New() default)")
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
		existing.Configuration.Headers[0].Value != "Bearer secret" {
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
			Headers: []smplkit.HttpHeader{{Name: "Authorization", Value: "Bearer OLD"}},
		},
	}
	in := jobInputs{headers: []string{"Authorization: Bearer NEW"}, headerSet: true}
	if err := applyJobInputsToModel(existing, nil, in); err != nil {
		t.Fatalf("applyJobInputsToModel: %v", err)
	}
	if len(existing.Configuration.Headers) != 1 ||
		existing.Configuration.Headers[0].Value != "Bearer NEW" {
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
			Headers: []smplkit.HttpHeader{
				{Name: "Authorization", Value: "Bearer OLD"},
				{Name: "Content-Type", Value: "application/json"},
			},
		},
	}
	in := jobInputs{headers: []string{"Authorization: Bearer NEW"}, headerSet: true}
	if err := applyJobInputsToModel(existing, nil, in); err != nil {
		t.Fatalf("applyJobInputsToModel: %v", err)
	}
	if len(existing.Configuration.Headers) != 1 ||
		existing.Configuration.Headers[0].Value != "Bearer NEW" {
		t.Errorf("--header should replace the full set: %#v", existing.Configuration.Headers)
	}
}

func TestApplyJobFileToModel_AppliesSetFieldsOnly(t *testing.T) {
	tls := false
	existing := &smplkit.Job{
		ID:       "j",
		Name:     "Original",
		Schedule: "0 0 * * *",
		Enabled:  true,
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
			Headers: []smplkit.HttpHeader{{Name: "X-Old", Value: "1"}},
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
	if len(c.Headers) != 1 || c.Headers[0].Name != "X-New" {
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
