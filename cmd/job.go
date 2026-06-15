package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	smplkit "github.com/smplkit/go-sdk/v3"
	"github.com/spf13/cobra"

	"github.com/smplkit/cli/internal/output"
)

// jobFileShape mirrors output.JobAttr but with omittable fields so a
// `get -o json | smplkit job apply -f -` round-trip is clean. Reused for
// both create and apply.
//
// `type` is intentionally absent: the jobs service supports only "http"
// (the SDK hardcodes it) and the field is immutable, so a round-trip's
// `type` key is ignored rather than silently no-op'd.
type jobFileShape struct {
	ID                string                    `json:"id,omitempty"`
	Name              string                    `json:"name,omitempty"`
	Description       *string                   `json:"description,omitempty"`
	Enabled           *bool                     `json:"enabled,omitempty"`
	Schedule          string                    `json:"schedule,omitempty"`
	ConcurrencyPolicy string                    `json:"concurrency_policy,omitempty"`
	Configuration     *output.JobHTTPConfigAttr `json:"configuration,omitempty"`
}

func registerJobCmd(root *cobra.Command) {
	job := &cobra.Command{
		Use:   "job",
		Short: "Manage scheduled jobs",
	}

	job.AddCommand(jobListCmd())
	job.AddCommand(jobGetCmd())
	job.AddCommand(jobCreateCmd())
	job.AddCommand(jobApplyCmd())
	job.AddCommand(jobDeleteCmd())
	job.AddCommand(jobRunCmd())

	root.AddCommand(job)
}

func jobListCmd() *cobra.Command {
	var (
		limit int
		all   bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List jobs",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := withClient()
			if err != nil {
				return err
			}
			ctx := cliContext(cmd)
			ns := client.Jobs()
			var jobs []*smplkit.Job
			if all {
				jobs, err = fetchAllJobs(ctx, ns, limit)
			} else {
				input := smplkit.ListJobsInput{}
				if limit > 0 {
					input.PageSize = limit
				}
				jobs, err = ns.List(ctx, input)
			}
			if err != nil {
				return err
			}
			return renderer(cmd).RenderJobs(jobs)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 0, "page size (server default when unset)")
	cmd.Flags().BoolVar(&all, "all", false, "fetch every page")
	return cmd
}

// fetchAllJobs walks every page of the jobs collection. The jobs List
// surface takes a ListJobsInput (offset pagination) rather than the
// variadic ListOption fetcher paginate.All expects, so it gets its own
// small page-walker — mirrors fetchAllForwarders.
func fetchAllJobs(ctx context.Context, ns *smplkit.JobsClient, limit int) ([]*smplkit.Job, error) {
	pageSize := 1000
	if limit > 0 && limit < pageSize {
		pageSize = limit
	}
	var out []*smplkit.Job
	for page := 1; ; page++ {
		jobs, err := ns.List(ctx, smplkit.ListJobsInput{PageNumber: page, PageSize: pageSize})
		if err != nil {
			return nil, err
		}
		out = append(out, jobs...)
		if len(jobs) < pageSize {
			return out, nil
		}
	}
}

func jobGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Get a job by id",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := withClient()
			if err != nil {
				return err
			}
			j, err := client.Jobs().Get(cliContext(cmd), args[0])
			if err != nil {
				return err
			}
			return renderer(cmd).RenderJob(j)
		},
	}
}

func jobCreateCmd() *cobra.Command {
	var in jobInputs
	cmd := &cobra.Command{
		Use:   "create <id>",
		Short: "Create a job",
		Long: "Create a scheduled job. Supply --schedule and --url (plus optional --method/\n" +
			"--header/--body) for the simple case, or -f job.json (produced by\n" +
			"`smplkit job get -o json`) to round-trip a full definition. Scalar flags\n" +
			"override file values where both are supplied. Fields without a scalar flag\n" +
			"(enabled, concurrency policy, success status, timeout, TLS) come from -f.\n" +
			"--header sets the complete header set; supply every header you want.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			client, err := withClient()
			if err != nil {
				return err
			}
			ns := client.Jobs()

			shape, err := loadJobFile(in.file)
			if err != nil {
				return err
			}
			in.readChanged(cmd)

			job, err := buildJobForCreate(ns, id, shape, in)
			if err != nil {
				return err
			}
			if err := job.Save(cliContext(cmd)); err != nil {
				return err
			}
			return renderer(cmd).RenderJob(job)
		},
	}
	addJobScalarFlags(cmd, &in)
	return cmd
}

