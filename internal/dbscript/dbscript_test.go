package dbscript

import (
	"regexp"
	"strings"
	"testing"

	"github.com/brickKit/be-ops/internal/projconf"
	"github.com/brickKit/be-ops/internal/testfixture"
)

const salesCfg = "PG_DATABASE: $var:PG_DATABASE\nPG_SCHEMA: sales\nPG_USER: sales_rt\nPG_OWNER_USER: sales_own\n" +
	"PG_PASSWORD_FILE: ${ERP_SALES_DB_PASSWORD}\nPG_OWNER_PASSWORD_FILE: ${ERP_SALES_DB_OWNER_PASSWORD}\n"

// project writes a project with erp/sales and mdm/customer (databases) hosted by be/go-core,
// and infra/bff-mobile without a database.
func project(t *testing.T, edits map[string]string) *projconf.Project {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"brickkit.yaml":         "components:\n  - {id: erp/sales, version: 3.0.0}\n  - {id: mdm/customer, version: 3.0.0}\n  - {id: infra/bff-mobile, version: 3.0.0}\n  - {id: be/go-core, version: 1.1.0}\n",
		"config/vars.yaml":      "PG_DATABASE: brickkit_db\n",
		"config/erp-sales.yaml": salesCfg,
		"config/mdm-customer.yaml": "PG_DATABASE: $var:PG_DATABASE\nPG_SCHEMA: customer\nPG_USER: cust_rt\nPG_OWNER_USER: cust_own\n" +
			"PG_PASSWORD_FILE: file://.secrets/mdm-customer/pg\nPG_OWNER_PASSWORD_FILE: ${MDM_CUSTOMER_DB_OWNER_PASSWORD}\n",
		"config/infra-bff-mobile.yaml":        "IAM_URL: x\n",
		"config/be-go-core.yaml":              "PG_DATABASE: $var:PG_DATABASE\nPG_USER: core_login\nPG_PASSWORD_FILE: ${BE_GO_CORE_DB_PASSWORD}\n",
		"shell/be/go-core/component.yaml":     "metadata: {id: be/go-core, version: 1.1.0}\nshell:\n  members: [erp/sales@3.0.0, mdm/customer@3.0.0]\n",
		"components/erp/sales/component.yaml": "metadata: {id: erp/sales, version: 3.0.0}\n",
	}
	for k, v := range edits {
		files[k] = v
	}
	for k, v := range files {
		testfixture.Write(t, dir, k, v)
	}
	p, err := projconf.Load(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func script(t *testing.T, p *projconf.Project, db string) string {
	t.Helper()
	plan, err := Build(p, db)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Render(plan)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRender_OwnerAndRuntimeRoles(t *testing.T) {
	s := script(t, project(t, nil), "")
	for _, want := range []string{
		`CREATE DATABASE "brickkit_db"`,
		`CREATE ROLE "sales_own" LOGIN`, `CREATE ROLE "sales_rt" LOGIN`,
		`ALTER ROLE "sales_rt" LOGIN PASSWORD :'ERP_SALES_DB_PASSWORD';`,
		`ALTER ROLE "sales_own" LOGIN PASSWORD :'ERP_SALES_DB_OWNER_PASSWORD';`,
		`CREATE SCHEMA IF NOT EXISTS "sales" AUTHORIZATION "sales_own";`,
		`ALTER SCHEMA "sales" OWNER TO "sales_own";`,
		`GRANT USAGE ON SCHEMA "sales" TO "sales_rt";`,
		`REVOKE CREATE ON SCHEMA "sales" FROM "sales_rt";`,
		`ALTER DEFAULT PRIVILEGES FOR ROLE "sales_own" IN SCHEMA "sales" GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO "sales_rt";`,
		`ALTER DEFAULT PRIVILEGES FOR ROLE "sales_own" IN SCHEMA "sales" GRANT USAGE, SELECT ON SEQUENCES TO "sales_rt";`,
		`ALTER ROLE "sales_rt" SET statement_timeout = '30s';`,
		`ALTER ROLE "sales_rt" SET idle_in_transaction_session_timeout = '60s';`,
		`ALTER ROLE "sales_rt" SET lock_timeout = '5s';`,
		`ALTER ROLE "core_login" SET statement_timeout = '30s';`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("script lacks %q", want)
		}
	}
	if strings.Contains(s, "_archive") || strings.Contains(s, "bff") {
		t.Error("no archive schema, and a component without a database gets nothing")
	}
}

func TestRender_ShellGetsMemberRuntimeRolesWithoutInherit(t *testing.T) {
	s := script(t, project(t, nil), "")
	for _, want := range []string{
		`GRANT "sales_rt" TO "core_login" WITH INHERIT FALSE, SET TRUE;`,
		`GRANT "cust_rt" TO "core_login" WITH INHERIT FALSE, SET TRUE;`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("script lacks %q", want)
		}
	}
	if regexp.MustCompile(`GRANT "(sales_own|cust_own)" TO`).MatchString(s) {
		t.Error("a shell is never granted an owner role")
	}
}

