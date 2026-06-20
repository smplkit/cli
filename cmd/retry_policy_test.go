package cmd

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	smplkit "github.com/smplkit/go-sdk/v3"
)

// TestParseBackoff covers the --backoff value mapping: each valid spelling
// (case-insensitive) maps to its SDK enum, and anything else is rejected.
func TestParseBackoff(t *testing.T) {
	cases := map[string]smplkit.Backoff{
		"exponential": smplkit.BackoffExponential,
		"EXPONENTIAL": smplkit.BackoffExponential,
		" fixed ":     smplkit.BackoffFixed,
		"Fixed":       smplkit.BackoffFixed,
	}
	for in, want := range cases {
		got, err := parseBackoff(in)
		if err != nil {
			t.Errorf("parseBackoff(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseBackoff(%q) = %q want %q", in, got, want)
		}
	}
	if _, err := parseBackoff("linear"); err == nil {
		t.Error("expected error for an invalid --backoff value")
	}
}

// TestParseRetryReason covers the --retry-reason value mapping: each valid
// spelling (case-insensitive) maps to its SDK enum, and anything else is
// rejected.
func TestParseRetryReason(t *testing.T) {
	cases := map[string]smplkit.RetryReason{
		"CONNECTION_ERROR":   smplkit.RetryReasonConnectionError,
		"connection_error":   smplkit.RetryReasonConnectionError,
		"NON_SUCCESS_STATUS": smplkit.RetryReasonNonSuccessStatus,
		" timeout ":          smplkit.RetryReasonTimeout,
	}
	for in, want := range cases {
		got, err := parseRetryReason(in)
		if err != nil {
			t.Errorf("parseRetryReason(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseRetryReason(%q) = %q want %q", in, got, want)
		}
	}
	if _, err := parseRetryReason("BOGUS"); err == nil {
		t.Error("expected error for an invalid --retry-reason value")
	}
}

// TestParseRetryReasons verifies the list helper validates each entry and that
// a nil/empty input yields a nil slice (retries nothing).
func TestParseRetryReasons(t *testing.T) {
	got, err := parseRetryReasons([]string{"timeout", "CONNECTION_ERROR"})
	if err != nil {
		t.Fatalf("parseRetryReasons: %v", err)
	}
	if len(got) != 2 || got[0] != smplkit.RetryReasonTimeout || got[1] != smplkit.RetryReasonConnectionError {
		t.Errorf("parseRetryReasons: %#v", got)
	}
	if nilSlice, err := parseRetryReasons(nil); err != nil || nilSlice != nil {
		t.Errorf("empty input should yield (nil, nil): %#v %v", nilSlice, err)
	}
	if _, err := parseRetryReasons([]string{"timeout", "bogus"}); err == nil {
		t.Error("expected error when a reason in the list is invalid")
	}
}

// TestRetryPolicyCreateCmd_InvalidBackoff verifies --backoff rejects a value
// outside exponential/fixed before any client call.
func TestRetryPolicyCreateCmd_InvalidBackoff(t *testing.T) {
	ns := testRetryPoliciesClient(t)
	_, err := buildRetryPolicyForCreate(ns, "p", nil, retryPolicyInputs{
		backoff: "linear", backoffSet: true,
	})
	const want = "invalid --backoff"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("got err %v, want containing %q", err, want)
	}
}

// TestRetryPolicyScalarFlags_Wired verifies the create/apply scalar flags are
// registered with the expected long kebab-case names.
func TestRetryPolicyScalarFlags_Wired(t *testing.T) {
	cmd := retryPolicyCreateCmd()
	for _, name := range []string{
		"name", "max-retries", "backoff", "delay-seconds",
		"max-delay-seconds", "retry-status", "retry-reason", "file",
	} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("create command missing --%s flag", name)
		}
	}
	apply := retryPolicyApplyCmd()
	for _, name := range []string{"max-retries", "retry-status", "retry-reason"} {
		if apply.Flags().Lookup(name) == nil {
			t.Errorf("apply command missing --%s flag", name)
		}
	}
}

// TestRetryPolicyListCmd_Flags verifies the list command exposes --limit,
// --all, and --name (mirroring job list).
func TestRetryPolicyListCmd_Flags(t *testing.T) {
	cmd := retryPolicyListCmd()
	for _, name := range []string{"limit", "all", "name"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("list command missing --%s flag", name)
		}
	}
}