func jobApplyCmd() *cobra.Command {
	var in jobInputs
	cmd := &cobra.Command{
		Use:   "apply <id>",
		Short: "Create or update a job (idempotent upsert)",
		Long: "Reconciles a job to the supplied definition: GET the job; if it exists,\n" +
			"apply -f file then the changed scalar flags (every field you don't pass is\n" +
			"preserved from the server) and PUT it back; if it does not exist, create it.\n" +
			"Safe to run repeatedly — re-running with the same flags is a no-op, and\n" +
			"changing a flag reconciles drift. Note: --header sets the COMPLETE header\n" +
			"set, so re-supply every header you want to keep (rotating one of several\n" +
			"headers means passing them all). Built for scheduled CI keeping a job in a\n" +
			"desired state.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			client, err := withClient()
			if err != nil {
				return err
			}
			ns := client.Jobs()
			ctx := cliContext(cmd)

			shape, err := loadJobFile(in.file)
			if err != nil {
				return err
			}
			in.readChanged(cmd)

			existing, gerr := ns.Get(ctx, id)
			if gerr != nil {
				if !isJobNotFound(gerr) {
					// A real error (auth, network, 5xx) — never silently create.
					return gerr
				}
				// Not found → create.
				job, berr := buildJobForCreate(ns, id, shape, in)
				if berr != nil {
					return berr
				}
				if err := job.Save(ctx); err != nil {
					return err
				}
				return renderer(cmd).RenderJob(job)
			}

			// Found → mutate in place and full-replace.
			if err := applyJobInputsToModel(existing, shape, in); err != nil {
				return err
			}
			if err := existing.Save(ctx); err != nil {
				return err
			}
			return renderer(cmd).RenderJob(existing)
		},
	}
	addJobScalarFlags(cmd, &in)
	return cmd
}

func jobDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a job",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if !confirm(cmd, yes, fmt.Sprintf("Delete job %q?", id)) {
				return fmt.Errorf("aborted")
			}
			client, err := withClient()
			if err != nil {
				return err
			}
			if err := client.Jobs().Delete(cliContext(cmd), id); err != nil {
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

func jobRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run <id>",
		Short: "Trigger an immediate run of a job",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := withClient()
			if err != nil {
				return err
			}
			run, err := client.Jobs().Run(cliContext(cmd), args[0])
			if err != nil {
				return err
			}
			return renderer(cmd).RenderRun(run)
		},
	}
}

// jobInputs bundles the scalar create/apply flags plus a record of which
// were explicitly set, so the apply (read-modify-write) path leaves
// unspecified fields untouched and create can fall back to the -f file.
type jobInputs struct {
	name     string
	schedule string
	url      string
	method   string
	body     string
	headers  []string
	file     string

	nameSet     bool
	scheduleSet bool
	urlSet      bool
	methodSet   bool
	bodySet     bool
	headerSet   bool
}

func addJobScalarFlags(cmd *cobra.Command, in *jobInputs) {
	cmd.Flags().StringVar(&in.name, "name", "", "display name (defaults to the id)")
	cmd.Flags().StringVar(&in.schedule, "schedule", "", "5-field UTC cron, an ISO-8601 datetime, or \"now\"")
	cmd.Flags().StringVar(&in.url, "url", "", "absolute http(s) URL the job calls when it fires")
	cmd.Flags().StringVar(&in.method, "method", "POST", "HTTP method: GET|POST|PUT|PATCH|DELETE")
	cmd.Flags().StringArrayVar(&in.headers, "header", nil, "HTTP header (repeatable): --header \"Name: Value\" (replaces the full header set)")
	cmd.Flags().StringVar(&in.body, "body", "", "request body sent on each run")
	cmd.Flags().StringVarP(&in.file, "file", "f", "", "load definition from JSON file (round-trips `get -o json`)")
}

