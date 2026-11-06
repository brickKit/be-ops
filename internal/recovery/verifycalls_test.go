package recovery

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/brickKit/be-sdk-go/v3/externalpb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakeMembers answers the calls of steps 7-10 from what each test sets.
type fakeMembers struct {
	evidence  [][]byte
	audit     []AuditRecord
	asked     map[string]*externalpb.ReconcileRequest
	reconcile map[string]error
	checks    map[string][]CheckOutcome
	checkErr  map[string]error
	present   map[string]map[string][2]string
	presErr   map[string]error
	numbers   map[string][]Series
	advanced  map[string][]Series
	sinces    []time.Time
}

func (f *fakeMembers) ListEvidence(context.Context) ([][]byte, error) { return f.evidence, nil }
func (f *fakeMembers) ListAudit(_ context.Context, since time.Time, categories []string) ([]AuditRecord, error) {
	f.sinces = append(f.sinces, since)
	var out []AuditRecord
	for _, r := range f.audit {
		if len(categories) == 0 || contains(categories, r.Category) {
			out = append(out, r)
		}
	}
	return out, nil
}
func (f *fakeMembers) Reconcile(_ context.Context, a string, req *externalpb.ReconcileRequest) ([]Conclusion, error) {
	if f.asked == nil {
		f.asked = map[string]*externalpb.ReconcileRequest{}
	}
	f.asked[a] = req
	if err := f.reconcile[a]; err != nil {
		return nil, err
	}
	return []Conclusion{{Reference: "r", Conclusion: "matched"}}, nil
}
func (f *fakeMembers) MemberChecks(_ context.Context, m string) ([]CheckOutcome, error) {
	return f.checks[m], f.checkErr[m]
}
func (f *fakeMembers) Present(_ context.Context, o string, _ []Asked) (map[string][2]string, error) {
	return f.present[o], f.presErr[o]
}
func (f *fakeMembers) Numbers(_ context.Context, o string) ([]Series, error) {
	return f.numbers[o], nil
}
func (f *fakeMembers) Advance(_ context.Context, o string, s []Series) error {
	if f.advanced == nil {
		f.advanced = map[string][]Series{}
	}
	f.advanced[o] = s
	return nil
}

var unimpl = status.Error(codes.Unimplemented, "unknown service")

func input(kind string) VerifyInput {
	return VerifyInput{Kind: kind, Recovery: "r1", T: "2026-11-05T10:00:00Z", FailedAt: "2026-11-05T11:00:00Z", Window: 3600,
		Members: []string{"erp/sales", "mdm/old"}, Senders: []string{"erp/payments", "erp/legacy"}}
}

// Step 7: each adapter gets its own issue records after T, the window and the failure moment; an older adapter is
// listed; one that fails fails the step (redone).
func TestReconcileCalls(t *testing.T) {
	f := &fakeMembers{evidence: [][]byte{evidenceJSON(IssuedCategory, "erp/payments", "p-1", "2026-11-05T10:05:00Z")},
		reconcile: map[string]error{"erp/legacy": unimpl}}
	rep, err := VerifyCalls(context.Background(), f, input(CallsReconcile))
	require.NoError(t, err)
	assert.Equal(t, []string{"p-1"}, f.asked["erp/payments"].GetIssued())
	assert.EqualValues(t, 3600, f.asked["erp/payments"].GetWindowBeforeSeconds())
	assert.Equal(t, "2026-11-05T11:00:00Z", f.asked["erp/payments"].GetFailedAt())
	assert.Equal(t, []string{"erp/legacy"}, rep.Unimplemented)
	assert.Len(t, rep.Conclusions["erp/payments"], 1)
	f.reconcile["erp/payments"] = errors.New("down")
	_, err = VerifyCalls(context.Background(), f, input(CallsReconcile))
	assert.ErrorContains(t, err, "redo the step")
}

