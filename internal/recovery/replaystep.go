package recovery

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/brickKit/be-sdk-go/v3/runprofile"
)

// Step 6, 6.2-6.5 (assembly protocol 15; A141, A142): the members restart replaying (the identity member ends the
// sessions from before at its start, 6.6); the alert channel's intents are verified (6.2); the replay's calls run
// where the members' system surfaces are reachable, the deployment's network (ReplayCalls in a container of the
// tool's own binary: BrickKit maps one port of each component to the host, the user surface); the list goes into
// the run profile.

// ReplayImage is the image the calls run in: the components' base (the tool's binary is static and mounted).
const ReplayImage = "alpine:3.24.2"

// ReplayInput is what the calls get from the step.
type ReplayInput struct {
	Recovery       string            `json:"recovery"`
	Addresses      map[string]string `json:"addresses"`
	Alerts         [][]byte          `json:"alerts"` // the alert channel's intents whose signature holds
	ReviewSubjects []string          `json:"review_subjects"`
	WaitSeconds    int               `json:"wait_seconds"`
}

// SystemAddresses are the components' system surfaces in the deployment's network: the alias BrickKit gives a
// component (its ID and version, "/" and "." as "-") and the port its manifest names grpc.
func (p *Project) SystemAddresses() map[string]string {
	out := map[string]string{}
	alias := strings.NewReplacer("/", "-", ".", "-")
	for _, c := range p.Components {
		var m struct {
			Deployment struct {
				ExtraPorts []struct {
					Name string `yaml:"name"`
					Port int    `yaml:"port"`
				} `yaml:"extraPorts"`
			} `yaml:"deployment"`
		}
		if readYAML(filepath.Join(p.Dir, ".brickkit", "manifests", c.ID, c.Version, "component.yaml"), &m) != nil {
			continue
		}
		for _, port := range m.Deployment.ExtraPorts {
			if port.Name == "grpc" {
				out[c.ID] = fmt.Sprintf("%s:%d", alias.Replace(c.ID+"-"+c.Version), port.Port)
			}
		}
	}
	return out
}

// alerts sorts the alert channel's records (6.2): an evidence record whose run-profile signature holds and whose
// signer is its recorder is replayed; any other is listed for review, and the subject it names (when it can be read)
// is closed in 6.5 unless an authorization admin.
func alerts(listed []runprofile.Listed) (trusted [][]byte, review []string, untrusted []string) {
	untrusted = []string{} // recorded in the run profile, whose canonical encoding has no null
	for _, l := range listed {
		var rec struct {
			RecordType string `json:"record_type"`
			ID         string `json:"id"`
			Subject    string `json:"subject"`
			Recorder   struct {
				Component string `json:"component"`
			} `json:"recorder"`
		}
		read := l.Read && json.Unmarshal(l.Record.Body, &rec) == nil
		if read && rec.RecordType != "be.evidence.record" {
			continue // another alert, not an intent
		}
		switch {
		case l.Err == nil && rec.Recorder.Component == l.Record.Writer.Component:
			trusted = append(trusted, l.Record.Body)
			continue
		case l.Err == nil:
			untrusted = append(untrusted, fmt.Sprintf("%s: signed by %s, recorded by %s", l.Key, l.Record.Writer.Component,
				rec.Recorder.Component))
		default:
			untrusted = append(untrusted, fmt.Sprintf("%s: %v", l.Key, l.Err))
		}
		if read && rec.Subject != "" {
			review = append(review, rec.Subject)
		}
	}
	return trusted, review, untrusted
}

