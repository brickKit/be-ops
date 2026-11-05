package recovery

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var tRec = time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)

func evidenceJSON(category, recorder, reference, at string) []byte {
	return []byte(`{"record_type":"be.evidence.record","category":"` + category + `","at":"` + at +
		`","recorder":{"component":"` + recorder + `"},"change":{"reference":"` + reference + `"}}`)
}

// Step 7: the issue records written at or after T, by recorder, each reference once.
func TestIssuedAfter(t *testing.T) {
	got := IssuedAfter([][]byte{
		evidenceJSON(IssuedCategory, "erp/payments", "p-2", "2026-11-05T10:00:00.000000Z"),
		evidenceJSON(IssuedCategory, "erp/payments", "p-1", "2026-11-05T10:05:00.000000Z"),
		evidenceJSON(IssuedCategory, "erp/payments", "p-1", "2026-11-05T10:06:00.000000Z"),
		evidenceJSON(IssuedCategory, "erp/payments", "p-old", "2026-11-05T09:59:59.000000Z"),
		evidenceJSON("security_tightening", "infra/authz", "x", "2026-11-05T10:05:00.000000Z"),
		[]byte("not json"),
	}, tRec)
	assert.Equal(t, map[string][]string{"erp/payments": {"p-1", "p-2"}}, got)
}

func auditRec(source string, seq int64, minute int, category, typ, id, version, actor, op string) AuditRecord {
	return AuditRecord{Source: source, SourceEpoch: "2.1", SourceSeq: seq, OccurredAt: tRec.Add(time.Duration(minute) * time.Minute),
		Category: category, TargetType: typ, TargetID: id, Version: version, Actor: actor, Operation: op}
}

// Step 9: one question per object at the highest version named; security changes and unnamed targets left out; the
// verdicts on the owners' answers.
func TestTheLoss(t *testing.T) {
	records := []AuditRecord{
		auditRec("erp/sales", 5, 1, "business_change", "erp.sales.order", "o-1", "2", "bob", "erp.sales.order.create"),
		auditRec("erp/sales", 7, 2, "business_change", "erp.sales.order", "o-1", "3", "alice", "erp.sales.order.edit"),
		auditRec("erp/sales", 6, 2, "sensitive_read", "erp.sales.order", "o-1", "", "bob", "erp.sales.order.read"),
		auditRec("erp/sales", 8, 3, "sensitive_read", "erp.sales.order", "o-2", "", "carol", "erp.sales.order.export"),
		auditRec("infra/authz", 4, 1, "security_change", "grant", "g-1", "", "dave", "infra.authz.security_record.change"),
		auditRec("erp/sales", 9, 4, "business_change", "", "", "", "erin", "erp.sales.price_list.import"),
	}
	asked, unnamed := AskOwners(records)
	assert.Equal(t, 1, unnamed)
	require.Len(t, asked, 2)
	assert.Equal(t, "o-1", asked[0].ID)
	assert.Equal(t, "3", asked[0].Version, "the highest version named")
	assert.Equal(t, "bob", asked[0].Records[0].Actor, "in the order they occurred")

	assert.Nil(t, Judge(asked[0], "present", "3"), "kept")
	lost := Judge(asked[0], "older", "2")
	require.NotNil(t, lost)
	assert.Equal(t, LossLost, lost.Verdict)
	assert.Equal(t, []string{"bob", "alice"}, lost.Users)
	assert.Equal(t, []string{"erp.sales.order.create", "erp.sales.order.edit", "erp.sales.order.read"}, lost.Operations)
	assert.Equal(t, "2026-11-05T10:01:00Z", lost.Since)
	assert.Equal(t, LossLost, Judge(asked[0], "absent", "").Verdict)
	assert.Equal(t, LossUnprovable, Judge(asked[1], "present", "4").Verdict, "no version named: cannot tell")
	assert.Equal(t, LossUnprovable, Judge(asked[0], "CAPABILITY_UNSUPPORTED", "").Verdict)
}

// P14.4: the gaps in each source's production sequence after T, and the window after its last record.
func TestTheAuditLoss(t *testing.T) {
	records := []AuditRecord{
		auditRec("erp/sales", 3, 1, "business_change", "t", "1", "", "", ""), auditRec("erp/sales", 7, 3, "business_change", "t", "1", "", "", ""),
		auditRec("erp/sales", 4, 2, "business_change", "t", "1", "", "", ""), auditRec("mdm/org", 1, 1, "business_change", "t", "1", "", "", ""),
	}
	got := AuditLoss(records, tRec.Add(time.Hour))
	require.Len(t, got, 2)
	assert.Equal(t, [][]int{{5, 6}}, got[0].Gaps)
	assert.EqualValues(t, 7, got[0].LastSeq)
	assert.Equal(t, "2026-11-05T10:03:00Z", got[0].LastAt)
	assert.Equal(t, "2026-11-05T11:00:00Z", got[0].WindowTo)
	assert.Equal(t, [][]int{}, got[1].Gaps)
}

// P14.5: the greater of the owner's maximum and the audit's; a series only the audit names is raised too.
func TestTheRaisePlan(t *testing.T) {
	num := func(r AuditRecord, le, code, period, n string) AuditRecord {
		r.Number = &DocumentNumber{LegalEntity: le, Code: code, Period: period, Number: n}
		return r
	}
	b := auditRec("erp/billing", 1, 1, "business_change", "erp.billing.invoice", "i", "2", "", "erp.billing.invoice.issue")
	all, raise := RaisePlan([]Series{{Owner: "erp/billing", LegalEntity: "le-1", Code: "INV", Period: "2026", Max: 10},
		{Owner: "erp/billing", LegalEntity: "le-2", Code: "INV", Period: "2026", Max: 5},
		{Owner: "erp/billing", LegalEntity: "le-4", Code: "INV", Period: "2026", Max: 7}},
		[]AuditRecord{num(b, "le-1", "INV", "2026", "12"), num(b, "le-1", "INV", "2026", "11"), num(b, "le-2", "INV", "2026", "4"),
			num(b, "le-3", "INV", "2026", "2"), num(b, "le-1", "INV", "2026", "x"), num(b, "le-4", "INV", "2026", "7")})
	assert.Len(t, all, 4)
	require.Len(t, raise, 2)
	assert.Equal(t, Series{Owner: "erp/billing", LegalEntity: "le-1", Code: "INV", Period: "2026", Max: 12, FromAudit: 12}, raise[0])
	assert.Equal(t, Series{Owner: "erp/billing", LegalEntity: "le-3", Code: "INV", Period: "2026", Max: 2, FromAudit: 2}, raise[1],
		"a series the owner no longer has")
}

// A stored audit record read back: target, version, the issued number.
func TestParseAudit(t *testing.T) {
	r, err := ParseAudit("erp/billing", "2.1", 9, []byte(`{"id":"a","occurred_at":"2026-11-05T10:01:00.000000Z",
		"category":"business_change","operation":"erp.billing.invoice.issue","actor":"alice",
		"target":{"type":"erp.billing.invoice","id":"i-1","version":"4"},
		"changes":{"after":{},"document_number":{"legal_entity":"le-1","code":"INV","period":"2026","number":"12"}}}`))
	require.NoError(t, err)
	assert.Equal(t, "i-1", r.TargetID)
	assert.Equal(t, "4", r.Version)
	assert.Equal(t, &DocumentNumber{LegalEntity: "le-1", Code: "INV", Period: "2026", Number: "12"}, r.Number)
	_, err = ParseAudit("x", "", 0, []byte(`{"occurred_at":"yesterday"}`))
	assert.Error(t, err)
}