// Step 8: passes only when every check of every member passed; an older member or one that fails is not a pass.
func TestCheckCalls(t *testing.T) {
	f := &fakeMembers{checks: map[string][]CheckOutcome{"erp/sales": {{Check: "a", Outcome: "pass"}}},
		checkErr: map[string]error{"mdm/old": unimpl}}
	rep, err := VerifyCalls(context.Background(), f, input(CallsCheck))
	require.NoError(t, err)
	assert.False(t, rep.Pass, "an older member answers no checks")
	assert.Equal(t, []string{"mdm/old"}, rep.Unimplemented)
	f.checkErr = nil
	f.checks["mdm/old"] = []CheckOutcome{{Check: "b", Outcome: "pass"}}
	rep, _ = VerifyCalls(context.Background(), f, input(CallsCheck))
	assert.True(t, rep.Pass)
	f.checks["mdm/old"] = []CheckOutcome{{Check: "b", Outcome: "diff"}}
	rep, _ = VerifyCalls(context.Background(), f, input(CallsCheck))
	assert.False(t, rep.Pass)
}

// Step 9: the owners' answers judged; an owner that cannot answer makes its objects unprovable; the audit loss from
// every category.
func TestLossCalls(t *testing.T) {
	f := &fakeMembers{audit: []AuditRecord{
		auditRec("erp/sales", 1, 1, "business_change", "erp.sales.order", "o-1", "3", "alice", "edit"),
		auditRec("erp/sales", 2, 2, "business_change", "erp.sales.order", "o-2", "1", "bob", "create"),
		auditRec("erp/sales", 4, 3, "security_change", "x", "y", "", "c", "op"),
		auditRec("mdm/org", 1, 1, "business_change", "mdm.org_unit", "u-1", "2", "carol", "move"),
	}, present: map[string]map[string][2]string{"erp/sales": {"o-1\x00erp.sales.order": {"present", "3"},
		"o-2\x00erp.sales.order": {"absent", ""}}}, presErr: map[string]error{"mdm/org": unimpl}}
	rep, err := VerifyCalls(context.Background(), f, input(CallsLoss))
	require.NoError(t, err)
	assert.Equal(t, 3, rep.Asked)
	require.Len(t, rep.Lost, 2)
	assert.Equal(t, "o-2", rep.Lost[0].ID)
	assert.Equal(t, LossLost, rep.Lost[0].Verdict)
	assert.Equal(t, "u-1", rep.Lost[1].ID)
	assert.Equal(t, LossUnprovable, rep.Lost[1].Verdict)
	require.Len(t, rep.AuditLoss, 2)
	assert.Equal(t, [][]int{{3, 3}}, rep.AuditLoss[0].Gaps, "the security change counts in the sequence")
	assert.Equal(t, time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC), f.sinces[0], "from T")
	assert.Empty(t, rep.EvidenceLoss)

	f.evidence = [][]byte{doubleFailureJSON("infra/authz", "r-1", "security_tightening", "carol", "2026-11-05T10:01:00.000000Z")}
	rep, err = VerifyCalls(context.Background(), f, input(CallsLoss))
	require.NoError(t, err)
	require.Len(t, rep.EvidenceLoss, 1, "the double failures after T in the loss (RC22)")
	assert.Equal(t, "r-1", rep.EvidenceLoss[0].Record)
}

// Step 10: the numbers reported, then raised only with Raise, each owner its own series.
func TestNumberCalls(t *testing.T) {
	r := auditRec("erp/billing", 1, 1, "business_change", "erp.billing.invoice", "i", "2", "", "issue")
	r.Number = &DocumentNumber{LegalEntity: "le-1", Code: "INV", Period: "2026", Number: "9"}
	f := &fakeMembers{audit: []AuditRecord{r}, numbers: map[string][]Series{"erp/sales": {}}}
	in := input(CallsNumbers)
	in.Members = []string{"erp/sales", "erp/billing"}
	rep, err := VerifyCalls(context.Background(), f, in)
	require.NoError(t, err)
	require.Len(t, rep.Raised, 1)
	assert.Nil(t, f.advanced, "only reported")
	in.Raise = true
	_, err = VerifyCalls(context.Background(), f, in)
	require.NoError(t, err)
	assert.Equal(t, []Series{{Owner: "erp/billing", LegalEntity: "le-1", Code: "INV", Period: "2026", Max: 9, FromAudit: 9}},
		f.advanced["erp/billing"])
}
