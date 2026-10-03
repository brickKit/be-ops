package authzdecl

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brickKit/be-ops/internal/component"
	"github.com/brickKit/be-ops/internal/testfixture"
)

func TestGenTypes_AppendsNewKeepsOldWarnsOrphans(t *testing.T) {
	existing := []TypeRow{{Type: "erp.sales.order", Owner: "erp/sales", Table: "sales_orders"}}
	rows, warns, err := GenTypes(existing, []*component.Component{widget(t)})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Type != "erp.sales.order" || rows[1] != (TypeRow{Type: "conformance.widget.widget", Owner: "conformance/widget", Table: "widgets"}) {
		t.Errorf("rows = %+v", rows)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "erp.sales.order") {
		t.Errorf("want one orphan warning, got %v", warns)
	}
	if _, _, err := GenTypes([]TypeRow{{Type: "conformance.widget.widget", Owner: "x/y"}}, []*component.Component{widget(t)}); err == nil {
		t.Error("an owner change must be an error")
	}
}

func TestTypesTSV_RoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "resource-types.tsv")
	rows := []TypeRow{{Type: "a.b.c", Owner: "a/b", Table: "cs", Deprecated: ""}, {Type: "a.b.d", Owner: "a/b", Table: "ds", Deprecated: "replaced by a.b.e"}}
	if err := WriteTypesTSV(p, rows); err != nil {
		t.Fatal(err)
	}
	got, err := ReadTypesTSV(p)
	if err != nil || len(got) != 2 || got[1] != rows[1] {
		t.Errorf("round trip = %+v %v", got, err)
	}
	if !strings.HasPrefix(testfixture.Read(t, filepath.Dir(p), "resource-types.tsv"), "type\towner_component\ttable\tdeprecated\n") {
		t.Error("header wrong")
	}
	if missing, err := ReadTypesTSV(filepath.Join(t.TempDir(), "none.tsv")); err != nil || missing != nil {
		t.Errorf("a missing registry is empty, got %v %v", missing, err)
	}
}

func TestCatalog_WidgetShapeAndDeterminism(t *testing.T) {
	cs := []*component.Component{widget(t)}
	a, err := Catalog(cs)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Catalog(cs)
	if string(a) != string(b) {
		t.Error("catalog is not deterministic")
	}
	var doc struct {
		ResourceTypes []struct {
			Type, OwnerComponent, ViewKey, Derivation string
			Keys                                      []string
			Fields                                    []map[string]any
			Share                                     map[string]any
		} `json:"resource_types"`
	}
	if err := json.Unmarshal(a, &doc); err != nil {
		t.Fatal(err)
	}
	r := doc.ResourceTypes
	if len(r) != 1 || r[0].Type != "conformance.widget.widget" || r[0].Derivation != "direct" || len(r[0].Fields) != 1 || r[0].Share == nil {
		t.Errorf("catalog = %s", a)
	}
	if !strings.Contains(string(a), `"owner_component": "conformance/widget"`) || !strings.Contains(string(a), `"view_key": "conformance.widget.view"`) {
		t.Errorf("catalog lacks owner or view_key: %s", a)
	}
}

func TestCheckSubjects_LifecycleSubjectsAreRegistered(t *testing.T) {
	cs := []*component.Component{widget(t)}
	p, err := CheckSubjects([]SubjectRow{{Subject: "user", Owner: "infra/iam-casdoor"}}, cs)
	if err != nil {
		t.Fatal(err)
	}
	if len(p) != 1 || !strings.Contains(p[0], "owner") {
		t.Errorf("want the unregistered subject owner, got %v", p)
	}
	p, _ = CheckSubjects([]SubjectRow{{Subject: "user", Owner: "infra/iam-casdoor"}, {Subject: "owner", Owner: "conformance/peer"}}, cs)
	if len(p) != 0 {
		t.Errorf("all registered: %v", p)
	}
	dup, _ := CheckSubjects([]SubjectRow{{Subject: "user"}, {Subject: "user"}}, nil)
	if len(dup) != 1 {
		t.Errorf("a duplicate subject must be reported: %v", dup)
	}
}

func TestSubjectsTSV_Read(t *testing.T) {
	dir := t.TempDir()
	testfixture.Write(t, dir, "data-subjects.tsv", "subject\towner\tdescription\ncustomer\tmdm/customer\ta customer\n")
	rows, err := ReadSubjectsTSV(filepath.Join(dir, "data-subjects.tsv"))
	if err != nil || len(rows) != 1 || rows[0].Owner != "mdm/customer" {
		t.Errorf("rows = %+v %v", rows, err)
	}
}
