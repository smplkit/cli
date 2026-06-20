package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	smplkit "github.com/smplkit/go-sdk/v3"
	"github.com/spf13/cobra"
)

// retryPolicyFileShape mirrors output.RetryPolicyAttr but with omittable
// fields so a `get -o json | smplkit retry-policy apply -f -` round-trip is
// clean. Reused for both create and apply.
//
// The read-only server fields (created_at, updated_at, version) carried by a
// `get -o json` snapshot are absent here: they are ignored on replay.
type retryPolicyFileShape struct {
	ID              string            `json:"id,omitempty"`
	Name            string            `json:"name,omitempty"`
	MaxRetries      *int              `json:"max_retries,omitempty"`
	Backoff         string            `json:"backoff,omitempty"`
	DelaySeconds    *int              `json:"delay_seconds,omitempty"`
	MaxDelaySeconds *int              `json:"max_delay_seconds,omitempty"`
	RetryOn         *retryOnFileShape `json:"retry_on,omitempty"`
}

// retryOnFileShape is the file representation of the RetryOn payload: the
// status codes and failure reasons a policy retries.
type retryOnFileShape struct {
	Statuses []int    `json:"statuses,omitempty"`
	Reasons  []string `json:"reasons,omitempty"`
}

func registerRetryPolicyCmd(root *cobra.Command) {
	policy := &cobra.Command{
		Use:   "retry-policy",
		Short: "Manage reusable retry policies for jobs",
	}

	policy.AddCommand(retryPolicyListCmd())
	policy.AddCommand(retryPolicyGetCmd())
	policy.AddCommand(retryPolicyCreateCmd())
	policy.AddCommand(retryPolicyApplyCmd())
	policy.AddCommand(retryPolicyDeleteCmd())

	root.AddCommand(policy)
}

func retryPolicyListCmd() *cobra.Command {
	var (
		limit int
		all   bool
		name  string
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List retry policies",
		Long: "List the account's retry policies. Filter by name with --name (matches a\n" +
			"case-insensitive substring of the policy name). Retry policies are\n" +
			"account-global — never environment-scoped.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			input := smplkit.ListRetryPoliciesInput{}
			if name != "" {
				n := name
				input.Name = &n
			}
			client, err := withClient()
			if err != nil {
				return err
			}
			ctx := cliContext(cmd)
			ns := client.Jobs().RetryPolicies()
			var policies []*smplkit.RetryPolicy
			if all {
				policies, err = fetchAllRetryPolicies(ctx, ns, input, limit)
			} else {
				if limit > 0 {
					input.PageSize = limit
				}
				policies, err = ns.List(ctx, input)
			}
			if err != nil {
				return err
			}
			return renderer(cmd).RenderRetryPolicies(policies)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 0, "page size (server default when unset)")
	cmd.Flags().BoolVar(&all, "all", false, "fetch every page")
	cmd.Flags().StringVar(&name, "name", "", "filter to policies whose name contains this text (case-insensitive)")
	return cmd
}

// fetchAllRetryPolicies walks every page of the retry-policies collection,
// preserving the caller's Name filter. Mirrors fetchAllJobs — the retry-policy
// List surface takes a ListRetryPoliciesInput (offset pagination) rather than
// the variadic ListOption fetcher paginate.All expects.
func fetchAllRetryPolicies(ctx context.Context, ns *smplkit.RetryPoliciesClient, input smplkit.ListRetryPoliciesInput, limit int) ([]*smplkit.RetryPolicy, error) {
	pageSize := 1000
	if limit > 0 && limit < pageSize {
		pageSize = limit
	}
	var out []*smplkit.RetryPolicy
	for page := 1; ; page++ {
		input.PageNumber = page
		input.PageSize = pageSize
		policies, err := ns.List(ctx, input)
		if err != nil {
			return nil, err
		}
		out = append(out, policies...)
		if len(policies) < pageSize {
			return out, nil
		}
	}
}

func retryPolicyGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Get a retry policy by id",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := withClient()
			if err != nil {
				return err
			}
			p, err := client.Jobs().RetryPolicies().Get(cliContext(cmd), args[0])
			if err != nil {
				return err
			}
			return renderer(cmd).RenderRetryPolicy(p)
		},
	}
}

