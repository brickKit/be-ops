package protocol

import "testing"

func TestLoadCatalogue_ReadsPinnedConfigKeys(t *testing.T) {
	cat, err := LoadCatalogue()
	if err != nil {
		t.Fatal(err)
	}
	k, ok := cat.Key("PG_PASSWORD_FILE")
	if !ok {
		t.Fatal("PG_PASSWORD_FILE missing from the catalogue")
	}
	if !k.Secret || k.Mount != "file" || !k.Required || k.Type != "string" {
		t.Errorf("PG_PASSWORD_FILE = %+v, want secret file-mounted required string", k)
	}
	if !k.InProfile("db") {
		t.Errorf("PG_PASSWORD_FILE profiles = %v, want db", k.Profiles)
	}
	port, _ := cat.Key("PG_PORT")
	if port.Default == nil || *port.Default != "5432" || port.Type != "integer" {
		t.Errorf("PG_PORT = %+v", port)
	}
	sch, _ := cat.Key("PG_SCHEMA")
	if sch.Default != nil {
		t.Errorf("PG_SCHEMA must have no default, got %q", *sch.Default)
	}
	agrpc, _ := cat.Key("AUTHZ_GRPC_URL")
	if len(agrpc.Profiles) != 0 || agrpc.AppliesWhen == "" {
		t.Errorf("AUTHZ_GRPC_URL must be an applies_when key: %+v", agrpc)
	}
	pool, _ := cat.Key("PG_POOL_MAX")
	if pool.ShellDefault == nil || *pool.ShellDefault != "40" {
		t.Errorf("PG_POOL_MAX shell default = %v", pool.ShellDefault)
	}
	bus, _ := cat.Key("EVENT_BUS_URL")
	if len(bus.OneOf) != 2 {
		t.Errorf("EVENT_BUS_URL one_of = %v", bus.OneOf)
	}
}

func TestLoadCatalogue_RetiredAndReserved(t *testing.T) {
	cat, err := LoadCatalogue()
	if err != nil {
		t.Fatal(err)
	}
	if r, ok := cat.RetiredKey("AUTHZ_BUNDLE_URL"); !ok || r.ReplacedBy != "AUTHZ_URL" {
		t.Errorf("AUTHZ_BUNDLE_URL retired = %+v %v", r, ok)
	}
	if !cat.IsReserved("COMPONENT_ID") || !cat.IsReserved("ERP_SALES_ENDPOINT") || cat.IsReserved("PG_HOST") {
		t.Error("reserved-name rules wrong")
	}
	if cat.Secrets.Suffix != "_FILE" || cat.Secrets.Mount != "file" {
		t.Errorf("secrets = %+v", cat.Secrets)
	}
	if !cat.MatchesPattern("WIDGET_NO_FORMAT") || cat.MatchesPattern("WIDGET_FORMAT") {
		t.Error("pattern _NO_FORMAT not recognised")
	}
}

func TestParseCatalogue_RejectsUnknownProtocolVersion(t *testing.T) {
	if _, err := ParseCatalogue([]byte("protocol: \"2.0\"\nkeys: []\n")); err == nil {
		t.Fatal("want an error for protocol 2.0")
	}
}