// readChanged records which scalar flags the user actually supplied. Must
// be called after flag parsing (inside RunE).
func (in *jobInputs) readChanged(cmd *cobra.Command) {
	in.nameSet = cmd.Flags().Changed("name")
	in.scheduleSet = cmd.Flags().Changed("schedule")
	in.urlSet = cmd.Flags().Changed("url")
	in.methodSet = cmd.Flags().Changed("method")
	in.bodySet = cmd.Flags().Changed("body")
	in.headerSet = cmd.Flags().Changed("header")
}

// buildJobForCreate assembles a fresh, unsaved *Job from the -f file and
// the scalar flags (flags win). Used by `create` and by `apply` when the
// job does not yet exist.
func buildJobForCreate(ns *smplkit.JobsClient, id string, shape *jobFileShape, in jobInputs) (*smplkit.Job, error) {
	effName := id
	switch {
	case in.nameSet && in.name != "":
		effName = in.name
	case shape != nil && shape.Name != "":
		effName = shape.Name
	}

	effSchedule := ""
	switch {
	case in.scheduleSet:
		effSchedule = in.schedule
	case shape != nil && shape.Schedule != "":
		effSchedule = shape.Schedule
	}
	if effSchedule == "" {
		return nil, fmt.Errorf("missing --schedule (or `schedule` in -f file)")
	}

	cfg, err := buildJobHTTPConfig(shape, in)
	if err != nil {
		return nil, err
	}

	opts := []smplkit.JobOption{}
	if shape != nil {
		if shape.Enabled != nil {
			opts = append(opts, smplkit.WithJobEnabled(*shape.Enabled))
		}
		if shape.Description != nil {
			opts = append(opts, smplkit.WithJobDescription(*shape.Description))
		}
		if shape.ConcurrencyPolicy != "" {
			opts = append(opts, smplkit.WithJobConcurrencyPolicy(shape.ConcurrencyPolicy))
		}
	}

	return ns.New(id, effName, effSchedule, cfg, opts...), nil
}

// buildJobHTTPConfig assembles the HTTP request configuration from the -f
// file plus the scalar flags. Scalar flags override file values; --method
// falls back to the file's method, then to POST. --url is required.
func buildJobHTTPConfig(shape *jobFileShape, in jobInputs) (smplkit.HttpConfig, error) {
	cfg := smplkit.HttpConfig{}
	if shape != nil && shape.Configuration != nil {
		c := shape.Configuration
		cfg.URL = c.URL
		cfg.Method = smplkit.JobHttpMethod(c.Method)
		cfg.SuccessStatus = c.SuccessStatus
		cfg.Timeout = c.Timeout
		cfg.Body = c.Body
		cfg.TlsVerify = c.TLSVerify
		cfg.CaCert = c.CACert
		for _, h := range c.Headers {
			cfg.Headers = append(cfg.Headers, smplkit.HttpHeader{Name: h.Name, Value: h.Value})
		}
	}

	if in.urlSet {
		cfg.URL = in.url
	}
	if cfg.URL == "" {
		return cfg, fmt.Errorf("missing --url (or `configuration.url` in -f file)")
	}

	if in.methodSet {
		m, err := parseJobMethod(in.method)
		if err != nil {
			return cfg, err
		}
		cfg.Method = m
	}
	if cfg.Method == "" {
		cfg.Method = smplkit.JobHttpMethodPost
	}

	if in.headerSet {
		hdrs, err := parseJobHeaders(in.headers)
		if err != nil {
			return cfg, err
		}
		// Scalar headers replace any file-provided ones — predictable for
		// one-off credentials supplied on the command line.
		cfg.Headers = hdrs
	}
	if in.bodySet {
		b := in.body
		cfg.Body = &b
	}
	return cfg, nil
}

// applyJobInputsToModel mutates an existing job (fetched via Get) in place
// from the -f file and then the changed scalar flags, preserving every
// field the caller did not specify. This is what makes `apply` an
// idempotent, drift-reconciling upsert.
func applyJobInputsToModel(job *smplkit.Job, shape *jobFileShape, in jobInputs) error {
	if shape != nil {
		applyJobFileToModel(job, shape)
	}
	if in.nameSet {
		job.Name = in.name
	}
	if in.scheduleSet {
		job.Schedule = in.schedule
	}
	if in.urlSet {
		job.Configuration.URL = in.url
	}
	if in.methodSet {
		m, err := parseJobMethod(in.method)
		if err != nil {
			return err
		}
		job.Configuration.Method = m
	}
	if in.headerSet {
		hdrs, err := parseJobHeaders(in.headers)
		if err != nil {
			return err
		}
		job.Configuration.Headers = hdrs
	}
	if in.bodySet {
		b := in.body
		job.Configuration.Body = &b
	}
	return nil
}

