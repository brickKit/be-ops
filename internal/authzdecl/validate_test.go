package authzdecl

import (
	"strings"
	"testing"

	"github.com/brickKit/be-ops/internal/component"
	"github.com/brickKit/be-ops/internal/testfixture"
)

// widget returns the widget fixture with its assembly.yaml edited by the given replacements.
func widget(t *testing.T, repl ...string) *component.Component {
	t.Helper()
	dir := testfixture.Copy(t, "widget")
	src := testfixture.Read(t, dir, "assembly.yaml")
	for i := 0; i+1 < len(repl); i += 2 {
		if !strings.Contains(src, repl[i]) {
			t.Fatalf("fixture lacks %q", repl[i])
		}
		src = strings.Replace(src, repl[i], repl[i+1], 1)
	}
	testfixture.Write(t, dir, "assembly.yaml", src)
	c, err := component.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func validate(t *testing.T, cs []*component.Component, reg []TypeRow) string {
	t.Helper()
	p, err := Validate(cs, reg)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(p, "\n")
}

func TestValidate_WidgetIsValid(t *testing.T) {
	if p := validate(t, []*component.Component{widget(t)}, nil); p != "" {
		t.Errorf("widget should be valid:\n%s", p)
	}
}

func TestValidate_Refusals(t *testing.T) {
	cases := []struct {
		name string
		repl []string
		want string
	}{
		{"view_key missing", []string{"    view_key: conformance.widget.view\n", ""}, "view_key"},
		{"view_key not among keys", []string{"    view_key: conformance.widget.view", "    view_key: conformance.widget.create"}, "view_key"},
		{"undeclared key", []string{"keys: [conformance.widget.view,", "keys: [conformance.widget.fly, conformance.widget.view,"}, "conformance.widget.fly"},
		{"field key used as a key", []string{"keys: [conformance.widget.view,", "keys: [conformance.widget.price.read, conformance.widget.view,"}, "field"},
		{"field read not a field key", []string{"read: conformance.widget.price.read", "read: conformance.widget.view"}, "fields"},
		{"share key delegable", []string{"type: action, delegable: false }", "type: action }"}, "delegable"},
		{"share relation unknown", []string{"relations: [viewer, editor]", "relations: [viewer, owner]"}, "owner"},
		{"grant outside keys", []string{"grants: [conformance.widget.view] }", "grants: [conformance.widget.create] }"}, "conformance.widget.create"},
		{"include unknown", []string{"includes: [viewer]", "includes: [reader]"}, "reader"},
		{"dimension not scoped", []string{"dimensions: [owner, org, region]", "dimensions: [owner, org, warehouse]"}, "warehouse"},
		{"graph needs capability", []string{"derivation: direct", "derivation: graph"}, "graph"},
		{"unknown capability", []string{"requires_capabilities: []", "requires_capabilities: [teleport]"}, "teleport"},
		{"bad type name", []string{"type: conformance.widget.widget", "type: conformance.Widget"}, "conformance.Widget"},
		{"inherit from unknown type", []string{"inherits: []", "inherits: [{from: erp.sales.order, via: order_id, relation: viewer, as: viewer}]"}, "erp.sales.order"},
		{"bad permission type", []string{"type: page }", "type: screen }"}, "screen"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := validate(t, []*component.Component{widget(t, tc.repl...)}, nil)
			if !strings.Contains(p, tc.want) {
				t.Errorf("want a problem naming %q, got:\n%s", tc.want, p)
			}
		})
	}
}

func TestValidate_OneOwnerPerType(t *testing.T) {
	a := widget(t)
	b := widget(t, "id: conformance/widget", "id: conformance/widget2")
	b.ID = "conformance/widget2"
	if p := validate(t, []*component.Component{a, b}, nil); !strings.Contains(p, "conformance/widget2") {
		t.Errorf("two owners of one type must be refused:\n%s", p)
	}
	reg := []TypeRow{{Type: "conformance.widget.widget", Owner: "conformance/other"}}
	if p := validate(t, []*component.Component{a}, reg); !strings.Contains(p, "conformance/other") {
		t.Errorf("a registered owner must not change:\n%s", p)
	}
}

func TestValidate_InheritFromRegisteredType(t *testing.T) {
	c := widget(t, "inherits: []", "inherits: [{from: erp.sales.order, via: order_id, relation: viewer, as: viewer}]")
	reg := []TypeRow{{Type: "erp.sales.order", Owner: "erp/sales"}}
	if p := validate(t, []*component.Component{c}, reg); p != "" {
		t.Errorf("a registered parent type is valid:\n%s", p)
	}
}
