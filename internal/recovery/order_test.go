package recovery

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rec seals nothing: the replay order reads only the record's fields.
func rec(t *testing.T, order int, id, owner, category, phase, epoch string, seq int64) Evidence {
	t.Helper()
	r := map[string]any{"record_type": "be.evidence.record", "id": id, "recorder": map[string]any{"component": owner},
		"category": category, "phase": phase}
	if epoch != "" {
		r["position"] = map[string]any{"epoch": epoch, "commit_seq": seq}
	}
	raw, err := json.Marshal(r)
	require.NoError(t, err)
	e, err := ParseEvidence(raw, order)
	require.NoError(t, err)
	return e
}

func order(ps []Planned) []string {
	out := []string{}
	for _, p := range ps {
		out = append(out, p.ID)
	}
	return out
}

// Protocol 15's example: a grant (107) and its revocation (108) a second later; the revocation's intent was appended
// before the grant's general record. By commit sequence the replay is "grant, revoke", not the append order.
func TestTheReplayGoesByCommitSequenceNotByAppendOrder(t *testing.T) {
	p := PlanReplay([]Evidence{
		rec(t, 1, "revoke", AuthzID, "security_tightening", "intent", "", 0),
		rec(t, 2, "grant", AuthzID, "security_widening", "general", "1.1", 107),
		rec(t, 3, "revoke", AuthzID, "security_tightening", "effective", "1.1", 108),
	})
	assert.Equal(t, []string{"grant", "revoke"}, order(p.Authz))
	assert.Empty(t, p.AuthzDeferred, "the revocation has its conclusion: not intent-only")
}

func TestThePlanGroupsOrdersAndSetsApart(t *testing.T) {
	p := PlanReplay([]Evidence{
		rec(t, 1, "a-intent", AuthzID, "security_tightening", "intent", "", 0),
		rec(t, 2, "o-late", "mdm/org", "org_tightening", "effective", "2.1", 1), // a newer generation
		rec(t, 3, "o-early", "mdm/org", "org_tightening", "effective", "1.1", 99),
		rec(t, 4, "o-only", "mdm/org", "org_tightening", "intent", "", 0),
		rec(t, 5, "i-cancel", "infra/iam-casdoor", "account_status", "intent", "", 0),
		rec(t, 6, "i-cancel", "infra/iam-casdoor", "account_status", "cancelled", "", 0),
		rec(t, 7, "i-off", "infra/iam-casdoor", "account_status", "effective", "1.1", 5),
		rec(t, 8, "i-off", "infra/iam-casdoor", "account_status", "effective", "1.1", 5), // the alert channel's copy
		rec(t, 9, "a-grant", AuthzID, "security_widening", "general", "1.1", 3),
		rec(t, 10, "a-only-widen", AuthzID, "security_widening", "intent", "", 0),
		rec(t, 11, "i-lost", "infra/iam-casdoor", DoubleFailureCategory, "general", "1.1", 6), // P12.23: not replayed
	})
	assert.Equal(t, []string{"i-off", "o-early", "o-late", "o-only"}, order(p.Owners),
		"by owner; each by (generation, timeline, sequence); intent-only last; a copy once")
	assert.True(t, p.Owners[3].IntentOnly)
	assert.Equal(t, []string{"a-grant", "a-only-widen"}, order(p.Authz), "the authorization member's own, last")
	assert.Equal(t, []string{"a-intent"}, order(p.AuthzDeferred), "its intent-only tightenings wait for 6.4-6.5")
	assert.Equal(t, []Ignored{{ID: "i-cancel", Owner: "infra/iam-casdoor", Reason: "cancelled"},
		{ID: "i-lost", Owner: "infra/iam-casdoor", Reason: "a double failure: listed in step 9"}}, p.Ignored)
}

func TestARecordWithoutAnIdentityOrAWrongEpochIsRefused(t *testing.T) {
	_, err := ParseEvidence([]byte(`{"record_type":"be.evidence.record","phase":"intent"}`), 0)
	assert.Error(t, err)
	_, err = ParseEvidence([]byte(`{"record_type":"be.evidence.record","id":"x","recorder":{"component":"c"},"phase":"effective",
		"position":{"epoch":"7","commit_seq":1}}`), 0)
	assert.ErrorContains(t, err, "generation")
}