func TestRender_RuntimeIsNeverAMemberOfOwner(t *testing.T) {
	s := script(t, project(t, nil), "")
	if !strings.Contains(s, `REVOKE "sales_own" FROM "sales_rt"`) {
		t.Error("an existing owner membership of the runtime role must be revoked")
	}
}

func TestBuild_Refusals(t *testing.T) {
	cases := map[string]struct {
		edits map[string]string
		want  string
	}{
		"missing owner":     {map[string]string{"config/erp-sales.yaml": strings.Replace(salesCfg, "PG_OWNER_USER: sales_own\n", "", 1)}, "PG_OWNER_USER"},
		"owner is runtime":  {map[string]string{"config/erp-sales.yaml": strings.Replace(salesCfg, "sales_own", "sales_rt", 1)}, "differ"},
		"archive schema":    {map[string]string{"config/erp-sales.yaml": strings.Replace(salesCfg, "PG_SCHEMA: sales", "PG_SCHEMA: sales_archive", 1)}, "_archive"},
		"secret as literal": {map[string]string{"config/erp-sales.yaml": strings.Replace(salesCfg, "${ERP_SALES_DB_PASSWORD}", "hunter2", 1)}, "PG_PASSWORD_FILE"},
		"shared runtime": {map[string]string{"config/mdm-customer.yaml": "PG_DATABASE: brickkit_db\nPG_SCHEMA: customer\nPG_USER: sales_rt\nPG_OWNER_USER: cust_own\n" +
			"PG_PASSWORD_FILE: ${A}\nPG_OWNER_PASSWORD_FILE: ${B}\n"}, "sales_rt"},
		"shell login is an owner": {map[string]string{"config/be-go-core.yaml": "PG_DATABASE: brickkit_db\nPG_USER: sales_own\nPG_PASSWORD_FILE: ${X}\n"}, "sales_own"},
		"shell without login":     {map[string]string{"config/be-go-core.yaml": "PG_DATABASE: brickkit_db\n"}, "be/go-core"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Build(project(t, tc.edits), "")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("want an error naming %q, got %v", tc.want, err)
			}
		})
	}
}

func TestRender_QuotesIdentifiers(t *testing.T) {
	cfg := strings.Replace(salesCfg, "PG_USER: sales_rt", `PG_USER: 'odd"role'`, 1)
	s := script(t, project(t, map[string]string{"config/erp-sales.yaml": cfg}), "")
	if !strings.Contains(s, `"odd""role"`) || !strings.Contains(s, `rolname = 'odd"role'`) {
		t.Errorf("identifier not quoted:\n%s", s)
	}
}

func TestBuild_DatabaseOverrideAndPasswordVars(t *testing.T) {
	plan, err := Build(project(t, nil), "brickkit_test_db")
	if err != nil {
		t.Fatal(err)
	}
	s, _ := Render(plan)
	if !strings.Contains(s, `\connect "brickkit_test_db"`) || strings.Contains(s, `\connect "brickkit_db"`) {
		t.Error("database override not applied")
	}
	vars := strings.Join(PasswordVars(plan), "\n")
	for _, want := range []string{"ERP_SALES_DB_PASSWORD\tenv:ERP_SALES_DB_PASSWORD", "BE_GO_CORE_DB_PASSWORD\tenv:BE_GO_CORE_DB_PASSWORD",
		"pw_file_secrets_mdm_customer_pg\tfile:.secrets/mdm-customer/pg"} {
		if !strings.Contains(vars, want) {
			t.Errorf("password vars lack %q:\n%s", want, vars)
		}
	}
}