func TestBuildRetryPolicyForCreate_NameDefaultsToID(t *testing.T) {
	ns := testRetryPoliciesClient(t)
	p, err := buildRetryPolicyForCreate(ns, "my-policy", nil, retryPolicyInputs{
		maxRetries: 3, maxRetriesSet: true,
		delaySeconds: 5, delaySecondsSet: true,
	})
	if err != nil {
		t.Fatalf("buildRetryPolicyForCreate: %v", err)
	}
	if p.Name != "my-policy" {
		t.Errorf("name should default to id, got %q", p.Name)
	}
	if p.MaxRetries != 3 {
		t.Errorf("max retries: %d", p.MaxRetries)
	}
	if p.DelaySeconds != 5 {
		t.Errorf("delay seconds: %d", p.DelaySeconds)
	}
	// Backoff defaults to exponential when no flag and no file.
	if p.Backoff != smplkit.BackoffExponential {
		t.Errorf("backoff should default to exponential, got %q", p.Backoff)
	}
}

func TestBuildRetryPolicyForCreate_FlagsAndRetryOn(t *testing.T) {
	ns := testRetryPoliciesClient(t)
	p, err := buildRetryPolicyForCreate(ns, "p", nil, retryPolicyInputs{
		name: "Aggressive", nameSet: true,
		maxRetries: 5, maxRetriesSet: true,
		backoff: "fixed", backoffSet: true,
		delaySeconds: 10, delaySecondsSet: true,
		maxDelaySeconds: 0, maxDelaySecondsSet: true, // fixed backoff: max-delay 0 must be omitted
		retryStatuses: []int{429, 503}, retryStatusesSet: true,
		retryReasons: []string{"TIMEOUT"}, retryReasonsSet: true,
	})
	if err != nil {
		t.Fatalf("buildRetryPolicyForCreate: %v", err)
	}
	if p.Name != "Aggressive" || p.Backoff != smplkit.BackoffFixed || p.MaxRetries != 5 || p.DelaySeconds != 10 {
		t.Errorf("scalar fields: %+v", p)
	}
	// max-delay-seconds 0 with fixed backoff is not applied (stays uncapped/nil).
	if p.MaxDelaySeconds != nil {
		t.Errorf("max-delay-seconds 0 should leave the cap nil: %v", p.MaxDelaySeconds)
	}
	if len(p.RetryOn.Statuses) != 2 || p.RetryOn.Statuses[0] != 429 {
		t.Errorf("retry-status: %#v", p.RetryOn.Statuses)
	}
	if len(p.RetryOn.Reasons) != 1 || p.RetryOn.Reasons[0] != smplkit.RetryReasonTimeout {
		t.Errorf("retry-reason: %#v", p.RetryOn.Reasons)
	}
}

func TestBuildRetryPolicyForCreate_MaxDelayExponential(t *testing.T) {
	ns := testRetryPoliciesClient(t)
	p, err := buildRetryPolicyForCreate(ns, "p", nil, retryPolicyInputs{
		maxRetries: 4, maxRetriesSet: true,
		backoff: "exponential", backoffSet: true,
		delaySeconds: 2, delaySecondsSet: true,
		maxDelaySeconds: 60, maxDelaySecondsSet: true,
	})
	if err != nil {
		t.Fatalf("buildRetryPolicyForCreate: %v", err)
	}
	if p.MaxDelaySeconds == nil || *p.MaxDelaySeconds != 60 {
		t.Errorf("max-delay-seconds should be applied for exponential backoff: %v", p.MaxDelaySeconds)
	}
}

