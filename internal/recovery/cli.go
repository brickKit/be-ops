package recovery

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/brickKit/be-sdk-go/v3/events/jetstream"
	"github.com/brickKit/be-sdk-go/v3/identity"
	"github.com/brickKit/be-sdk-go/v3/runprofile"
	"github.com/jackc/pgx/v5"
)

// Identity is the tool's own service identity: its certificate signs the run profile's records (P12.2) and it is the
// evidence store's reader (infra/audit's AUDIT_EVIDENCE_READERS default).
const Identity = "ops/recover"

// Usage is be-ops recover's help.
const Usage = `be-ops recover <step> [flags]   (assembly protocol 15, steps 1-10)

  status                         the deployment's recovery state, from the run profile
  announce --reason R --approver A --at T
                                 step 1: T (RFC 3339) is the point in time the databases go back to
  abandon                        give a frozen deployment up (before any database went back)
  freeze [--approver A --reason R]
                                 step 2: every member restarts frozen (RECOVERY_PHASE) and reports it; again while
                                 verifying or open for reading (a difference found: who allows it and why), the
                                 steps from 7 on are then done again
  restore-db [--temp N --port P] step 3: the instance at T into a temporary one, every schema copied back but the
                                 evidence store's
  advance                        step 4: the deployment's and the authorization's recovery generations
  redeliver                      step 5: the streams deleted, the members restart redelivering, every consumer asks
                                 its producers from its progress; waits for them and lists the gaps
  replay                         step 6, 6.2-6.6: every member restarts replaying (the identity member ends the
                                 sessions from before); the alert channel's intents verified; the evidence replayed
                                 by its owners in order, the authorization member last; its sources waited for, its
                                 deferred revocations, its invariant check; the list into the run profile. The calls
                                 run in a container on the deployment's network (` + ReplayImage + `, this binary mounted:
                                 build it with CGO_ENABLED=0)
  keys                           step 6, 6.8: new signing keys (current and standby) into the identity member's key
                                 files, the old ones kept beside them
  reconcile [--window D --failed-at T]
                                 step 7: every member restarts verifying (external commands query only); each
                                 adapter matches the external records from T minus the window (the declared clock skew
                                 plus the external posting delay, default 1h) with its open commands and the issue
                                 records after T; --failed-at is when sends after T may have gone on unrecorded until
  check                          step 8: every member with a database answers its checks
  loss [--failed-at T]           step 9: the audit store's business changes and sensitive reads after T asked of their
                                 owners (lost, older, cannot tell), the audit loss, what must not be entered again
  open-read --approver A [--reason R]
                                 step 10: every member restarts read-only; checks that did not pass need a reason
                                 (the approver confirms the key sets refreshed and an authorization admin can log in)
  open --approver A              step 10: the continuous number series raised past the audit's numbers after T; every
                                 member restarts running; the recovery is over
  backup-record --finished-at T --backup ID
                                 not a step: the backup tool's record of a successful backup in the run profile (kind
                                 backup); archive units release their online rows only after one (be-protocol P15.21)
  Steps 6-10 run their calls in a container on the deployment's network (` + ReplayImage + `, this binary mounted:
  build it with CGO_ENABLED=0).

  --project DIR (default .) is the BrickKit project; DEPLOYMENT_ID comes from its config/vars.yaml.
  Environment: RUN_PROFILE_URL, RUN_PROFILE_BUCKET, RUN_PROFILE_REGION, RUN_PROFILE_ACCESS_KEY, RUN_PROFILE_SECRET_KEY
  (the run profile); SERVICE_TLS_CERT_FILE, SERVICE_TLS_KEY_FILE, SERVICE_TLS_CA_FILE (the ops/recover certificate of the
  deployment); RECOVER_PG_ADMIN (an administrator's DSN of the instance); BUS_URL (nats://, step 5).`

