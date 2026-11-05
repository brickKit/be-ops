package recovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/brickKit/be-sdk-go/v3/ids"
	"github.com/brickKit/be-sdk-go/v3/runprofile"
	"github.com/jackc/pgx/v5"
)

// Profile is the run profile the tool keeps the recovery in (runprofile.Store): signed, object-locked records.
type Profile interface {
	Append(ctx context.Context, kind string, body any) (runprofile.Record, error)
	List(ctx context.Context, kind string) ([]runprofile.Record, []error, error)
	Advance(ctx context.Context, kind string, seen ...int64) (int64, error)
}

// Tool is be-ops recover for one deployment (a BrickKit project).
type Tool struct {
	Project *Project
	Profile Profile
	// Conn opens an administrator's connection to one database of the instance the components' databases live in.
	Conn func(ctx context.Context, database string) (*pgx.Conn, error)
	// Exec runs an external command (brickkit, docker, the backup script) in a directory and returns its output.
	Exec func(ctx context.Context, dir, name string, args ...string) (string, error)
	// Phases reads the recovery phase every running member reports (its self-description), by component.
	Phases func(ctx context.Context) (map[string]string, error)
	// Backup is infra/scripts/pg-backup.sh; Instance the instance's container (be-postgres).
	Backup, Instance string
	// Bus deletes a stream with its durable subscriptions (step 5); nil: the transport is rebuilt otherwise.
	DeleteStream func(ctx context.Context, stream string) error
	Out          io.Writer
	Now          func() time.Time
	// Wait bounds the waits for the members (a phase change) and for the consumers (step 5).
	Wait time.Duration
}

func (t *Tool) now() time.Time {
	if t.Now != nil {
		return t.Now().UTC()
	}
	return time.Now().UTC()
}

func (t *Tool) say(format string, a ...any) { fmt.Fprintf(t.Out, format+"\n", a...) }

// Status folds the run profile's recovery records.
func (t *Tool) Status(ctx context.Context) (Status, error) {
	recs, problems, err := t.Profile.List(ctx, runprofile.KindRecoveryStep)
	if err != nil {
		return Status{}, err
	}
	for _, p := range problems {
		t.say("⚠ a recovery record is not used: %v", p) // its signature does not hold (P12.4)
	}
	var steps []Step
	for _, r := range recs {
		s, err := Decode(r.Body)
		if err != nil {
			return Status{}, fmt.Errorf("record %s: %w", r.ID, err)
		}
		steps = append(steps, s)
	}
	return Fold(steps)
}

// record appends a step after checking it against the state table.
func (t *Tool) record(ctx context.Context, st Status, s Step) error {
	if err := Allowed(st, s.Step); err != nil {
		return err
	}
	s.State, s.At = After(s.Step), t.now().Format(AtLayout)
	if s.Recovery == "" {
		s.Recovery = st.Recovery
	}
	_, err := t.Profile.Append(ctx, runprofile.KindRecoveryStep, s)
	return err
}

// Announce is step 1: who decided, why, and the point in time the databases go back to.
func (t *Tool) Announce(ctx context.Context, reason, approver string, at time.Time) error {
	st, err := t.Status(ctx)
	if err != nil {
		return err
	}
	if reason == "" || approver == "" || at.IsZero() {
		return errors.New("the reason, the approver and T are recorded (protocol 15, step 1)")
	}
	if !at.Before(t.now()) {
		return errors.New("T is a moment in the past: the latest one before the damage that the archive reaches")
	}
	if err := t.record(ctx, st, Step{Step: StepAnnounce, Recovery: ids.New().String(), T: at.UTC().Format(time.RFC3339Nano),
		Reason: reason, Approver: approver}); err != nil {
		return err
	}
	t.say("announced: T = %s; next: be-ops recover freeze", at.UTC().Format(time.RFC3339Nano))
	return nil
}

// Abandon gives a frozen deployment up: running again (only before any database went back).
func (t *Tool) Abandon(ctx context.Context) error {
	st, err := t.Status(ctx)
	if err != nil {
		return err
	}
	if err := Allowed(st, StepAbandon); err != nil {
		return err
	}
	if err := t.phase(ctx, ""); err != nil {
		return err
	}
	return t.record(ctx, st, Step{Step: StepAbandon})
}

// Freeze is step 2: every member restarts in the frozen phase (no forwarding, subscription, job or user request:
// be-protocol P1.23) and is seen to report it.
func (t *Tool) Freeze(ctx context.Context) error {
	st, err := t.Status(ctx)
	if err != nil {
		return err
	}
	if err := Allowed(st, StepFreeze); err != nil {
		return err
	}
	if err := t.phase(ctx, "frozen"); err != nil {
		return err
	}
	return t.record(ctx, st, Step{Step: StepFreeze, Detail: map[string]any{"phase": "frozen"}})
}

