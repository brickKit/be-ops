package recovery

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brickKit/be-sdk-go/v3/runprofile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeProfile keeps records in memory, as the run profile keeps them (append only).
type fakeProfile struct {
	mu     sync.Mutex
	recs   map[string][]runprofile.Record
	fail   bool
	listed []runprofile.Listed // ListAll of the alert channel: records with their verification as given
}

func (f *fakeProfile) Append(_ context.Context, kind string, body any) (runprofile.Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return runprofile.Record{}, assert.AnError
	}
	raw, _ := json.Marshal(body)
	r := runprofile.Record{Kind: kind, Body: raw}
	if f.recs == nil {
		f.recs = map[string][]runprofile.Record{}
	}
	f.recs[kind] = append(f.recs[kind], r)
	return r, nil
}

func (f *fakeProfile) List(_ context.Context, kind string) ([]runprofile.Record, []error, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]runprofile.Record(nil), f.recs[kind]...), nil, nil
}

func (f *fakeProfile) ListAll(ctx context.Context, kind string) ([]runprofile.Listed, error) {
	if kind == runprofile.KindAlert {
		return f.listed, nil
	}
	recs, _, err := f.List(ctx, kind)
	out := make([]runprofile.Listed, 0, len(recs))
	for _, r := range recs {
		out = append(out, runprofile.Listed{Key: r.ID, Record: r, Read: true})
	}
	return out, err
}

func (f *fakeProfile) Advance(ctx context.Context, kind string, seen ...int64) (int64, error) {
	var cur int64
	recs, _, _ := f.List(ctx, kind)
	for _, r := range recs {
		var b struct{ Value int64 }
		_ = json.Unmarshal(r.Body, &b)
		cur = max(cur, b.Value)
	}
	next := runprofile.NextGeneration(cur, seen...)
	_, err := f.Append(ctx, kind, map[string]int64{"value": next})
	return next, err
}

func project(t *testing.T) *Project {
	dir := t.TempDir()
	write(t, dir, "brickkit.yaml", "project: f3-x\ncomponents:\n  - id: infra/audit\n    version: 3.0.16\n  - id: mdm/org\n    version: 3.0.16\n  - id: infra/authz\n    version: 3.0.27\n")
	write(t, dir, "config/vars.yaml", "PG_DATABASE: be_experiment\n")
	for id, schema := range map[string]string{"infra-audit": "infra_audit", "mdm-org": "mdm_org", "infra-authz": "infra_authz"} {
		write(t, dir, "config/"+id+".yaml", "PG_DATABASE: $var:PG_DATABASE\nPG_SCHEMA: "+schema+"\n")
	}
	p, err := LoadProject(dir)
	require.NoError(t, err)
	return p
}

// Steps 1-3 with the run profile, the commands and the members' reports faked: announce records T and why, freeze
// writes the phase, brings the project up and waits for every member to report it, restore-db restores to T into a
// temporary instance and copies every schema back but the evidence store's; each step only in its turn.
func TestTheFirstStepsInTurn(t *testing.T) {
	ctx := context.Background()
	p := project(t)
	prof := &fakeProfile{}
	var cmds []string
	phase := ""
	var out bytes.Buffer
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	tool := &Tool{Project: p, Profile: prof, Backup: "/x/pg-backup.sh", Instance: "be-postgres", Out: &out, Wait: 3 * time.Second,
		Now: func() time.Time { now = now.Add(time.Second); return now },
		Exec: func(_ context.Context, dir, name string, args ...string) (string, error) {
			cmds = append(cmds, strings.TrimSpace(name+" "+strings.Join(args, " ")))
			if name == "brickkit" {
				raw, _ := os.ReadFile(filepath.Join(p.Dir, "config/vars.yaml"))
				phase = ""
				if _, v, ok := strings.Cut(string(raw), "RECOVERY_PHASE: "); ok {
					phase = strings.Trim(strings.TrimSpace(v), `"`)
				}
			}
			return "", nil
		},
		Phases: func(context.Context) (map[string]string, error) {
			return map[string]string{"infra/audit": phase, "mdm/org": phase}, nil
		}}

	assert.Error(t, tool.Freeze(ctx), "announce first")
	assert.Error(t, tool.Announce(ctx, "", "alice", now.Add(-time.Hour)), "why is recorded")
	assert.Error(t, tool.Announce(ctx, "ransomware", "alice", now.Add(time.Hour)), "T is in the past")
	T := time.Date(2026, 11, 5, 10, 30, 0, 0, time.UTC)
	require.NoError(t, tool.Announce(ctx, "ransomware", "alice", T))
	assert.Error(t, tool.RestoreDB(ctx, "tmp", 15439), "freeze first")

	require.NoError(t, tool.Freeze(ctx))
	raw, _ := os.ReadFile(filepath.Join(p.Dir, "config/vars.yaml"))
	assert.Contains(t, string(raw), "RECOVERY_PHASE: frozen")
	assert.Equal(t, []string{"brickkit up"}, cmds)
	st, err := tool.Status(ctx)
	require.NoError(t, err)
	assert.Equal(t, Restoring, st.State)
	assert.Equal(t, T, st.T)

	cmds = nil
	require.NoError(t, tool.RestoreDB(ctx, "tmp", 15439))
	require.Len(t, cmds, 4)
	assert.Equal(t, "/x/pg-backup.sh restore-to 2026-11-05T10:30:00Z tmp 15439", cmds[0])
	assert.Contains(t, cmds[1], "DROP SCHEMA IF EXISTS infra_authz CASCADE")
	assert.Contains(t, cmds[1], "pg_dump -d be_experiment -n infra_authz")
	assert.Contains(t, cmds[2], "-n mdm_org")
	assert.Equal(t, "/x/pg-backup.sh drop-temp tmp", cmds[3])
	assert.NotContains(t, strings.Join(cmds, "\n"), "infra_audit", "the evidence store never goes back")
	st, _ = tool.Status(ctx)
	assert.Equal(t, []string{StepFreeze, StepRestoreDB}, st.Done)
	assert.Error(t, tool.Abandon(ctx), "the databases went back: no giving up")
}

// A member that does not report the phase stops the step: nothing is recorded.
func TestAFreezeWaitsForEveryMember(t *testing.T) {
	ctx := context.Background()
	p := project(t)
	prof := &fakeProfile{}
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	tool := &Tool{Project: p, Profile: prof, Out: &bytes.Buffer{}, Wait: 2 * time.Second,
		Now:  func() time.Time { now = now.Add(time.Second); return now },
		Exec: func(context.Context, string, string, ...string) (string, error) { return "", nil },
		Phases: func(context.Context) (map[string]string, error) {
			return map[string]string{"infra/audit": "frozen", "mdm/org": ""}, nil
		}}
	require.NoError(t, tool.Announce(ctx, "a test", "alice", now.Add(-time.Hour)))
	err := tool.Freeze(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mdm/org=")
	st, _ := tool.Status(ctx)
	assert.Equal(t, Frozen, st.State, "not recorded")
}

// A shell's self-description gives each member's phase.
func TestPhasesOfAShell(t *testing.T) {
	out := map[string]string{}
	require.NoError(t, phasesOf([]byte(`{"component_id":"be/go-infra","recovery_phase":"frozen","members":[
		{"component_id":"infra/audit","recovery_phase":"frozen"},{"component_id":"mdm/org","recovery_phase":"frozen"}]}`), out))
	require.NoError(t, phasesOf([]byte(`{"component_id":"erp/sales"}`), out))
	assert.Equal(t, map[string]string{"infra/audit": "frozen", "mdm/org": "frozen", "erp/sales": ""}, out)
}
