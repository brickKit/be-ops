package dbscript

import (
	"strings"
	"testing"

	beprotocol "github.com/brickKit/be-protocol"
)

const pgBus = "postgres://be-postgres:5432/brickkit_db?schema=be_bus"

// busProject is the test project with the PostgreSQL queue: erp/sales and the shell use it,
// mdm/customer declares no bus.
func busProject(t *testing.T, edits map[string]string) map[string]string {
	t.Helper()
	files := map[string]string{
		"config/vars.yaml":       "PG_DATABASE: brickkit_db\nEVENT_BUS_URL: " + pgBus + "\n",
		"config/erp-sales.yaml":  salesCfg + "EVENT_BUS_URL: $var:EVENT_BUS_URL\n",
		"config/be-go-core.yaml": "PG_DATABASE: $var:PG_DATABASE\nPG_USER: core_login\nPG_PASSWORD_FILE: ${BE_GO_CORE_DB_PASSWORD}\nEVENT_BUS_URL: $var:EVENT_BUS_URL\n",
	}
	for k, v := range edits {
		files[k] = v
	}
	return files
}

func TestRender_NoBusSchemaOnNATS(t *testing.T) {
	s := script(t, project(t, map[string]string{"config/erp-sales.yaml": salesCfg + "EVENT_BUS_URL: nats://be-nats:4222\n"}), "")
	if strings.Contains(s, "be_bus") {
		t.Error("a project on NATS gets no be_bus schema")
	}
}

func TestRender_BusSchemaFromTheProtocolDDLWithOwnerAndGrants(t *testing.T) {
	s := script(t, project(t, busProject(t, nil)), "")
	ddl, err := beprotocol.FS.ReadFile("ddl/be_bus.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		string(ddl),
		`CREATE ROLE "be_bus_owner" NOLOGIN`,
		`CREATE TABLE IF NOT EXISTS be_bus.message_default PARTITION OF be_bus.message DEFAULT;`,
		`ALTER SCHEMA be_bus OWNER TO "be_bus_owner";`,
		`REVOKE ALL ON SCHEMA be_bus FROM PUBLIC;`,
		`GRANT USAGE ON SCHEMA be_bus TO "sales_rt";`,
		`GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA be_bus TO "sales_rt";`,
		`GRANT USAGE ON SEQUENCE be_bus.message_seq TO "sales_rt";`,
		`GRANT USAGE ON SCHEMA be_bus TO "core_login";`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("script lacks %q", want)
		}
	}
	if strings.Contains(s, `be_bus TO "cust_rt"`) {
		t.Error("a component that declares no bus gets no be_bus grant")
	}
	if strings.Contains(s, `be_bus TO "sales_own"`) {
		t.Error("owner roles never get be_bus grants")
	}
	if i, j := strings.Index(s, `\connect "brickkit_db"`), strings.Index(s, string(ddl)); i < 0 || j < i {
		t.Error("the bus DDL runs inside the bus database")
	}
}

func TestBuild_BusRefusals(t *testing.T) {
	cases := map[string]struct {
		edits map[string]string
		want  string
	}{
		"mixed adapters": {map[string]string{"config/mdm-customer.yaml": "PG_DATABASE: $var:PG_DATABASE\nPG_SCHEMA: customer\nPG_USER: cust_rt\nPG_OWNER_USER: cust_own\n" +
			"PG_PASSWORD_FILE: ${A}\nPG_OWNER_PASSWORD_FILE: ${B}\nEVENT_BUS_URL: nats://be-nats:4222\n"}, "same"},
		"other schema": {map[string]string{"config/vars.yaml": "PG_DATABASE: brickkit_db\nEVENT_BUS_URL: postgres://h:5432/brickkit_db?schema=bus\n"}, "be_bus"},
		"no database":  {map[string]string{"config/vars.yaml": "PG_DATABASE: brickkit_db\nEVENT_BUS_URL: postgres://h:5432?schema=be_bus\n"}, "database"},
		"not literal":  {map[string]string{"config/vars.yaml": "PG_DATABASE: brickkit_db\nEVENT_BUS_URL: ${BUS}\n"}, "literal"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Build(project(t, busProject(t, tc.edits)), "")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("want an error naming %q, got %v", tc.want, err)
			}
		})
	}
}

func TestBuild_DatabaseOverrideMovesTheBus(t *testing.T) {
	s := script(t, project(t, busProject(t, nil)), "brickkit_test_db")
	if !strings.Contains(s, `\connect "brickkit_test_db"`) || strings.Contains(s, `\connect "brickkit_db"`) {
		t.Error("the test database carries its own bus")
	}
}
