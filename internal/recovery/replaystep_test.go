package recovery

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brickKit/be-sdk-go/v3/runprofile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recoveryMode is a manifest's declaration of the recovery phase (every member here has it).
const recoveryMode = "configSchema:\n  properties:\n    RECOVERY_PHASE:\n      type: string\n"

// stepSix is a project after step 5: the run profile holds steps 1-5, the manifests name the system ports, the
// identity member's configuration names its signing key file.
func stepSix(t *testing.T) (*Project, *fakeProfile) {
	dir := t.TempDir()
	write(t, dir, "brickkit.yaml", "project: r4-x\ncomponents:\n  - id: infra/audit\n    version: 3.0.18\n"+
		"  - id: infra/authz\n    version: 3.0.30\n  - id: infra/iam-casdoor\n    version: 3.0.26\n  - id: erp/sales\n    version: 3.0.25\n")
	write(t, dir, "config/vars.yaml", "DEPLOYMENT_ID: d1\nRECOVERY_PHASE: redelivering\n")
	write(t, dir, "config/infra-iam-casdoor.yaml", "IAM_ISSUER: x\nIAM_SIGNING_KEY: file://.secrets/iam/signing-key.pem\n")
	write(t, dir, ".secrets/iam/signing-key.pem", "the old key")
	for id, port := range map[string]string{"infra/audit/3.0.18": "9206", "infra/authz/3.0.30": "9223", "infra/iam-casdoor/3.0.26": "9200"} {
		write(t, dir, ".brickkit/manifests/"+id+"/component.yaml",
			"deployment:\n  port: 8000\n  extraPorts:\n    - name: grpc\n      port: "+port+"\n      protocol: grpc\n"+recoveryMode)
	}
	write(t, dir, ".brickkit/manifests/erp/sales/3.0.25/component.yaml", "deployment:\n  port: 8084\n"+recoveryMode) // no system port
	p, err := LoadProject(dir)
	require.NoError(t, err)
	prof := &fakeProfile{}
	at := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	for i, s := range []string{StepAnnounce, StepFreeze, StepRestoreDB, StepAdvance, StepRedeliver} {
		st := Step{Step: s, State: After(s), At: at.Add(time.Duration(i) * time.Second).Format(AtLayout)}
		if s == StepAnnounce {
			st.Recovery, st.T = "r1 2026-11-05", at.Add(-time.Hour).Format(time.RFC3339Nano)
		}
		_, err := prof.Append(context.Background(), runprofile.KindRecoveryStep, st)
		require.NoError(t, err)
	}
	return p, prof
}

func evidenceBody(t *testing.T, id, recorder, subject string) json.RawMessage {
	raw, err := json.Marshal(map[string]any{"record_type": "be.evidence.record", "id": id, "phase": "intent",
		"category": "account_status", "subject": subject, "recorder": map[string]any{"component": recorder}})
	require.NoError(t, err)
	return raw
}

