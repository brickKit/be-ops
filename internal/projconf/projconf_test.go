package projconf

import (
	"testing"

	"github.com/brickKit/be-ops/internal/testfixture"
)

func project(t *testing.T) string {
	dir := t.TempDir()
	testfixture.Write(t, dir, "brickkit.yaml", "project: p\ncomponents:\n  - id: erp/sales\n    version: 3.0.0\n  - id: be/go-core\n    version: 1.1.0\n")
	testfixture.Write(t, dir, "config/vars.yaml", "PG_DATABASE: brickkit_db\nSALES_OWNER: sales_owner\n")
	testfixture.Write(t, dir, "config/erp-sales.yaml", "PG_DATABASE: $var:PG_DATABASE  # string\nPG_USER: sales_rt\nPG_OWNER_USER: $var:SALES_OWNER\n"+
		"PG_PASSWORD_FILE: ${ERP_SALES_DB_PASSWORD}  # string | secret\nPG_OWNER_PASSWORD_FILE: file://.secrets/erp-sales/owner\nPG_PORT: 5432\n")
	testfixture.Write(t, dir, "deploy.yaml", "target: docker\nvars:\n  PG_DATABASE: other_db\ncomponents: []\n")
	return dir
}

func TestLoad_InstalledComponentsAndValues(t *testing.T) {
	p, err := Load(project(t), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Components) != 2 || p.Components[0].ID != "erp/sales" || p.Components[1].Version != "1.1.0" {
		t.Errorf("components = %+v", p.Components)
	}
	v, err := p.Resolve("erp/sales", "PG_OWNER_USER")
	if err != nil || v.Kind != Literal || v.Value != "sales_owner" {
		t.Errorf("$var resolves: %+v %v", v, err)
	}
	if v, _ := p.Resolve("erp/sales", "PG_PASSWORD_FILE"); v.Kind != Env || v.Value != "ERP_SALES_DB_PASSWORD" {
		t.Errorf("${VAR} = %+v", v)
	}
	if v, _ := p.Resolve("erp/sales", "PG_OWNER_PASSWORD_FILE"); v.Kind != File || v.Value != ".secrets/erp-sales/owner" {
		t.Errorf("file:// = %+v", v)
	}
	if v, _ := p.Resolve("erp/sales", "PG_PORT"); v.Value != "5432" {
		t.Errorf("number = %+v", v)
	}
	if v, _ := p.Resolve("erp/sales", "NOPE"); v.Kind != Absent {
		t.Errorf("absent = %+v", v)
	}
	if _, err := p.Resolve("be/go-core", "PG_USER"); err != nil {
		t.Errorf("a component without a config file has every key absent, got %v", err)
	}
}

func TestLoad_DeployVarsOverrideSharedVars(t *testing.T) {
	dir := project(t)
	p, err := Load(dir, dir+"/deploy.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := p.Resolve("erp/sales", "PG_DATABASE"); v.Value != "other_db" {
		t.Errorf("deploy vars win: %+v", v)
	}
}

func TestResolve_UndefinedVarIsAnError(t *testing.T) {
	dir := project(t)
	testfixture.Write(t, dir, "config/erp-sales.yaml", "PG_USER: $var:MISSING\n")
	p, _ := Load(dir, "")
	if _, err := p.Resolve("erp/sales", "PG_USER"); err == nil {
		t.Error("an undefined $var must be an error")
	}
}
