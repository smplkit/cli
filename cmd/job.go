package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

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
// `type` key is ignored rather than silently no-op'd. The base `enabled`
// is likewise absent: enablement is per-environment (the base `enabled`
// is a read-only roll-up the server derives), so it lives in the
// `environments` map. A `get -o json` snapshot still carries the rendered
// roll-up `enabled`, which is ignored here on replay.
type jobFileShape struct {
	ID                string                     `json:"id,omitempty"`
	Name              string                     `json:"name,omitempty"`
	Description       *string                    `json:"description,omitempty"`
	Schedule          string                     `json:"schedule,omitempty"`
	Timezone          string                     `json:"timezone,omitempty"`
	ConcurrencyPolicy string                     `json:"concurrency_policy,omitempty"`
	Environments      map[string]jobEnvFileShape `json:"environments,omitempty"`
	Configuration     *output.JobHTTPConfigAttr  `json:"configuration,omitempty"`
}

// jobEnvFileShape is the per-environment override carried in the `-f`
// file: whether the job is enabled in that environment and an optional
// configuration that fully replaces the base configuration there (a nil
// Configuration inherits the base).
type jobEnvFileShape struct {
	Enabled bool `json:"enabled"`
	// Schedule is an optional per-environment cron override (recurring jobs
	// only); empty inherits the job's base schedule.
	Schedule string `json:"schedule,omitempty"`
	// Timezone is an optional per-environment IANA timezone override (recurring
	// jobs only); empty inherits the job's base timezone.
	Timezone      string                    `json:"timezone,omitempty"`
	Configuration *output.JobHTTPConfigAttr `json:"configuration,omitempty"`
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
	job.AddCommand(jobUsageCmd())

	runs := &cobra.Command{
		Use:   "runs",
		Short: "Inspect and act on job runs",
	}
	runs.AddCommand(jobRunsListCmd())
	runs.AddCommand(jobRunsGetCmd())
	runs.AddCommand(jobRunsCancelCmd())
	runs.AddCommand(jobRunsRerunCmd())
	job.AddCommand(runs)

	root.AddCommand(job)
}

func jobListCmd() *cobra.Command {
	var (
		limit     int
		all       bool
		kind      string
		scheduled bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List jobs",
		Long: "List jobs. Filter by kind with --kind recurring|manual|one_off. Filter\n" +
			"to jobs with (or without) an upcoming fire in some environment using\n" +
			"--scheduled / --scheduled=false. With no --kind, recurring and manual\n" +
			"jobs are returned; one-off jobs are omitted unless you pass\n" +
			"--kind one_off.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			input := smplkit.ListJobsInput{}
			if kind != "" {
				k, err := parseJobKind(kind)
				if err != nil {
					return err
				}
				input.Kind = &k
			}
			if cmd.Flags().Changed("scheduled") {
				input.Scheduled = &scheduled
			}
			client, err := withClient()
			if err != nil {
				return err
			}
			ctx := cliContext(cmd)
			ns := client.Jobs()
			var jobs []*smplkit.Job
			if all {
				jobs, err = fetchAllJobs(ctx, ns, input, limit)
			} else {
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
	cmd.Flags().StringVar(&kind, "kind", "", "filter by kind: recurring|manual|one_off")
	cmd.Flags().BoolVar(&scheduled, "scheduled", false, "only jobs with (or, with =false, without) an upcoming fire")
	return cmd
}

// parseJobKind validates a --kind value and maps it to the SDK enum.
func parseJobKind(raw string) (smplkit.JobKind, error) {
	switch k := smplkit.JobKind(raw); k {
	case smplkit.JobKindRecurring,
		smplkit.JobKindManual,
		smplkit.JobKindOneOff:
		return k, nil
	default:
		return "", fmt.Errorf("invalid --kind %q (expected recurring|manual|one_off)", raw)
	}
}

// fetchAllJobs walks every page of the jobs collection, preserving the
// caller's filters (input.Kind / input.Scheduled). The jobs List
// surface takes a ListJobsInput (offset pagination) rather than the
// variadic ListOption fetcher paginate.All expects, so it gets its own
// small page-walker — mirrors fetchAllForwarders.
func fetchAllJobs(ctx context.Context, ns *smplkit.JobsClient, input smplkit.ListJobsInput, limit int) ([]*smplkit.Job, error) {
	pageSize := 1000
	if limit > 0 && limit < pageSize {
		pageSize = limit
	}
	var out []*smplkit.Job
	for page := 1; ; page++ {
		input.PageNumber = page
		input.PageSize = pageSize
		jobs, err := ns.List(ctx, input)
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
			"(concurrency policy, success status, timeout, TLS) come from -f.\n" +
			"--header sets the complete header set; supply every header you want.\n\n" +
			"Enablement is per-environment: a recurring (cron) job fires only in\n" +
			"environments turned on via --enable-env <env> (repeatable) or the file's\n" +
			"`environments` map. A one-off (datetime / \"now\") job runs once in the\n" +
			"single environment named by --env.",
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
			"changing a flag reconciles drift. Enablement reconciles per environment\n" +
			"via --enable-env / --disable-env (each repeatable), leaving the enablement\n" +
			"of environments you don't name untouched. Note: --header sets the COMPLETE\n" +
			"header set, so re-supply every header you want to keep (rotating one of\n" +
			"several headers means passing them all). Built for scheduled CI keeping a\n" +
			"job in a desired state.",
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
	var env string
	cmd := &cobra.Command{
		Use:   "run <id>",
		Short: "Trigger an immediate run of a job",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := withClient()
			if err != nil {
				return err
			}
			run, err := client.Jobs().Run(cliContext(cmd), args[0], env)
			if err != nil {
				return err
			}
			return renderer(cmd).RenderRun(run)
		},
	}
	cmd.Flags().StringVar(&env, "env", "", "environment the manual run executes in (defaults to the job's environment when unambiguous)")
	return cmd
}

func jobUsageCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "usage",
		Short: "Show current-period jobs usage for the account",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := withClient()
			if err != nil {
				return err
			}
			usage, err := client.Jobs().Usage(cliContext(cmd))
			if err != nil {
				return err
			}
			return renderer(cmd).RenderUsage(usage)
		},
	}
}