func TestTheReplayStepRunsTheCallsInTheNetworkAndRecordsTheList(t *testing.T) {
	ctx := context.Background()
	p, prof := stepSix(t)
	prof.listed = []runprofile.Listed{
		{Key: "a1", Read: true, Record: runprofile.Record{Writer: runprofile.Writer{Component: "infra/iam-casdoor"},
			Body: evidenceBody(t, "e1", "infra/iam-casdoor", "bob")}},
		{Key: "a2", Read: true, Record: runprofile.Record{Writer: runprofile.Writer{Component: "erp/sales"},
			Body: evidenceBody(t, "e2", "infra/iam-casdoor", "carol")}}, // signed by another than its recorder
		{Key: "a3", Read: true, Err: errors.New("the signature does not hold"),
			Record: runprofile.Record{Body: evidenceBody(t, "e3", "mdm/org", "dave")}},
		{Key: "a4", Read: true, Record: runprofile.Record{Body: json.RawMessage(`{"other":"alert"}`)}}, // not an intent
	}
	phase := ""
	var in ReplayInput
	var run []string
	tool := &Tool{Project: p, Profile: prof, Out: &bytes.Buffer{}, Wait: 3 * time.Second,
		Exec: func(_ context.Context, _, name string, args ...string) (string, error) {
			switch name {
			case "brickkit":
				raw, _ := os.ReadFile(filepath.Join(p.Dir, "config/vars.yaml"))
				_, v, _ := strings.Cut(string(raw), "RECOVERY_PHASE: ")
				phase = strings.TrimSpace(v)
			case "docker":
				run = args
				work := ""
				for i, a := range args {
					if a == "-v" && strings.HasSuffix(args[i+1], ":/work") {
						work = strings.TrimSuffix(args[i+1], ":/work")
					}
				}
				raw, err := os.ReadFile(filepath.Join(work, "in.json"))
				require.NoError(t, err)
				require.NoError(t, json.Unmarshal(raw, &in))
				out, _ := json.Marshal(Report{Outcomes: []Outcome{{ID: "e1", Outcome: "applied"}, {ID: "x", Outcome: "reflected"}},
					Check: CheckResult{AdminState: "operational"}})
				require.NoError(t, os.WriteFile(filepath.Join(work, "out.json"), out, 0o600))
			}
			return "", nil
		},
		Phases: func(context.Context) (map[string]string, error) {
			return map[string]string{"infra/audit": phase, "infra/authz": phase}, nil
		}}
	assert.Error(t, tool.Keys(ctx), "replay first")
	require.NoError(t, tool.Replay(ctx, "/bin/be-ops"))

	assert.Equal(t, "replaying", phase, "the members restarted replaying")
	assert.Equal(t, map[string]string{"infra/audit": "infra-audit-3-0-18:9206", "infra/authz": "infra-authz-3-0-30:9223",
		"infra/iam-casdoor": "infra-iam-casdoor-3-0-26:9200"}, in.Addresses, "the aliases and the system ports")
	require.Len(t, in.Alerts, 1, "only the intent whose signature holds and whose signer is its recorder")
	assert.Contains(t, string(in.Alerts[0]), `"e1"`)
	assert.Equal(t, []string{"carol", "dave"}, in.ReviewSubjects, "the subjects of the untrusted intents (6.2)")
	assert.Equal(t, "r1 2026-11-05", in.Recovery)
	cmd := strings.Join(run, " ")
	assert.Contains(t, cmd, "--network brickkit-r4-x-net")
	assert.Contains(t, cmd, "/bin/be-ops:/be-ops:ro")
	assert.Contains(t, cmd, ReplayImage+" /be-ops recover replay-calls")
	assert.Contains(t, cmd, "DEPLOYMENT_ID=d1")
	st, err := tool.Status(ctx)
	require.NoError(t, err)
	assert.Contains(t, st.Done, StepReplay)
	recs, _, _ := prof.List(ctx, runprofile.KindRecoveryStep)
	last, _ := Decode(recs[len(recs)-1].Body)
	assert.Equal(t, map[string]any{"applied": float64(1), "reflected": float64(1)}, last.Detail["outcomes"])

	// 6.8: new keys in the files the configuration names, a standby named, the old file kept
	require.NoError(t, tool.Keys(ctx))
	old, err := os.ReadFile(filepath.Join(p.Dir, ".secrets/iam/signing-key.pem.before-r1_2026-11-05"))
	require.NoError(t, err)
	assert.Equal(t, "the old key", string(old))
	cfg, _ := os.ReadFile(filepath.Join(p.Dir, "config/infra-iam-casdoor.yaml"))
	assert.Contains(t, string(cfg), "IAM_SIGNING_KEY_NEXT: file://.secrets/iam/signing-key-next.pem")
	for _, f := range []string{"signing-key.pem", "signing-key-next.pem"} {
		raw, err := os.ReadFile(filepath.Join(p.Dir, ".secrets/iam", f))
		require.NoError(t, err)
		block, _ := pem.Decode(raw)
		require.NotNil(t, block, f)
		k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		require.NoError(t, err)
		assert.IsType(t, ed25519.PrivateKey{}, k)
		info, _ := os.Stat(filepath.Join(p.Dir, ".secrets/iam", f))
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
	recs, _, _ = prof.List(ctx, runprofile.KindRecoveryStep)
	last, _ = Decode(recs[len(recs)-1].Body)
	keys := last.Detail["keys"].(map[string]any)["infra/iam-casdoor"].(map[string]any)
	assert.NotEqual(t, keys["current"], keys["standby"])
}

func TestAKeyThatIsNoFileIsMadeByHand(t *testing.T) {
	assert.Equal(t, []string{"", ""}, func() []string { a, _ := fileRef("${KEY}"); b, _ := fileRef(nil); return []string{a, b} }())
	p, ok := fileRef(" file://.secrets/k.pem ")
	assert.True(t, ok)
	assert.Equal(t, ".secrets/k.pem", p)
}

// The run profile's canonical encoding takes no null: a replay with nothing untrusted and nothing replayed records
// empty lists, not nulls.
func TestTheReplayRecordsNoNull(t *testing.T) {
	ctx := context.Background()
	p, prof := stepSix(t)
	phase := ""
	tool := &Tool{Project: p, Profile: prof, Out: &bytes.Buffer{}, Wait: 3 * time.Second,
		Exec: func(_ context.Context, _, name string, args ...string) (string, error) {
			if name == "brickkit" {
				raw, _ := os.ReadFile(filepath.Join(p.Dir, "config/vars.yaml"))
				_, v, _ := strings.Cut(string(raw), "RECOVERY_PHASE: ")
				phase = strings.TrimSpace(v)
				return "", nil
			}
			for i, a := range args {
				if a == "-v" && strings.HasSuffix(args[i+1], ":/work") {
					rep, err := RunReplay(ctx, Plan{}, &fakeCaller{}, nil, time.Second, func(time.Duration) {})
					require.NoError(t, err)
					out, _ := json.Marshal(rep)
					require.NoError(t, os.WriteFile(filepath.Join(strings.TrimSuffix(args[i+1], ":/work"), "out.json"), out, 0o600))
				}
			}
			return "", nil
		},
		Phases: func(context.Context) (map[string]string, error) { return map[string]string{"infra/audit": phase}, nil }}
	require.NoError(t, tool.Replay(ctx, "/bin/be-ops"))
	recs, _, _ := prof.List(ctx, runprofile.KindRecoveryStep)
	assert.NotContains(t, string(recs[len(recs)-1].Body), "null")
}