func TestBuildRetryPolicyForCreate_FromFile(t *testing.T) {
	ns := testRetryPoliciesClient(t)
	mr := 2
	ds := 7
	mds := 120
	shape := &retryPolicyFileShape{
		Name:            "From File",
		MaxRetries:      &mr,
		Backoff:         "exponential",
		DelaySeconds:    &ds,
		MaxDelaySeconds: &mds,
		RetryOn: &retryOnFileShape{
			Statuses: []int{500},
			Reasons:  []string{"NON_SUCCESS_STATUS"},
		},
	}
	p, err := buildRetryPolicyForCreate(ns, "p", shape, retryPolicyInputs{})
	if err != nil {
		t.Fatalf("buildRetryPolicyForCreate: %v", err)
	}
	if p.Name != "From File" || p.MaxRetries != 2 || p.DelaySeconds != 7 {
		t.Errorf("fields from file: %+v", p)
	}
	if p.MaxDelaySeconds == nil || *p.MaxDelaySeconds != 120 {
		t.Errorf("max-delay-seconds from file: %v", p.MaxDelaySeconds)
	}
	if len(p.RetryOn.Statuses) != 1 || p.RetryOn.Statuses[0] != 500 {
		t.Errorf("retry_on.statuses from file: %#v", p.RetryOn.Statuses)
	}
	if len(p.RetryOn.Reasons) != 1 || p.RetryOn.Reasons[0] != smplkit.RetryReasonNonSuccessStatus {
		t.Errorf("retry_on.reasons from file: %#v", p.RetryOn.Reasons)
	}
}

func TestBuildRetryPolicyForCreate_InvalidFileReason(t *testing.T) {
	ns := testRetryPoliciesClient(t)
	shape := &retryPolicyFileShape{
		RetryOn: &retryOnFileShape{Reasons: []string{"BOGUS"}},
	}
	if _, err := buildRetryPolicyForCreate(ns, "p", shape, retryPolicyInputs{}); err == nil {
		t.Error("expected error for an invalid retry_on reason in the file")
	}
}

func TestApplyRetryPolicyInputsToModel_PreservesUnspecified(t *testing.T) {
	mds := 90
	existing := &smplkit.RetryPolicy{
		ID:              "p",
		Name:            "Original",
		MaxRetries:      3,
		Backoff:         smplkit.BackoffExponential,
		DelaySeconds:    5,
		MaxDelaySeconds: &mds,
		RetryOn: smplkit.RetryOn{
			Statuses: []int{429},
			Reasons:  []smplkit.RetryReason{smplkit.RetryReasonTimeout},
		},
	}
	// Only change max-retries. Everything else must survive.
	in := retryPolicyInputs{maxRetries: 7, maxRetriesSet: true}
	if err := applyRetryPolicyInputsToModel(existing, nil, in); err != nil {
		t.Fatalf("applyRetryPolicyInputsToModel: %v", err)
	}
	if existing.MaxRetries != 7 {
		t.Errorf("max retries not updated: %d", existing.MaxRetries)
	}
	if existing.Name != "Original" {
		t.Errorf("name should be preserved: %q", existing.Name)
	}
	if existing.DelaySeconds != 5 || existing.Backoff != smplkit.BackoffExponential {
		t.Errorf("backoff/delay should be preserved: %+v", existing)
	}
	if existing.MaxDelaySeconds == nil || *existing.MaxDelaySeconds != 90 {
		t.Errorf("max-delay-seconds should be preserved: %v", existing.MaxDelaySeconds)
	}
	if len(existing.RetryOn.Statuses) != 1 || existing.RetryOn.Statuses[0] != 429 {
		t.Errorf("retry_on.statuses should be preserved: %#v", existing.RetryOn.Statuses)
	}
	if len(existing.RetryOn.Reasons) != 1 || existing.RetryOn.Reasons[0] != smplkit.RetryReasonTimeout {
		t.Errorf("retry_on.reasons should be preserved: %#v", existing.RetryOn.Reasons)
	}
}

func TestApplyRetryPolicyInputsToModel_ClearsMaxDelayOnZero(t *testing.T) {
	mds := 90
	existing := &smplkit.RetryPolicy{ID: "p", MaxDelaySeconds: &mds}
	// --max-delay-seconds 0 explicitly clears the cap.
	in := retryPolicyInputs{maxDelaySeconds: 0, maxDelaySecondsSet: true}
	if err := applyRetryPolicyInputsToModel(existing, nil, in); err != nil {
		t.Fatalf("applyRetryPolicyInputsToModel: %v", err)
	}
	if existing.MaxDelaySeconds != nil {
		t.Errorf("max-delay-seconds 0 should clear the cap: %v", existing.MaxDelaySeconds)
	}
}