func retryPolicyCreateCmd() *cobra.Command {
	var in retryPolicyInputs
	cmd := &cobra.Command{
		Use:   "create <id>",
		Short: "Create a retry policy",
		Long: "Create a reusable retry policy. Supply --max-retries, --backoff, and\n" +
			"--delay-seconds for the simple case, or -f policy.json (produced by\n" +
			"`smplkit retry-policy get -o json`) to round-trip a full definition.\n" +
			"Scalar flags override file values where both are supplied. --retry-status\n" +
			"(repeatable) and --retry-reason (repeatable) set which failures to retry.\n" +
			"--max-delay-seconds caps the wait for exponential backoff only.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			client, err := withClient()
			if err != nil {
				return err
			}
			ns := client.Jobs().RetryPolicies()

			shape, err := loadRetryPolicyFile(in.file)
			if err != nil {
				return err
			}
			in.readChanged(cmd)

			policy, err := buildRetryPolicyForCreate(ns, id, shape, in)
			if err != nil {
				return err
			}
			if err := policy.Save(cliContext(cmd)); err != nil {
				return err
			}
			return renderer(cmd).RenderRetryPolicy(policy)
		},
	}
	addRetryPolicyScalarFlags(cmd, &in)
	return cmd
}

func retryPolicyApplyCmd() *cobra.Command {
	var in retryPolicyInputs
	cmd := &cobra.Command{
		Use:   "apply <id>",
		Short: "Create or update a retry policy (idempotent upsert)",
		Long: "Reconciles a retry policy to the supplied definition: GET the policy; if it\n" +
			"exists, apply -f file then the changed scalar flags (every field you don't\n" +
			"pass is preserved from the server) and PUT it back; if it does not exist,\n" +
			"create it. Safe to run repeatedly — re-running with the same flags is a\n" +
			"no-op, and changing a flag reconciles drift. Note: --retry-status and\n" +
			"--retry-reason each set the COMPLETE list, so re-supply every value you\n" +
			"want to keep. Built for scheduled CI keeping a policy in a desired state.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			client, err := withClient()
			if err != nil {
				return err
			}
			ns := client.Jobs().RetryPolicies()
			ctx := cliContext(cmd)

			shape, err := loadRetryPolicyFile(in.file)
			if err != nil {
				return err
			}
			in.readChanged(cmd)

			existing, gerr := ns.Get(ctx, id)
			if gerr != nil {
				if !isRetryPolicyNotFound(gerr) {
					// A real error (auth, network, 5xx) — never silently create.
					return gerr
				}
				// Not found → create.
				policy, berr := buildRetryPolicyForCreate(ns, id, shape, in)
				if berr != nil {
					return berr
				}
				if err := policy.Save(ctx); err != nil {
					return err
				}
				return renderer(cmd).RenderRetryPolicy(policy)
			}

			// Found → mutate in place and full-replace.
			if err := applyRetryPolicyInputsToModel(existing, shape, in); err != nil {
				return err
			}
			if err := existing.Save(ctx); err != nil {
				return err
			}
			return renderer(cmd).RenderRetryPolicy(existing)
		},
	}
	addRetryPolicyScalarFlags(cmd, &in)
	return cmd
}

func retryPolicyDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a retry policy",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if !confirm(cmd, yes, fmt.Sprintf("Delete retry policy %q?", id)) {
				return fmt.Errorf("aborted")
			}
			client, err := withClient()
			if err != nil {
				return err
			}
			if err := client.Jobs().RetryPolicies().Delete(cliContext(cmd), id); err != nil {
				return err
			}
			if globals.Quiet {
				fmt.Fprintln(cmd.OutOrStdout(), id)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

// retryPolicyInputs bundles the scalar create/apply flags plus a record of
// which were explicitly set, so the apply (read-modify-write) path leaves
// unspecified fields untouched and create can fall back to the -f file.
type retryPolicyInputs struct {
	name            string
	maxRetries      int
	backoff         string
	delaySeconds    int
	maxDelaySeconds int
	retryStatuses   []int
	retryReasons    []string
	file            string

	nameSet            bool
	maxRetriesSet      bool
	backoffSet         bool
	delaySecondsSet    bool
	maxDelaySecondsSet bool
	retryStatusesSet   bool
	retryReasonsSet    bool
}

func addRetryPolicyScalarFlags(cmd *cobra.Command, in *retryPolicyInputs) {
	cmd.Flags().StringVar(&in.name, "name", "", "display name (defaults to the id)")
	cmd.Flags().IntVar(&in.maxRetries, "max-retries", 0, "retries after the initial attempt (3 means up to 4 attempts total; 0 disables; max 10)")
	cmd.Flags().StringVar(&in.backoff, "backoff", string(smplkit.BackoffExponential), "how the wait between retries grows: exponential|fixed")
	cmd.Flags().IntVar(&in.delaySeconds, "delay-seconds", 0, "wait before a retry (constant for fixed; base that doubles each retry for exponential)")
	cmd.Flags().IntVar(&in.maxDelaySeconds, "max-delay-seconds", 0, "ceiling on the wait between retries (exponential backoff only)")
	cmd.Flags().IntSliceVar(&in.retryStatuses, "retry-status", nil, "response status code to retry (repeatable): --retry-status 429 --retry-status 503")
	cmd.Flags().StringArrayVar(&in.retryReasons, "retry-reason", nil, "failure category to retry (repeatable): CONNECTION_ERROR|NON_SUCCESS_STATUS|TIMEOUT")
	cmd.Flags().StringVarP(&in.file, "file", "f", "", "load definition from JSON file (round-trips `get -o json`)")
}

// readChanged records which scalar flags the user actually supplied. Must be
// called after flag parsing (inside RunE).
func (in *retryPolicyInputs) readChanged(cmd *cobra.Command) {
	in.nameSet = cmd.Flags().Changed("name")
	in.maxRetriesSet = cmd.Flags().Changed("max-retries")
	in.backoffSet = cmd.Flags().Changed("backoff")
	in.delaySecondsSet = cmd.Flags().Changed("delay-seconds")
	in.maxDelaySecondsSet = cmd.Flags().Changed("max-delay-seconds")
	in.retryStatusesSet = cmd.Flags().Changed("retry-status")
	in.retryReasonsSet = cmd.Flags().Changed("retry-reason")
}

// buildRetryPolicyForCreate assembles a fresh, unsaved *RetryPolicy from the
// -f file and the scalar flags (flags win). Used by `create` and by `apply`
// when the policy does not yet exist.
func buildRetryPolicyForCreate(ns *smplkit.RetryPoliciesClient, id string, shape *retryPolicyFileShape, in retryPolicyInputs) (*smplkit.RetryPolicy, error) {
	effName := id
	switch {
	case in.nameSet && in.name != "":
		effName = in.name
	case shape != nil && shape.Name != "":
		effName = shape.Name
	}

	effMaxRetries := 0
	switch {
	case in.maxRetriesSet:
		effMaxRetries = in.maxRetries
	case shape != nil && shape.MaxRetries != nil:
		effMaxRetries = *shape.MaxRetries
	}

	effDelaySeconds := 0
	switch {
	case in.delaySecondsSet:
		effDelaySeconds = in.delaySeconds
	case shape != nil && shape.DelaySeconds != nil:
		effDelaySeconds = *shape.DelaySeconds
	}

	effBackoffRaw := string(smplkit.BackoffExponential)
	switch {
	case in.backoffSet:
		effBackoffRaw = in.backoff
	case shape != nil && shape.Backoff != "":
		effBackoffRaw = shape.Backoff
	}
	backoff, err := parseBackoff(effBackoffRaw)
	if err != nil {
		return nil, err
	}

	opts := []smplkit.RetryPolicyOption{}

	switch {
	case in.maxDelaySecondsSet && in.maxDelaySeconds > 0:
		opts = append(opts, smplkit.WithRetryPolicyMaxDelaySeconds(in.maxDelaySeconds))
	case !in.maxDelaySecondsSet && shape != nil && shape.MaxDelaySeconds != nil:
		opts = append(opts, smplkit.WithRetryPolicyMaxDelaySeconds(*shape.MaxDelaySeconds))
	}

	retryOn, rerr := buildRetryOn(shape, in)
	if rerr != nil {
		return nil, rerr
	}
	if len(retryOn.Statuses) > 0 || len(retryOn.Reasons) > 0 {
		opts = append(opts, smplkit.WithRetryPolicyRetryOn(retryOn))
	}

	return ns.New(id, effName, effMaxRetries, backoff, effDelaySeconds, opts...), nil
}

// buildRetryOn assembles the RetryOn payload from the -f file plus the scalar
// flags. Scalar flags replace the file's lists entirely where supplied.
func buildRetryOn(shape *retryPolicyFileShape, in retryPolicyInputs) (smplkit.RetryOn, error) {
	out := smplkit.RetryOn{}
	if shape != nil && shape.RetryOn != nil {
		out.Statuses = append([]int(nil), shape.RetryOn.Statuses...)
		reasons, err := parseRetryReasons(shape.RetryOn.Reasons)
		if err != nil {
			return out, err
		}
		out.Reasons = reasons
	}
	if in.retryStatusesSet {
		out.Statuses = append([]int(nil), in.retryStatuses...)
	}
	if in.retryReasonsSet {
		reasons, err := parseRetryReasons(in.retryReasons)
		if err != nil {
			return out, err
		}
		out.Reasons = reasons
	}
	return out, nil
}

// applyRetryPolicyInputsToModel mutates an existing policy (fetched via Get) in
// place from the -f file and then the changed scalar flags, preserving every
// field the caller did not specify. This is what makes `apply` an idempotent,
// drift-reconciling upsert.
func applyRetryPolicyInputsToModel(policy *smplkit.RetryPolicy, shape *retryPolicyFileShape, in retryPolicyInputs) error {
	if shape != nil {
		if err := applyRetryPolicyFileToModel(policy, shape); err != nil {
			return err
		}
	}
	if in.nameSet {
		policy.Name = in.name
	}
	if in.maxRetriesSet {
		policy.MaxRetries = in.maxRetries
	}
	if in.backoffSet {
		backoff, err := parseBackoff(in.backoff)
		if err != nil {
			return err
		}
		policy.Backoff = backoff
	}
	if in.delaySecondsSet {
		policy.DelaySeconds = in.delaySeconds
	}
	if in.maxDelaySecondsSet {
		if in.maxDelaySeconds > 0 {
			v := in.maxDelaySeconds
			policy.MaxDelaySeconds = &v
		} else {
			// max-delay-seconds=0 clears the cap (uncapped exponential backoff).
			policy.MaxDelaySeconds = nil
		}
	}
	if in.retryStatusesSet {
		policy.RetryOn.Statuses = append([]int(nil), in.retryStatuses...)
	}
	if in.retryReasonsSet {
		reasons, err := parseRetryReasons(in.retryReasons)
		if err != nil {
			return err
		}
		policy.RetryOn.Reasons = reasons
	}
	return nil
}

// applyRetryPolicyFileToModel applies the set fields of a -f file onto an
// existing policy model, leaving unset fields intact.
func applyRetryPolicyFileToModel(policy *smplkit.RetryPolicy, shape *retryPolicyFileShape) error {
	if shape.Name != "" {
		policy.Name = shape.Name
	}
	if shape.MaxRetries != nil {
		policy.MaxRetries = *shape.MaxRetries
	}
	if shape.Backoff != "" {
		backoff, err := parseBackoff(shape.Backoff)
		if err != nil {
			return err
		}
		policy.Backoff = backoff
	}
	if shape.DelaySeconds != nil {
		policy.DelaySeconds = *shape.DelaySeconds
	}
	if shape.MaxDelaySeconds != nil {
		v := *shape.MaxDelaySeconds
		policy.MaxDelaySeconds = &v
	}
	if shape.RetryOn != nil {
		policy.RetryOn.Statuses = append([]int(nil), shape.RetryOn.Statuses...)
		reasons, err := parseRetryReasons(shape.RetryOn.Reasons)
		if err != nil {
			return err
		}
		policy.RetryOn.Reasons = reasons
	}
	return nil
}

func loadRetryPolicyFile(path string) (*retryPolicyFileShape, error) {
	data, err := readFileFlag(path)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}
	var shape retryPolicyFileShape
	if err := json.Unmarshal(data, &shape); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &shape, nil
}

