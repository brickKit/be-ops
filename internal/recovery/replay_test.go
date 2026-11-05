package recovery

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeCaller struct {
	calls      []string
	outcomes   map[string]string // by record id; default applied
	down       string            // an owner that cannot be reached
	pending    int               // Sources answers not applied this many times
	subjects   []string
	unprovable []string
}

func idOf(raw []byte) string {
	var r struct{ ID string }
	_ = json.Unmarshal(raw, &r)
	return r.ID
}

func (f *fakeCaller) Replay(_ context.Context, owner string, raw []byte) (string, string, error) {
	if owner == f.down {
		return "", "", errors.New("unavailable")
	}
	f.calls = append(f.calls, "replay "+idOf(raw))
	if o := f.outcomes[idOf(raw)]; o != "" {
		return o, "because", nil
	}
	return "applied", "", nil
}

func (f *fakeCaller) Sources(_ context.Context, since string) (string, bool, error) {
	f.calls = append(f.calls, "sources "+since)
	if f.pending > 0 {
		f.pending--
		return "T1", false, nil
	}
	return "T1", true, nil
}

func (f *fakeCaller) ReplayDeferred(_ context.Context, raw []byte) (string, string, error) {
	f.calls = append(f.calls, "deferred "+idOf(raw))
	if o := f.outcomes[idOf(raw)]; o != "" {
		return o, "", nil
	}
	return "applied", "", nil
}

func (f *fakeCaller) Check(_ context.Context, subjects []string) (CheckResult, error) {
	f.calls = append(f.calls, "check")
	f.subjects = subjects
	return CheckResult{AdminState: "operational", Unprovable: f.unprovable}, nil
}

func withSubject(t *testing.T, e Evidence, subject string) Evidence {
	t.Helper()
	e.Subject = subject
	return e
}

func TestTheReplayCallsInOrderAndWaitsForTheSources(t *testing.T) {
	p := PlanReplay([]Evidence{
		rec(t, 1, "a-intent", AuthzID, "security_tightening", "intent", "", 0),
		rec(t, 2, "a-grant", AuthzID, "security_widening", "general", "1.1", 3),
		withSubject(t, rec(t, 3, "o-1", "mdm/org", "org_tightening", "effective", "1.1", 1), "unit-1"),
		withSubject(t, rec(t, 4, "i-1", "infra/iam-casdoor", "account_status", "effective", "1.1", 2), "bob"),
		rec(t, 5, "o-only", "mdm/org", "org_tightening", "intent", "", 0),
	})
	f := &fakeCaller{pending: 2, outcomes: map[string]string{"i-1": OutcomeFailed, "a-intent": "for_review"}}
	slept := 0
	rep, err := RunReplay(context.Background(), p, f, []string{"carol"}, time.Minute, func(time.Duration) { slept++ })
	require.NoError(t, err)
	assert.Equal(t, []string{"replay i-1", "replay o-1", "replay o-only", "replay a-grant",
		"sources ", "sources T1", "sources T1", "deferred a-intent", "check"}, f.calls)
	assert.Equal(t, 2, slept)
	assert.Equal(t, "T1", rep.SourceAt)
	assert.Equal(t, []string{"bob", "carol"}, f.subjects, "a failed re-execution's subject is closed with the 6.2 ones")
	assert.ElementsMatch(t, []string{"i-1", "o-only", "a-intent"}, rep.Review, "failed, intent-only, for review")
	require.Len(t, rep.Outcomes, 5)
	assert.True(t, rep.Outcomes[2].IntentOnly)
}

func TestAnOwnerOutOfReachStopsTheStep(t *testing.T) {
	p := PlanReplay([]Evidence{rec(t, 1, "o-1", "mdm/org", "org_tightening", "effective", "1.1", 1)})
	_, err := RunReplay(context.Background(), p, &fakeCaller{down: "mdm/org"}, nil, time.Minute, func(time.Duration) {})
	assert.ErrorContains(t, err, "do the step again")
}

func TestSourcesNotAppliedInTimeStopTheStep(t *testing.T) {
	f := &fakeCaller{pending: 1000}
	_, err := RunReplay(context.Background(), Plan{}, f, nil, 0, func(time.Duration) {})
	assert.ErrorContains(t, err, "6.4")
	assert.NotContains(t, f.calls, "check", "no invariant check before the sources are applied")
}

func TestAnUnprovableCheckStopsTheStep(t *testing.T) {
	_, err := RunReplay(context.Background(), Plan{}, &fakeCaller{unprovable: []string{"bob"}}, nil, time.Minute,
		func(time.Duration) {})
	assert.ErrorContains(t, err, "do the step again")
}

type listingCaller struct {
	*fakeCaller
	stored [][]byte
}

func (l listingCaller) ListEvidence(context.Context) ([][]byte, error) { return l.stored, nil }

// The calls' side reads the evidence store, adds the trusted alerts, and replays each record once.
func TestTheCallsMergeTheStoreAndTheAlerts(t *testing.T) {
	r1 := rec(t, 0, "e1", "mdm/org", "org_tightening", "effective", "1.1", 1)
	alertCopy := rec(t, 0, "e1", "mdm/org", "org_tightening", "effective", "1.1", 1) // the store has it too
	onlyAlert := rec(t, 0, "e2", "mdm/org", "org_tightening", "intent", "", 0)
	f := &fakeCaller{}
	var got Report
	err := ReplayCalls(context.Background(), listingCaller{fakeCaller: f, stored: [][]byte{r1.Raw}},
		ReplayInput{Alerts: [][]byte{alertCopy.Raw, onlyAlert.Raw}, ReviewSubjects: []string{"dave"}, WaitSeconds: 1},
		func(r Report) error { got = r; return nil })
	require.NoError(t, err)
	assert.Equal(t, []string{"replay e1", "replay e2", "sources ", "check"}, f.calls)
	assert.Equal(t, []string{"dave"}, f.subjects)
	assert.Len(t, got.Outcomes, 2)
	err = ReplayCalls(context.Background(), listingCaller{fakeCaller: &fakeCaller{}, stored: [][]byte{[]byte(`{}`)}},
		ReplayInput{}, func(Report) error { return nil })
	assert.ErrorContains(t, err, "evidence record 0", "a record that is none stops the step")
}