// applyJobFileToModel applies the set fields of a -f file onto an existing
// job model, leaving unset fields intact.
func applyJobFileToModel(job *smplkit.Job, shape *jobFileShape) {
	if shape.Name != "" {
		job.Name = shape.Name
	}
	if shape.Description != nil {
		job.Description = shape.Description
	}
	if shape.Enabled != nil {
		job.Enabled = *shape.Enabled
	}
	if shape.Schedule != "" {
		job.Schedule = shape.Schedule
	}
	if shape.ConcurrencyPolicy != "" {
		job.ConcurrencyPolicy = shape.ConcurrencyPolicy
	}
	if shape.Configuration != nil {
		c := shape.Configuration
		if c.URL != "" {
			job.Configuration.URL = c.URL
		}
		if c.Method != "" {
			job.Configuration.Method = smplkit.JobHttpMethod(c.Method)
		}
		if c.SuccessStatus != "" {
			job.Configuration.SuccessStatus = c.SuccessStatus
		}
		if c.Timeout != 0 {
			job.Configuration.Timeout = c.Timeout
		}
		if c.Body != nil {
			job.Configuration.Body = c.Body
		}
		if c.TLSVerify != nil {
			job.Configuration.TlsVerify = c.TLSVerify
		}
		if c.CACert != nil {
			job.Configuration.CaCert = c.CACert
		}
		if c.Headers != nil {
			job.Configuration.Headers = nil
			for _, h := range c.Headers {
				job.Configuration.Headers = append(job.Configuration.Headers,
					smplkit.HttpHeader{Name: h.Name, Value: h.Value})
			}
		}
	}
}

func loadJobFile(path string) (*jobFileShape, error) {
	data, err := readFileFlag(path)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}
	var shape jobFileShape
	if err := json.Unmarshal(data, &shape); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &shape, nil
}

// parseJobHeaders parses repeatable "Name: Value" header flags into the
// SDK's header slice, splitting on the first colon so values may contain
// colons (e.g. "Authorization: Bearer abc:def").
func parseJobHeaders(raws []string) ([]smplkit.HttpHeader, error) {
	out := make([]smplkit.HttpHeader, 0, len(raws))
	for _, raw := range raws {
		colon := strings.Index(raw, ":")
		if colon == -1 {
			return nil, fmt.Errorf("--header must be \"Name: Value\", got %q", raw)
		}
		name := strings.TrimSpace(raw[:colon])
		value := strings.TrimSpace(raw[colon+1:])
		if name == "" {
			return nil, fmt.Errorf("--header has an empty name: %q", raw)
		}
		out = append(out, smplkit.HttpHeader{Name: name, Value: value})
	}
	return out, nil
}

// isJobNotFound reports whether err is (or wraps) the SDK's 404
// NotFoundError. The apply command uses this to distinguish "job does not
// exist yet → create it" from any other error (auth, network, 5xx), which
// must never be swallowed into a create.
func isJobNotFound(err error) bool {
	var notFound *smplkit.NotFoundError
	return errors.As(err, &notFound)
}

// parseJobMethod normalizes and validates an HTTP method flag.
func parseJobMethod(raw string) (smplkit.JobHttpMethod, error) {
	switch m := smplkit.JobHttpMethod(strings.ToUpper(strings.TrimSpace(raw))); m {
	case smplkit.JobHttpMethodGet,
		smplkit.JobHttpMethodPost,
		smplkit.JobHttpMethodPut,
		smplkit.JobHttpMethodPatch,
		smplkit.JobHttpMethodDelete:
		return m, nil
	default:
		return "", fmt.Errorf("invalid --method %q (expected GET|POST|PUT|PATCH|DELETE)", raw)
	}
}
