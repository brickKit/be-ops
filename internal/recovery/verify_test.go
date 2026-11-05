package recovery

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brickKit/be-sdk-go/v3/runprofile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Steps 7-10 with the run profile, the commands and the calls faked: each step in its turn, the phase it sets, the
// inputs the calls get, the guards of read-only opening, the numbers raised at the end, and the recovery over.
func TestTheStepsOfVerifyingAndOpening(t *testing.T) {
	ctx := context.Background()
	p := project(t)
	write(t, p.Dir, ".brickkit/manifests/mdm/org/3.0.16/component.yaml",
		"configSchema:\n  properties:\n    RECOVERY_PHASE:\n      enum: [frozen, verifying, read_only]\n    EXTERNAL_COMMANDS_QUERY_ONLY:\n      type: boolean\n")
	prof := &fakeProfile{}
	phase := ""
	var inputs []VerifyInput
	reports := map[string]VerifyReport{
		CallsReconcile: {Conclusions: map[string][]Conclusion{"mdm/org": {{Reference: "p-9", Conclusion: "external_only"},
			{Reference: "p-1", Conclusion: "matched"}}}},
		CallsCheck:   {Checks: map[string][]CheckOutcome{"mdm/org": {{Check: "x", Outcome: "diff", Differences: []string{}}}}, Pass: false},
		CallsLoss:    {Lost: []Lost{}, AuditLoss: []SourceLoss{}},
		CallsNumbers: {Raised: []Series{{Owner: "mdm/org", Code: "X", Max: 3}}},
	}
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	var out bytes.Buffer
	tool := &Tool{Project: p, Profile: prof, Out: &out, Wait: 3 * time.Second,
		Now: func() time.Time { now = now.Add(time.Second); return now },
		Exec: func(_ context.Context, _ string, name string, args ...string) (string, error) {
			switch name {
			case "brickkit":
				raw, _ := os.ReadFile(filepath.Join(p.Dir, "config/vars.yaml"))
				phase = ""
				if _, v, ok := strings.Cut(string(raw), "RECOVERY_PHASE: "); ok {
					phase = strings.Trim(strings.TrimSpace(v), `"`)
				}
			case "docker":
				var work string
				for _, a := range args {
					if w, ok := strings.CutSuffix(a, ":/work"); ok {
						work = w
					}
				}
				raw, err := os.ReadFile(filepath.Join(work, "in.json"))
				require.NoError(t, err)
				var in VerifyInput
				require.NoError(t, json.Unmarshal(raw, &in))
				inputs = append(inputs, in)
				b, _ := json.Marshal(reports[in.Kind])
				require.NoError(t, os.WriteFile(filepath.Join(work, "out.json"), b, 0o600))
			}
			return "", nil
		},
		Phases: func(context.Context) (map[string]string, error) {
			return map[string]string{"infra/audit": phase, "mdm/org": phase}, nil
		}}
	// steps 1-6 done
	for i, s := range []Step{{Step: StepAnnounce, Recovery: "r1", T: "2026-11-05T10:00:00Z"}, {Step: StepFreeze},
		{Step: StepRestoreDB}, {Step: StepAdvance}, {Step: StepRedeliver}, {Step: StepReplay}, {Step: StepKeys}} {
		s.State, s.At = After(s.Step), at(i)
		_, err := prof.Append(ctx, runprofile.KindRecoveryStep, s)
		require.NoError(t, err)
	}
	o := VerifyOptions{Binary: "/be-ops", Window: 90 * time.Minute, FailedAt: "2026-11-05T11:00:00Z"}
	assert.Error(t, tool.Check(ctx, o), "reconcile first")

	require.NoError(t, tool.Reconcile(ctx, o))
	assert.Equal(t, "verifying", phase)
	in := inputs[0]
	assert.Equal(t, CallsReconcile, in.Kind)
	assert.Equal(t, "2026-11-05T10:00:00Z", in.T)
	assert.EqualValues(t, 5400, in.Window)
	assert.Equal(t, []string{"mdm/org"}, in.Senders)
	assert.Equal(t, []string{"infra/audit", "infra/authz", "mdm/org"}, in.Members)

	require.NoError(t, tool.Check(ctx, o))
	require.NoError(t, tool.Loss(ctx, VerifyOptions{Binary: "/be-ops"}))
	assert.Equal(t, "2026-11-05T11:00:00Z", inputs[2].FailedAt, "the failure moment step 7 was given")
	loss, err := tool.lastDetail(ctx, Status{Recovery: "r1"}, StepLoss)
	require.NoError(t, err)
	assert.Equal(t, []any{"mdm/org p-9 (external_only)"}, loss["no_reentry"])

	assert.ErrorContains(t, tool.OpenRead(ctx, VerifyOptions{}), "--approver")
	assert.ErrorContains(t, tool.OpenRead(ctx, VerifyOptions{Approver: "alice"}), "--reason", "a check did not pass")
	assert.Equal(t, "verifying", phase, "nothing changed")
	assert.Error(t, tool.Open(ctx, VerifyOptions{Approver: "alice"}), "read-only first")
	require.NoError(t, tool.OpenRead(ctx, VerifyOptions{Approver: "alice", Reason: "x differs by design"}))
	assert.Equal(t, "read_only", phase)
	st, _ := tool.Status(ctx)
	assert.Equal(t, ReadOnly, st.State)

	assert.ErrorContains(t, tool.Open(ctx, VerifyOptions{Binary: "/be-ops"}), "--approver")
	require.NoError(t, tool.Open(ctx, VerifyOptions{Binary: "/be-ops", Approver: "alice"}))
	last := inputs[len(inputs)-1]
	assert.Equal(t, CallsNumbers, last.Kind)
	assert.True(t, last.Raise)
	assert.Equal(t, "", phase, "running")
	st, _ = tool.Status(ctx)
	assert.Equal(t, Status{State: Running}, st, "the recovery is over")
}