// Main runs one step.
func Main(args []string, out io.Writer) error {
	if len(args) == 0 {
		fmt.Fprintln(out, Usage)
		return errors.New("which step?")
	}
	step := args[0]
	switch step { // inside the deployment's network (Tool.runCalls): no project, no run profile
	case "replay-calls":
		return replayCalls(args[1:])
	case "verify-calls":
		return verifyCalls(args[1:])
	}
	fs := flag.NewFlagSet("recover "+step, flag.ContinueOnError)
	project := fs.String("project", ".", "the BrickKit project")
	reason := fs.String("reason", "", "why (announce)")
	approver := fs.String("approver", "", "who decided (announce)")
	at := fs.String("at", "", "T, RFC 3339 (announce)")
	temp := fs.String("temp", "be-recover-temp", "the temporary instance's container (restore-db)")
	port := fs.Int("port", 15439, "the temporary instance's host port (restore-db)")
	instance := fs.String("instance", "be-postgres", "the database instance's container")
	wait := fs.Duration("wait", 5*time.Minute, "how long to wait for the members and the consumers")
	window := fs.Duration("window", time.Hour, "how far before T the external records are listed (reconcile)")
	failedAt := fs.String("failed-at", "", "the failure moment, RFC 3339 (reconcile, loss)")
	finishedAt := fs.String("finished-at", "", "when the backup finished, RFC 3339 (backup-record)")
	backupID := fs.String("backup", "", "the backup's label (backup-record)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	p, err := LoadProject(*project)
	if err != nil {
		return err
	}
	t, err := build(p, *instance, *wait, out)
	if err != nil {
		return err
	}
	ctx := context.Background()
	switch step {
	case "status":
		st, err := t.Status(ctx)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "state: %s\nrecovery: %s\nT: %s\ndone: %s\n", st.State, st.Recovery, st.T.Format(time.RFC3339Nano),
			strings.Join(st.Done, ", "))
		return nil
	case "backup-record": // not a recovery step: the backup tool's record of a successful backup (be-protocol P15.21)
		at, err := time.Parse(time.RFC3339Nano, *finishedAt)
		if err != nil {
			return fmt.Errorf("--finished-at: %w", err)
		}
		return t.RecordBackup(ctx, *backupID, at)
	case StepAnnounce:
		when, err := time.Parse(time.RFC3339Nano, *at)
		if err != nil {
			return fmt.Errorf("--at: %w", err)
		}
		return t.Announce(ctx, *reason, *approver, when)
	case StepAbandon:
		return t.Abandon(ctx)
	case StepFreeze:
		return t.Freeze(ctx, *approver, *reason)
	case StepRestoreDB:
		return t.RestoreDB(ctx, *temp, *port)
	case StepAdvance:
		return t.Advance(ctx)
	case StepRedeliver:
		return t.Redeliver(ctx)
	case StepReplay:
		self, err := os.Executable()
		if err != nil {
			return err
		}
		return t.Replay(ctx, self)
	case StepKeys:
		return t.Keys(ctx)
	case StepReconcile, StepCheck, StepLoss, StepOpenRead, StepOpen:
		self, err := os.Executable()
		if err != nil {
			return err
		}
		if *failedAt != "" {
			if _, err := time.Parse(time.RFC3339Nano, *failedAt); err != nil {
				return fmt.Errorf("--failed-at: %w", err)
			}
		}
		o := VerifyOptions{Binary: self, Window: *window, FailedAt: *failedAt, Approver: *approver, Reason: *reason}
		return map[string]func(context.Context, VerifyOptions) error{StepReconcile: t.Reconcile, StepCheck: t.Check,
			StepLoss: t.Loss, StepOpenRead: t.OpenRead, StepOpen: t.Open}[step](ctx, o)
	}
	return fmt.Errorf("no such step: %s\n%s", step, Usage)
}

func build(p *Project, instance string, wait time.Duration, out io.Writer) (*Tool, error) {
	vars := map[string]string{}
	if err := readYAML(filepath.Join(p.Dir, "config", "vars.yaml"), &vars); err != nil {
		return nil, err
	}
	deployment := vars["DEPLOYMENT_ID"]
	env := os.Getenv
	tlsID, err := identity.LoadServiceTLS(deployment, Identity, env("SERVICE_TLS_CERT_FILE"), env("SERVICE_TLS_KEY_FILE"),
		env("SERVICE_TLS_CA_FILE"), os.ReadFile)
	if err != nil {
		return nil, fmt.Errorf("the ops/recover certificate: %w", err)
	}
	host, _ := os.Hostname()
	prof, err := runprofile.New(runprofile.Options{Endpoint: env("RUN_PROFILE_URL"), Region: env("RUN_PROFILE_REGION"),
		Bucket: env("RUN_PROFILE_BUCKET"), AccessKey: env("RUN_PROFILE_ACCESS_KEY"),
		Secret: func() string { return env("RUN_PROFILE_SECRET_KEY") }, Deployment: deployment,
		Writer: runprofile.Writer{Component: Identity, Instance: host}, Identity: tlsID})
	if err != nil {
		return nil, err
	}
	backup, err := findUp(p.Dir, filepath.Join("infra", "scripts", "pg-backup.sh"))
	if err != nil {
		return nil, err
	}
	t := &Tool{Project: p, Profile: prof, Backup: backup, Instance: instance, Out: out, Wait: wait, Exec: run,
		Conn: func(ctx context.Context, database string) (*pgx.Conn, error) {
			u, err := url.Parse(env("RECOVER_PG_ADMIN"))
			if err != nil || u.Host == "" {
				return nil, errors.New("RECOVER_PG_ADMIN is an administrator's DSN of the instance")
			}
			u.Path = "/" + database
			return pgx.Connect(ctx, u.String())
		}}
	t.Phases = func(ctx context.Context) (map[string]string, error) { return DockerPhases(ctx, p.Name, run) }
	if bus := env("BUS_URL"); strings.HasPrefix(bus, "nats://") {
		t.DeleteStream = func(ctx context.Context, stream string) error {
			tr, err := jetstream.Connect(bus, "be-ops-recover")
			if err != nil {
				return err
			}
			defer tr.Close()
			return tr.DeleteStream(ctx, stream)
		}
	}
	return t, nil
}