// phase writes the phase into the shared variables, brings the project up again and waits until every member
// reports it (an empty phase: none).
func (t *Tool) phase(ctx context.Context, phase string) error {
	if err := t.Project.SetPhase(phase); err != nil {
		return err
	}
	held := t.Project.Unsupported()
	if err := t.Project.Hold(held, phase != ""); err != nil {
		return err
	}
	if len(held) > 0 && phase != "" {
		t.say("stopped while the recovery runs (no recovery mode): %v", held)
	}
	if out, err := t.Exec(ctx, t.Project.Dir, "brickkit", "up"); err != nil {
		return fmt.Errorf("brickkit up: %w\n%s", err, tail(out))
	}
	deadline := t.now().Add(t.Wait)
	for {
		got, err := t.Phases(ctx)
		var wrong []string
		if err == nil {
			for id, p := range got {
				if p != phase && !(phase != "" && slices.Contains(held, id)) {
					wrong = append(wrong, id+"="+p)
				}
			}
			if len(got) > 0 && len(wrong) == 0 {
				t.say("every member reports the phase %q (%d)", phase, len(got))
				return nil
			}
		}
		if t.now().After(deadline) {
			slices.Sort(wrong)
			return fmt.Errorf("not every member reports the phase %q: %v %v", phase, wrong, err)
		}
		time.Sleep(time.Second)
	}
}

// RestoreDB is step 3: the instance as it was at T into a temporary instance, then every component's schema copied
// back from it, the evidence store's excepted (it never goes back); the members stay frozen meanwhile.
func (t *Tool) RestoreDB(ctx context.Context, temp string, port int) error {
	st, err := t.Status(ctx)
	if err != nil {
		return err
	}
	if err := Allowed(st, StepRestoreDB); err != nil {
		return err
	}
	if out, err := t.Exec(ctx, "", t.Backup, "restore-to", st.T.Format(time.RFC3339Nano), temp, strconv.Itoa(port)); err != nil {
		return fmt.Errorf("restoring to T: %w\n%s", err, tail(out))
	}
	defer func() { _, _ = t.Exec(context.WithoutCancel(ctx), "", t.Backup, "drop-temp", temp) }()
	var copied []string
	for _, c := range t.Project.Restored() {
		if out, err := t.Exec(ctx, "", "sh", "-c", fmt.Sprintf(
			`docker exec -u postgres %s psql -v ON_ERROR_STOP=1 -qAt -d %s -c 'DROP SCHEMA IF EXISTS %s CASCADE' && `+
				`docker exec -u postgres %s pg_dump -d %s -n %s | docker exec -i -u postgres %s psql -v ON_ERROR_STOP=1 -qAt -d %s`,
			t.Instance, c.Database, c.Schema, temp, c.Database, c.Schema, t.Instance, c.Database)); err != nil {
			return fmt.Errorf("%s (%s.%s): %w\n%s", c.ID, c.Database, c.Schema, err, tail(out))
		}
		copied = append(copied, c.Database+"."+c.Schema)
		t.say("copied back %s.%s (%s)", c.Database, c.Schema, c.ID)
	}
	return t.record(ctx, st, Step{Step: StepRestoreDB, Detail: map[string]any{"schemas": copied, "temporary": temp}})
}

// Advance is step 4: the deployment's recovery generation (the largest of the run profile and every database that
// went back, plus one, in the run profile first: P12.5) into every such database, and the authorization member's
// recovery generation likewise, so every view and permit from before is void.
func (t *Tool) Advance(ctx context.Context) error {
	st, err := t.Status(ctx)
	if err != nil {
		return err
	}
	if err := Allowed(st, StepAdvance); err != nil {
		return err
	}
	restored := t.Project.Restored()
	var seen []int64
	for _, c := range restored {
		var g int64
		if err := t.query(ctx, c.Database, `SELECT generation FROM `+pgx.Identifier{c.Schema, "be_recovery_generation"}.Sanitize(), &g); err != nil {
			return fmt.Errorf("%s: %w", c.ID, err)
		}
		seen = append(seen, g)
	}
	next, err := t.Profile.Advance(ctx, runprofile.KindDeploymentGeneration, seen...)
	if err != nil {
		return fmt.Errorf("the run profile did not take the new generation; nothing advanced: %w", err)
	}
	for _, c := range restored {
		if err := t.exec(ctx, c.Database, `UPDATE `+pgx.Identifier{c.Schema, "be_recovery_generation"}.Sanitize()+
			` SET generation = $1`, next); err != nil {
			return fmt.Errorf("%s: %w", c.ID, err)
		}
	}
	detail := map[string]any{"deployment_generation": next}
	for _, c := range restored {
		if c.ID != "infra/authz" {
			continue
		}
		table := pgx.Identifier{c.Schema, "authz_state"}.Sanitize()
		var g int64
		if err := t.query(ctx, c.Database, `SELECT recovery_generation FROM `+table, &g); err != nil {
			return fmt.Errorf("infra/authz: %w", err)
		}
		a, err := t.Profile.Advance(ctx, runprofile.KindAuthzGeneration, g)
		if err != nil {
			return fmt.Errorf("the run profile did not take the authorization generation: %w", err)
		}
		if err := t.exec(ctx, c.Database, `UPDATE `+table+` SET recovery_generation = $1`, a); err != nil {
			return fmt.Errorf("infra/authz: %w", err)
		}
		detail["authz_generation"] = a
	}
	t.say("advanced: %v", detail)
	return t.record(ctx, st, Step{Step: StepAdvance, Detail: detail})
}

