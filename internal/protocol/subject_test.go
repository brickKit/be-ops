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

func TestAssemblySchema_CapabilitiesAndTypePattern(t *testing.T) {
	s, err := LoadAssemblySchema()
	if err != nil {
		t.Fatal(err)
	}
	if !s.IsCapability("sharing") || !s.IsCapability("graph") || s.IsCapability("teleport") {
		t.Errorf("capabilities = %v", s.Capabilities)
	}
	if !s.ResourceType.MatchString("erp.sales.order") || s.ResourceType.MatchString("erp.sales") {
		t.Error("resource type pattern wrong")
	}
	if !s.PermKey.MatchString("erp.sales.view") || s.PermKey.MatchString("Erp.sales") {
		t.Error("perm key pattern wrong")
	}
}