// jobRunsListCmd lists run history. The SDK's Runs().List is cursor
// paginated but the wrapper does not surface the next cursor, so there is
// no --all: pass --after with the last id from the previous page to walk
// forward manually.
func jobRunsListCmd() *cobra.Command {
	var (
		job   string
		env   string
		limit int
		after string
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List job runs (newest first)",
		Long: "List run history, newest first, across all jobs or scoped to one with\n" +
			"--job. Restrict to a single environment with --env (omit to cover every\n" +
			"environment you can access). Cursor paginated: pass --limit for the page\n" +
			"size and --after with the last run id from the previous page to fetch the\n" +
			"next page.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := withClient()
			if err != nil {
				return err
			}
			input := smplkit.ListRunsInput{Job: job, After: after}
			if env != "" {
				input.Environments = []string{env}
			}
			if limit > 0 {
				input.PageSize = limit
			}
			runs, err := client.Jobs().Runs().List(cliContext(cmd), input)
			if err != nil {
				return err
			}
			return renderer(cmd).RenderRuns(runs)
		},
	}
	cmd.Flags().StringVar(&job, "job", "", "scope to a single job's run history")
	cmd.Flags().StringVar(&env, "env", "", "scope to runs stamped with this environment")
	cmd.Flags().IntVar(&limit, "limit", 0, "page size (server default when unset)")
	cmd.Flags().StringVar(&after, "after", "", "cursor: the last run id from the previous page")
	return cmd
}

func jobRunsGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <run-id>",
		Short: "Get a job run by id",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := withClient()
			if err != nil {
				return err
			}
			run, err := client.Jobs().Runs().Get(cliContext(cmd), args[0])
			if err != nil {
				return err
			}
			return renderer(cmd).RenderRun(run)
		},
	}
}

func jobRunsCancelCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cancel <run-id>",
		Short: "Cancel a pending job run",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := withClient()
			if err != nil {
				return err
			}
			run, err := client.Jobs().Runs().Cancel(cliContext(cmd), args[0])
			if err != nil {
				return err
			}
			return renderer(cmd).RenderRun(run)
		},
	}
}

func jobRunsRerunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rerun <run-id>",
		Short: "Re-run a prior run (spawns a new RERUN run)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := withClient()
			if err != nil {
				return err
			}
			run, err := client.Jobs().Runs().Rerun(cliContext(cmd), args[0])
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
	timezone string
	url      string
	method   string
	body     string
	headers  []string
	file     string

	// enableEnvs / disableEnvs turn the job on/off in named environments
	// (the per-environment enablement that replaced the old base flag). env
	// is the single environment a one-off (datetime / "now") job is born in.
	enableEnvs  []string
	disableEnvs []string
	env         string

	nameSet     bool
	scheduleSet bool
	timezoneSet bool
	urlSet      bool
	methodSet   bool
	bodySet     bool
	headerSet   bool
	envSet      bool
}

func addJobScalarFlags(cmd *cobra.Command, in *jobInputs) {
	cmd.Flags().StringVar(&in.name, "name", "", "display name (defaults to the id)")
	cmd.Flags().StringVar(&in.schedule, "schedule", "", "5-field UTC cron, an ISO-8601 datetime, or \"now\"")
	cmd.Flags().StringVar(&in.timezone, "timezone", "", "IANA timezone the cron is evaluated in (recurring jobs only); empty = UTC")
	cmd.Flags().StringVar(&in.url, "url", "", "absolute http(s) URL the job calls when it fires")
	cmd.Flags().StringVar(&in.method, "method", "POST", "HTTP method: GET|POST|PUT|PATCH|DELETE")
	cmd.Flags().StringArrayVar(&in.headers, "header", nil, "HTTP header (repeatable): --header \"Name: Value\" (replaces the full header set)")
	cmd.Flags().StringVar(&in.body, "body", "", "request body sent on each run")
	cmd.Flags().StringSliceVar(&in.enableEnvs, "enable-env", nil, "enable a recurring job in an environment (repeatable)")
	cmd.Flags().StringSliceVar(&in.disableEnvs, "disable-env", nil, "disable a recurring job in an environment (repeatable)")
	cmd.Flags().StringVar(&in.env, "env", "", "environment a one-off (datetime/\"now\") job is created in")
	cmd.Flags().StringVarP(&in.file, "file", "f", "", "load definition from JSON file (round-trips `get -o json`)")
}

// readChanged records which scalar flags the user actually supplied. Must
// be called after flag parsing (inside RunE).
func (in *jobInputs) readChanged(cmd *cobra.Command) {
	in.nameSet = cmd.Flags().Changed("name")
	in.scheduleSet = cmd.Flags().Changed("schedule")
	in.timezoneSet = cmd.Flags().Changed("timezone")
	in.urlSet = cmd.Flags().Changed("url")
	in.methodSet = cmd.Flags().Changed("method")
	in.bodySet = cmd.Flags().Changed("body")
	in.headerSet = cmd.Flags().Changed("header")
	in.envSet = cmd.Flags().Changed("env")
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

	effTimezone := ""
	switch {
	case in.timezoneSet:
		effTimezone = in.timezone
	case shape != nil && shape.Timezone != "":
		effTimezone = shape.Timezone
	}

	cfg, err := buildJobHTTPConfig(shape, in)
	if err != nil {
		return nil, err
	}

	opts := []smplkit.JobOption{}
	envs, eerr := buildJobEnvironments(shape, in)
	if eerr != nil {
		return nil, eerr
	}
	if len(envs) > 0 {
		opts = append(opts, smplkit.WithJobEnvironments(envs))
	}
	if in.envSet {
		opts = append(opts, smplkit.WithJobBirthEnvironment(in.env))
	}
	if shape != nil {
		if shape.Description != nil {
			opts = append(opts, smplkit.WithJobDescription(*shape.Description))
		}
		if shape.ConcurrencyPolicy != "" {
			opts = append(opts, smplkit.WithJobConcurrencyPolicy(shape.ConcurrencyPolicy))
		}
	}

	job := newJobForSchedule(ns, id, effName, effSchedule, cfg, opts...)
	if effTimezone != "" {
		job.SetTimezone(effTimezone)
	}
	return job, nil
}

// newJobForSchedule classifies effSchedule and picks the matching SDK
// constructor, mirroring the jobs service's kind derivation:
//   - "" (no schedule) → a manual job (never auto-fires; runs only on trigger).
//   - "now" → a one-off scheduled to fire immediately.
//   - an RFC-3339 datetime → a one-off scheduled to fire at that instant.
//   - anything else → a recurring job whose schedule is a cron expression.
func newJobForSchedule(ns *smplkit.JobsClient, id, name, schedule string, cfg smplkit.HttpConfig, opts ...smplkit.JobOption) *smplkit.Job {
	switch {
	case schedule == "":
		return ns.NewManualJob(id, name, cfg, opts...)
	case strings.EqualFold(schedule, "now"):
		return ns.Schedule(id, name, time.Now().UTC(), cfg, opts...)
	default:
		if when, err := time.Parse(time.RFC3339, schedule); err == nil {
			return ns.Schedule(id, name, when, cfg, opts...)
		}
		return ns.NewRecurringJob(id, name, schedule, cfg, opts...)
	}
}

