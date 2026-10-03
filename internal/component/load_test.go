package component

import (
	"testing"

	"github.com/brickKit/be-ops/internal/testfixture"
)

func TestLoad_WidgetManifestAndAssembly(t *testing.T) {
	c, err := Load(testfixture.Copy(t, "widget"))
	if err != nil {
		t.Fatal(err)
	}
	if c.ID != "conformance/widget" || c.Version != "1.0.0" {
		t.Errorf("id/version = %s %s", c.ID, c.Version)
	}
	if c.Port != 8080 || len(c.ExtraPorts) != 1 || c.ExtraPorts[0].Name != "grpc" {
		t.Errorf("ports = %d %+v", c.Port, c.ExtraPorts)
	}
	p, ok := c.Prop("PG_PASSWORD_FILE")
	if !ok || !p.Secret || p.Mount != "file" || p.Type != "string" {
		t.Errorf("PG_PASSWORD_FILE prop = %+v %v", p, ok)
	}
	port, _ := c.Prop("PG_PORT")
	if port.Default == nil || *port.Default != "5432" {
		t.Errorf("PG_PORT default = %v", port.Default)
	}
	if !c.IsRequired("PG_SCHEMA") || c.IsRequired("PG_PORT") {
		t.Error("required list not read")
	}
	if len(c.Events.Publishes) != 3 || len(c.Events.Subscribes) != 2 || !c.Events.Declared {
		t.Errorf("events = %+v", c.Events)
	}
	a := c.Assembly
	if a.Protocol != "1.0" || len(a.EdgeRoutes) != 2 || a.EdgeRoutes[1].Timeout != 20 {
		t.Errorf("assembly = %+v", a)
	}
	if len(a.Resources) != 1 || a.Resources[0].ViewKey != "conformance.widget.view" || a.Resources[0].Share == nil {
		t.Errorf("resources = %+v", a.Resources)
	}
	share, _ := a.Permission("conformance.widget.share")
	if share.Delegable == nil || *share.Delegable {
		t.Errorf("share delegable = %v", share.Delegable)
	}
	if c.FixturesPath() != c.Dir+"/conformance/fixtures.yaml" {
		t.Errorf("fixtures path = %s", c.FixturesPath())
	}
}

func TestLoad_EventContractsFixturesAndOpenAPI(t *testing.T) {
	c, err := Load(testfixture.Copy(t, "widget"))
	if err != nil {
		t.Fatal(err)
	}
	evs, err := c.EventContracts()
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 3 || evs[0].Subject != "conformance.widget.created.v1" {
		t.Errorf("event contract = %+v", evs)
	}
	fx, err := c.Fixtures()
	if err != nil {
		t.Fatal(err)
	}
	if !fx.Present || len(fx.Consumes) != 2 || fx.Consumes[0] != "conformance.owner.updated.v1" {
		t.Errorf("fixtures consumes = %+v", fx)
	}
	ops, err := c.OpenAPIOperations()
	if err != nil {
		t.Fatal(err)
	}
	var kinds, widgets bool
	for _, o := range ops {
		if o.Path == "/conformance/widget/kinds" && o.Method == "get" && o.Permission == "public" {
			kinds = true
		}
		if o.Path == "/conformance/widget/widgets/{id}/approve" && o.Permission == "conformance.widget.approve" && o.DeadlineSeconds == 15 {
			widgets = true
		}
	}
	if !kinds || !widgets {
		t.Errorf("openapi operations not read with server prefix: %+v", ops)
	}
}

func TestLoad_SignalsAreMarkedNotPublished(t *testing.T) {
	dir := testfixture.Copy(t, "peer")
	testfixture.Write(t, dir, "contracts/events/x.events.json", `{"events":[
	  {"subject":"conformance.peer.changed.v1","x-aggregate-type":"conformance.peer.x","x-signal":true,"payload":{"type":"object"}},
	  {"subject":"conformance.peer.poked.v1","x-aggregate-type":"conformance.peer.x","transport":"core NATS","payload":{"type":"object"}}],
	  "x-signals":[{"subject":"conformance.peer.other.v1"}]}`)
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	evs, err := c.EventContracts()
	if err != nil {
		t.Fatal(err)
	}
	signals := 0
	for _, e := range evs {
		if e.Signal {
			signals++
		}
	}
	if signals != 2 {
		t.Errorf("want the two marked entries as signals, got %+v", evs)
	}
}