// isRetryPolicyNotFound reports whether err is (or wraps) the SDK's 404
// NotFoundError. The apply command uses this to distinguish "policy does not
// exist yet → create it" from any other error (auth, network, 5xx), which must
// never be swallowed into a create.
func isRetryPolicyNotFound(err error) bool {
	var notFound *smplkit.NotFoundError
	return errors.As(err, &notFound)
}

// parseBackoff normalizes and validates a --backoff flag.
func parseBackoff(raw string) (smplkit.Backoff, error) {
	switch b := smplkit.Backoff(strings.ToLower(strings.TrimSpace(raw))); b {
	case smplkit.BackoffExponential,
		smplkit.BackoffFixed:
		return b, nil
	default:
		return "", fmt.Errorf("invalid --backoff %q (expected exponential|fixed)", raw)
	}
}

// parseRetryReason normalizes and validates a single --retry-reason value.
func parseRetryReason(raw string) (smplkit.RetryReason, error) {
	switch r := smplkit.RetryReason(strings.ToUpper(strings.TrimSpace(raw))); r {
	case smplkit.RetryReasonConnectionError,
		smplkit.RetryReasonNonSuccessStatus,
		smplkit.RetryReasonTimeout:
		return r, nil
	default:
		return "", fmt.Errorf("invalid --retry-reason %q (expected CONNECTION_ERROR|NON_SUCCESS_STATUS|TIMEOUT)", raw)
	}
}

// parseRetryReasons validates a list of --retry-reason values. A nil/empty
// input yields a nil slice (retries nothing).
func parseRetryReasons(raws []string) ([]smplkit.RetryReason, error) {
	if len(raws) == 0 {
		return nil, nil
	}
	out := make([]smplkit.RetryReason, 0, len(raws))
	for _, raw := range raws {
		r, err := parseRetryReason(raw)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}