// Redeliver is step 5: every stream and its durable subscriptions deleted (the transport is not an authoritative
// copy), the members restarted in the redelivering phase; each consumer, seeing its database's epoch changed, asks
// its producers for a redelivery from its progress (be-protocol P6.39). The step waits until every consumer has its
// answers and lists what each could not get (be_resend_gap).
func (t *Tool) Redeliver(ctx context.Context) error {
	st, err := t.Status(ctx)
	if err != nil {
		return err
	}
	if err := Allowed(st, StepRedeliver); err != nil {
		return err
	}
	streams := map[string]bool{}
	for _, c := range t.Project.Restored() {
		names, err := t.strings(ctx, c.Database, `SELECT DISTINCT stream FROM `+pgx.Identifier{c.Schema, "be_outbox_partition"}.Sanitize())
		if err != nil {
			return fmt.Errorf("%s: %w", c.ID, err)
		}
		for _, n := range names {
			streams[n] = true
		}
	}
	if t.DeleteStream != nil {
		for s := range streams {
			if err := t.DeleteStream(ctx, s); err != nil {
				return fmt.Errorf("deleting the stream %s: %w", s, err)
			}
		}
		t.say("deleted %d streams", len(streams))
	}
	if err := t.phase(ctx, "redelivering"); err != nil {
		return err
	}
	deadline := t.now().Add(t.Wait)
	for {
		pending, gaps, err := t.consumers(ctx)
		if err != nil {
			return err
		}
		if len(pending) == 0 {
			t.say("every consumer has its redelivery; gaps: %v", gaps)
			return t.record(ctx, st, Step{Step: StepRedeliver, Detail: map[string]any{"streams": len(streams), "gaps": gaps}})
		}
		if t.now().After(deadline) {
			return fmt.Errorf("consumers still waiting for a redelivery: %v (their logs say which producer did not answer)", pending)
		}
		time.Sleep(2 * time.Second)
	}
}

// consumers lists the components whose consumer side has not recorded the current epoch yet, and the gaps recorded.
func (t *Tool) consumers(ctx context.Context) (pending []string, gaps map[string][]string, err error) {
	gaps = map[string][]string{}
	for _, c := range t.Project.Restored() {
		conn, err := t.Conn(ctx, c.Database)
		if err != nil {
			return nil, nil, err
		}
		var subs bool
		if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM `+pgx.Identifier{c.Schema, "be_subscription_run"}.Sanitize()+`)`).
			Scan(&subs); err != nil || !subs {
			conn.Close(ctx)
			continue // no subscription: not a consumer
		}
		var settled bool
		err = conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM `+pgx.Identifier{c.Schema, "be_consumer_epoch"}.Sanitize()+` e, `+
			pgx.Identifier{c.Schema, "be_recovery_generation"}.Sanitize()+` g WHERE e.epoch_generation = g.generation)`).Scan(&settled)
		if err == nil && !settled {
			pending = append(pending, c.ID)
		}
		rows, qerr := conn.Query(ctx, `SELECT subscription || ' ' || source_component || ' ' || source_stream || '/' ||
			source_partition || ' ' || kind FROM `+pgx.Identifier{c.Schema, "be_resend_gap"}.Sanitize()+` ORDER BY id`)
		if qerr == nil {
			list, _ := pgx.CollectRows(rows, pgx.RowTo[string])
			if len(list) > 0 {
				gaps[c.ID] = list
			}
		}
		conn.Close(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", c.ID, err)
		}
	}
	return pending, gaps, nil
}

func (t *Tool) query(ctx context.Context, database, sql string, dest ...any) error {
	conn, err := t.Conn(ctx, database)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	return conn.QueryRow(ctx, sql).Scan(dest...)
}

func (t *Tool) exec(ctx context.Context, database, sql string, args ...any) error {
	conn, err := t.Conn(ctx, database)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	_, err = conn.Exec(ctx, sql, args...)
	return err
}

func (t *Tool) strings(ctx context.Context, database, sql string) ([]string, error) {
	conn, err := t.Conn(ctx, database)
	if err != nil {
		return nil, err
	}
	defer conn.Close(ctx)
	rows, err := conn.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

func tail(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > 15 {
		lines = lines[len(lines)-15:]
	}
	return strings.Join(lines, "\n")
}

// phasesOf reads one self-description into the phases by component: the recovery_phase field (empty when running),
// a shell's members each with their own.
func phasesOf(raw []byte, out map[string]string) error {
	var d struct {
		ID      string `json:"component_id"`
		Phase   string `json:"recovery_phase"`
		Members []struct {
			ID    string `json:"component_id"`
			Phase string `json:"recovery_phase"`
		} `json:"members"`
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		return err
	}
	if len(d.Members) == 0 {
		out[d.ID] = d.Phase
	}
	for _, m := range d.Members {
		out[m.ID] = m.Phase
	}
	return nil
}