// Replay is step 6's 6.2-6.5.
func (t *Tool) Replay(ctx context.Context, binary string) error {
	st, err := t.Status(ctx)
	if err != nil {
		return err
	}
	if err := Allowed(st, StepReplay); err != nil {
		return err
	}
	if err := t.phase(ctx, "replaying"); err != nil {
		return err
	}
	listed, err := t.Profile.ListAll(ctx, runprofile.KindAlert)
	if err != nil {
		return fmt.Errorf("the alert channel: %w", err)
	}
	trusted, review, untrusted := alerts(listed)
	for _, u := range untrusted {
		t.say("⚠ an alert-channel intent is not trusted (6.2), for review: %s", u)
	}
	in := ReplayInput{Recovery: st.Recovery, Addresses: t.Project.SystemAddresses(), Alerts: trusted,
		ReviewSubjects: review, WaitSeconds: int(t.Wait.Seconds())}
	rep, err := t.Calls(ctx, binary, in)
	if err != nil {
		return err
	}
	counts := map[string]int{}
	for _, o := range rep.Outcomes {
		counts[o.Outcome]++
	}
	t.say("replayed: %v; ignored %d; for review %d; the check: %+v", counts, len(rep.Ignored), len(rep.Review), rep.Check)
	return t.record(ctx, st, Step{Step: StepReplay, Detail: map[string]any{"report": rep, "outcomes": counts,
		"alerts_untrusted": untrusted}})
}

// Calls runs the replay's calls in the deployment's network (runCalls with replay-calls).
func (t *Tool) Calls(ctx context.Context, binary string, in ReplayInput) (Report, error) {
	var rep Report
	err := t.runCalls(ctx, binary, "replay-calls", in, &rep)
	return rep, err
}

// runCalls runs one of the tool's calls subcommands in the deployment's network: a container of ReplayImage with the
// tool's binary, the ops/recover certificate and an input and output directory mounted, on the project's network.
// The output is read whenever the calls wrote one, also when they failed (a partial report).
func (t *Tool) runCalls(ctx context.Context, binary, sub string, in, outp any) error {
	dir, err := os.MkdirTemp("", "be-recover-calls-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	raw, err := json.Marshal(in)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "in.json"), raw, 0o600); err != nil {
		return err
	}
	certs := map[string]string{"SERVICE_TLS_CERT_FILE": "/certs/cert.pem", "SERVICE_TLS_KEY_FILE": "/certs/key.pem",
		"SERVICE_TLS_CA_FILE": "/certs/ca.pem"}
	args := []string{"run", "--rm", "--network", "brickkit-" + t.Project.Name + "-net", "--user", fmt.Sprintf("%d", os.Getuid()),
		"-v", binary + ":/be-ops:ro", "-v", dir + ":/work"}
	for env, inside := range certs {
		args = append(args, "-v", os.Getenv(env)+":"+inside+":ro", "-e", env+"="+inside)
	}
	vars := map[string]string{}
	if err := readYAML(filepath.Join(t.Project.Dir, "config", "vars.yaml"), &vars); err != nil {
		return err
	}
	args = append(args, "-e", "DEPLOYMENT_ID="+vars["DEPLOYMENT_ID"], ReplayImage, "/be-ops", "recover", sub,
		"--in", "/work/in.json", "--out", "/work/out.json")
	out, runErr := t.Exec(ctx, "", "docker", args...)
	t.say("%s", tail(out))
	raw, err = os.ReadFile(filepath.Join(dir, "out.json"))
	switch {
	case err == nil:
		if err := json.Unmarshal(raw, outp); err != nil {
			return fmt.Errorf("the %s report: %w", sub, err)
		}
	case runErr == nil:
		return fmt.Errorf("the %s wrote no report: %w", sub, err)
	}
	if runErr != nil {
		return fmt.Errorf("the %s: %w", sub, runErr)
	}
	return nil
}

// ReplayCalls is the calls' side, in the deployment's network: the evidence store's records and the trusted alerts
// planned and replayed (6.3), the sources waited for (6.4), the deferred and the check (6.5).
func ReplayCalls(ctx context.Context, caller interface {
	Caller
	ListEvidence(ctx context.Context) ([][]byte, error)
}, in ReplayInput, out func(Report) error) error {
	stored, err := caller.ListEvidence(ctx)
	if err != nil {
		return err
	}
	var records []Evidence
	for i, raw := range append(stored, in.Alerts...) {
		e, err := ParseEvidence(raw, i)
		if err != nil {
			return fmt.Errorf("evidence record %d: %w", i, err)
		}
		records = append(records, e)
	}
	rep, err := RunReplay(ctx, PlanReplay(records), caller, in.ReviewSubjects, time.Duration(in.WaitSeconds)*time.Second, time.Sleep)
	if werr := out(rep); werr != nil && err == nil {
		err = werr
	}
	return err
}