// jobEnvShapeToConfig converts the file/CLI HTTP-config attribute shape
// into the SDK's HttpConfig used for per-environment overrides.
func jobEnvShapeToConfig(c *output.JobHTTPConfigAttr) *smplkit.HttpConfig {
	if c == nil {
		return nil
	}
	cfg := smplkit.HttpConfig{
		URL:           c.URL,
		Method:        smplkit.JobHttpMethod(c.Method),
		SuccessStatus: c.SuccessStatus,
		Timeout:       c.Timeout,
		Body:          c.Body,
		TlsVerify:     c.TLSVerify,
		CaCert:        c.CACert,
	}
	for _, h := range c.Headers {
		cfg.Headers = append(cfg.Headers, smplkit.HttpHeader{Name: h.Name, Value: h.Value})
	}
	return &cfg
}

// jobEnvFileToModel converts the file shape's environments map into the
// SDK's per-environment override map.
func jobEnvFileToModel(envs map[string]jobEnvFileShape) map[string]smplkit.JobEnvironment {
	if envs == nil {
		return nil
	}
	out := make(map[string]smplkit.JobEnvironment, len(envs))
	for k, e := range envs {
		out[k] = smplkit.JobEnvironment{
			Enabled:       e.Enabled,
			Schedule:      e.Schedule,
			Timezone:      e.Timezone,
			Configuration: jobEnvShapeToConfig(e.Configuration),
		}
	}
	return out
}

// buildJobEnvironments assembles the per-environment override map for a
// create from the -f file's `environments` plus the --enable-env /
// --disable-env scalar flags (flags win where they overlap the file).
func buildJobEnvironments(shape *jobFileShape, in jobInputs) (map[string]smplkit.JobEnvironment, error) {
	var envs map[string]smplkit.JobEnvironment
	if shape != nil && shape.Environments != nil {
		envs = jobEnvFileToModel(shape.Environments)
	}
	if len(in.enableEnvs) == 0 && len(in.disableEnvs) == 0 {
		return envs, nil
	}
	if envs == nil {
		envs = make(map[string]smplkit.JobEnvironment)
	}
	tmp := &smplkit.Job{Environments: envs}
	if err := applyJobEnvToggles(tmp, in.enableEnvs, in.disableEnvs); err != nil {
		return nil, err
	}
	return tmp.Environments, nil
}

// applyJobEnvToggles flips the enabled bit for the named environments on a
// job's Environments map, preserving any existing per-environment
// configuration override. --enable-env and --disable-env may not name the
// same environment.
func applyJobEnvToggles(job *smplkit.Job, enableEnvs, disableEnvs []string) error {
	if len(enableEnvs) == 0 && len(disableEnvs) == 0 {
		return nil
	}
	for _, e := range enableEnvs {
		for _, d := range disableEnvs {
			if e == d {
				return fmt.Errorf("environment %q given to both --enable-env and --disable-env", e)
			}
		}
	}
	if job.Environments == nil {
		job.Environments = make(map[string]smplkit.JobEnvironment)
	}
	setEnabled := func(env string, enabled bool) {
		cur := job.Environments[env]
		cur.Enabled = enabled
		job.Environments[env] = cur
	}
	for _, env := range enableEnvs {
		setEnabled(env, true)
	}
	for _, env := range disableEnvs {
		setEnabled(env, false)
	}
	return nil
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
	if err := applyJobEnvToggles(job, in.enableEnvs, in.disableEnvs); err != nil {
		return err
	}
	if in.nameSet {
		job.Name = in.name
	}
	if in.scheduleSet {
		job.Schedule = in.schedule
	}
	if in.timezoneSet {
		job.Timezone = in.timezone
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
	if shape.Environments != nil {
		job.Environments = jobEnvFileToModel(shape.Environments)
	}
	if shape.Schedule != "" {
		job.Schedule = shape.Schedule
	}
	if shape.Timezone != "" {
		job.Timezone = shape.Timezone
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
