package registry

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadPorts_跳过注释与空行(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "ports.tsv")
	writeFile(t, p, "# repo\tcomponent_id\thttp_port\tgrpc_port\tshell\tnote\n"+
		"\n"+
		"mdm-customer\tmdm/customer\t8080\t9090\tgo-core\t-\n"+
		"# ===== 分隔 =====\n"+
		"_infra-postgres\t-\t5432\t-\tout-of-band\t-\n")

	rows, err := LoadPorts(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("期望 2 行（注释与空行被跳过），得到 %d：%+v", len(rows), rows)
	}
	if rows[0].Repo != "mdm-customer" || rows[0].HTTPPort != "8080" || rows[0].Shell != "go-core" {
		t.Fatalf("第一行解析不对：%+v", rows[0])
	}
	if rows[1].Repo != "_infra-postgres" {
		t.Fatalf("第二行解析不对：%+v", rows[1])
	}
}

func TestLoadPorts_列数不对报错(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "ports.tsv")
	writeFile(t, p, "mdm-customer\tmdm/customer\t8080\n") // 只有 3 列，应为 6
	if _, err := LoadPorts(p); err == nil {
		t.Fatal("列数不对应该报错")
	}
}

func TestLoadSchemas_解析四列(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "schemas.tsv")
	writeFile(t, p, "# repo\tschema\trole\tshell_login_role\n"+
		"mdm-customer\tmdm_customer\tmdm_customer_rw\tshell_go_core\n"+
		"# 以下不建 schema\n")

	rows, err := LoadSchemas(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("期望 1 行，得到 %d", len(rows))
	}
	if rows[0].Schema != "mdm_customer" || rows[0].ShellLoginRole != "shell_go_core" {
		t.Fatalf("解析不对：%+v", rows[0])
	}
}