func TestApplyRetryPolicyInputsToModel_ScalarOverridesFile(t *testing.T) {
	existing := &smplkit.RetryPolicy{ID: "p", Name: "Original", DelaySeconds: 1}
	mr := 2
	shape := &retryPolicyFileShape{Name: "From File", MaxRetries: &mr}
	in := retryPolicyInputs{name: "From Flag", nameSet: true}
	if err := applyRetryPolicyInputsToModel(existing, shape, in); err != nil {
		t.Fatalf("applyRetryPolicyInputsToModel: %v", err)
	}
	// File applied first, then scalar flag overrides name.
	if existing.Name != "From Flag" {
		t.Errorf("scalar name should win: %q", existing.Name)
	}
	// max-retries came from the file (no scalar override).
	if existing.MaxRetries != 2 {
		t.Errorf("file max-retries should be applied: %d", existing.MaxRetries)
	}
}

func TestApplyRetryPolicyInputsToModel_InvalidBackoff(t *testing.T) {
	existing := &smplkit.RetryPolicy{ID: "p"}
	in := retryPolicyInputs{backoff: "linear", backoffSet: true}
	if err := applyRetryPolicyInputsToModel(existing, nil, in); err == nil {
		t.Error("expected error for an invalid --backoff on apply")
	}
}

func TestApplyRetryPolicyInputsToModel_InvalidReason(t *testing.T) {
	existing := &smplkit.RetryPolicy{ID: "p"}
	in := retryPolicyInputs{retryReasons: []string{"BOGUS"}, retryReasonsSet: true}
	if err := applyRetryPolicyInputsToModel(existing, nil, in); err == nil {
		t.Error("expected error for an invalid --retry-reason on apply")
	}
}

func TestApplyRetryPolicyInputsToModel_ReplacesRetryOnLists(t *testing.T) {
	existing := &smplkit.RetryPolicy{
		ID: "p",
		RetryOn: smplkit.RetryOn{
			Statuses: []int{429, 503},
			Reasons:  []smplkit.RetryReason{smplkit.RetryReasonTimeout},
		},
	}
	in := retryPolicyInputs{
		retryStatuses: []int{500}, retryStatusesSet: true,
		retryReasons: []string{"CONNECTION_ERROR"}, retryReasonsSet: true,
	}
	if err := applyRetryPolicyInputsToModel(existing, nil, in); err != nil {
		t.Fatalf("applyRetryPolicyInputsToModel: %v", err)
	}
	if len(existing.RetryOn.Statuses) != 1 || existing.RetryOn.Statuses[0] != 500 {
		t.Errorf("--retry-status should replace the full list: %#v", existing.RetryOn.Statuses)
	}
	if len(existing.RetryOn.Reasons) != 1 || existing.RetryOn.Reasons[0] != smplkit.RetryReasonConnectionError {
		t.Errorf("--retry-reason should replace the full list: %#v", existing.RetryOn.Reasons)
	}
}

func TestApplyRetryPolicyFileToModel_InvalidBackoff(t *testing.T) {
	existing := &smplkit.RetryPolicy{ID: "p"}
	shape := &retryPolicyFileShape{Backoff: "linear"}
	if err := applyRetryPolicyFileToModel(existing, shape); err == nil {
		t.Error("expected error for an invalid backoff in the file")
	}
}

func TestIsRetryPolicyNotFound(t *testing.T) {
	nf := &smplkit.NotFoundError{Base: smplkit.Error{StatusCode: 404}}
	if !isRetryPolicyNotFound(nf) {
		t.Error("a *NotFoundError should be detected")
	}
	if !isRetryPolicyNotFound(fmt.Errorf("jobs RetryPolicies.Get: %w", nf)) {
		t.Error("a wrapped *NotFoundError should be detected")
	}
	if isRetryPolicyNotFound(&smplkit.ValidationError{Base: smplkit.Error{StatusCode: 422}}) {
		t.Error("a ValidationError must not read as not-found")
	}
	if isRetryPolicyNotFound(errors.New("dial tcp: connection refused")) {
		t.Error("a plain error must not read as not-found")
	}
	if isRetryPolicyNotFound(nil) {
		t.Error("nil must not read as not-found")
	}
}

// testRetryPoliciesClient builds a RetryPoliciesClient for the pure builder
// helpers. The SDK does no I/O at construction, so a dummy API key is enough.
func testRetryPoliciesClient(t *testing.T) *smplkit.RetryPoliciesClient {
	t.Helper()
	ns, err := smplkit.NewJobsClient(smplkit.Config{APIKey: "test-key"})
	if err != nil {
		t.Fatalf("NewJobsClient: %v", err)
	}
	return ns.RetryPolicies()
}
