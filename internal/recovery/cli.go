package recovery

import (
	"context"
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
const Usage = `be-ops recover <step> [flags]   (assembly protocol 15, steps 1-5)

  status                         the deployment's recovery state, from the run profile
  announce --reason R --approver A --at T
                                 step 1: T (RFC 3339) is the point in time the databases go back to
  abandon                        give a frozen deployment up (before any database went back)
  freeze                         step 2: every member restarts frozen (RECOVERY_PHASE) and reports it
  restore-db [--temp N --port P] step 3: the instance at T into a temporary one, every schema copied back but the
                                 evidence store's
  advance                        step 4: the deployment's and the authorization's recovery generations
  redeliver                      step 5: the streams deleted, the members restart redelivering, every consumer asks
                                 its producers from its progress; waits for them and lists the gaps

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
	fs := flag.NewFlagSet("recover "+step, flag.ContinueOnError)
	project := fs.String("project", ".", "the BrickKit project")
	reason := fs.String("reason", "", "why (announce)")
	approver := fs.String("approver", "", "who decided (announce)")
	at := fs.String("at", "", "T, RFC 3339 (announce)")
	temp := fs.String("temp", "be-recover-temp", "the temporary instance's container (restore-db)")
	port := fs.Int("port", 15439, "the temporary instance's host port (restore-db)")
	instance := fs.String("instance", "be-postgres", "the database instance's container")
	wait := fs.Duration("wait", 5*time.Minute, "how long to wait for the members and the consumers")
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
	case StepAnnounce:
		when, err := time.Parse(time.RFC3339Nano, *at)
		if err != nil {
			return fmt.Errorf("--at: %w", err)
		}
		return t.Announce(ctx, *reason, *approver, when)
	case StepAbandon:
		return t.Abandon(ctx)
	case StepFreeze:
		return t.Freeze(ctx)
	case StepRestoreDB:
		return t.RestoreDB(ctx, *temp, *port)
	case StepAdvance:
		return t.Advance(ctx)
	case StepRedeliver:
		return t.Redeliver(ctx)
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
