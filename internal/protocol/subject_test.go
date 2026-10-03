package protocol

import "testing"

func TestSubjectPattern_FromEnvelopeSchema(t *testing.T) {
	re, err := SubjectPattern()
	if err != nil {
		t.Fatal(err)
	}
	for _, ok := range []string{"conformance.widget.created.v1", "crm.lead.stage_changed.v2"} {
		if !re.MatchString(ok) {
			t.Errorf("%s should match", ok)
		}
	}
	for _, bad := range []string{"sales.created.v1", "erp.sales.order-created.v1", "erp.sales.created.v0", "erp.sales.created"} {
		if re.MatchString(bad) {
			t.Errorf("%s should not match", bad)
		}
	}
}