// replayCalls is the replay's calls (ReplayCalls) with the input and output files Tool.Calls mounts.
func replayCalls(args []string) error {
	fs := flag.NewFlagSet("recover replay-calls", flag.ContinueOnError)
	in := fs.String("in", "", "the input (ReplayInput, JSON)")
	out := fs.String("out", "", "where the report goes (JSON)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	raw, err := os.ReadFile(*in)
	if err != nil {
		return err
	}
	var input ReplayInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return fmt.Errorf("%s: %w", *in, err)
	}
	env := os.Getenv
	tlsID, err := identity.LoadServiceTLS(env("DEPLOYMENT_ID"), Identity, env("SERVICE_TLS_CERT_FILE"), env("SERVICE_TLS_KEY_FILE"),
		env("SERVICE_TLS_CA_FILE"), os.ReadFile)
	if err != nil {
		return fmt.Errorf("the ops/recover certificate: %w", err)
	}
	caller := &GRPCCaller{TLS: tlsID, Addresses: input.Addresses, Recovery: input.Recovery}
	defer caller.Close()
	return ReplayCalls(context.Background(), caller, input, func(r Report) error {
		b, err := json.MarshalIndent(r, "", "  ")
		if err != nil {
			return err
		}
		return os.WriteFile(*out, b, 0o600)
	})
}

// verifyCalls is steps 7-10's calls (VerifyCalls) with the input and output files Tool.runCalls mounts.
func verifyCalls(args []string) error {
	fs := flag.NewFlagSet("recover verify-calls", flag.ContinueOnError)
	in := fs.String("in", "", "the input (VerifyInput, JSON)")
	out := fs.String("out", "", "where the report goes (JSON)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	raw, err := os.ReadFile(*in)
	if err != nil {
		return err
	}
	var input VerifyInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return fmt.Errorf("%s: %w", *in, err)
	}
	env := os.Getenv
	tlsID, err := identity.LoadServiceTLS(env("DEPLOYMENT_ID"), Identity, env("SERVICE_TLS_CERT_FILE"), env("SERVICE_TLS_KEY_FILE"),
		env("SERVICE_TLS_CA_FILE"), os.ReadFile)
	if err != nil {
		return fmt.Errorf("the ops/recover certificate: %w", err)
	}
	caller := &GRPCCaller{TLS: tlsID, Addresses: input.Addresses, Recovery: input.Recovery}
	defer caller.Close()
	rep, err := VerifyCalls(context.Background(), caller, input)
	b, merr := json.MarshalIndent(rep, "", "  ")
	if merr != nil {
		return merr
	}
	if werr := os.WriteFile(*out, b, 0o600); werr != nil && err == nil {
		err = werr
	}
	return err
}

// run is an external command; its combined output comes back either way.
func run(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func findUp(dir, rel string) (string, error) {
	d, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(d, rel)); err == nil {
			return filepath.Join(d, rel), nil
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", fmt.Errorf("%s not found above %s", rel, dir)
		}
		d = parent
	}
}

// DockerPhases reads every running container of the project (brickkit-<project>-*) for its self-description: the
// recovery phase of each component, a shell's members included.
func DockerPhases(ctx context.Context, project string, run func(context.Context, string, string, ...string) (string, error)) (map[string]string, error) {
	names, err := run(ctx, "", "docker", "ps", "--filter", "name=^brickkit-"+project+"-", "--format", "{{.Names}}")
	if err != nil {
		return nil, fmt.Errorf("docker ps: %w", err)
	}
	out := map[string]string{}
	for _, n := range strings.Fields(names) {
		env, err := run(ctx, "", "docker", "inspect", "-f", "{{range .Config.Env}}{{println .}}{{end}}", n)
		if err != nil {
			return nil, err
		}
		port := ""
		for _, l := range strings.Split(env, "\n") {
			if v, ok := strings.CutPrefix(l, "PORT="); ok {
				port = v
			}
		}
		if port == "" {
			continue // not a component (a migration that ran, a helper)
		}
		raw, err := run(ctx, "", "docker", "exec", n, "wget", "-qO-", "http://127.0.0.1:"+port+"/.well-known/be-component")
		if err != nil {
			return nil, fmt.Errorf("%s: its self-description: %w", n, err)
		}
		if err := phasesOf([]byte(raw), out); err != nil {
			return nil, fmt.Errorf("%s: %w", n, err)
		}
	}
	return out, nil
}
