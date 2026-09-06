package registry

import (
	"strings"
	"testing"
)

func TestCheck_端口重复报错(t *testing.T) {
	ports := []PortRow{
		{Repo: "mdm-customer", HTTPPort: "8080", GRPCPort: "9090"},
		{Repo: "erp-sales", HTTPPort: "8080", GRPCPort: "9094"}, // 撞了 mdm-customer 的 HTTP
	}
	errs := Check(ports, nil)
	if !containsSubstr(errs, "8080") {
		t.Fatalf("应该报出端口重复，实际：%v", errs)
	}
}

func TestCheck_frontend族共用80放行(t *testing.T) {
	// 只测端口重复与"用 80 的必须是谁"这两条，不走聚合 Check()——
	// 那会连带触发组件数核对（这里只有 2 行，不是 62），污染这条测试
	// 本来要盯的东西。
	ports := []PortRow{
		{Repo: "frontend-standard", HTTPPort: "80", GRPCPort: "-"},
		{Repo: "frontend-advanced", HTTPPort: "80", GRPCPort: "-"},
	}
	if errs := checkPortDuplicates(ports); len(errs) != 0 {
		t.Fatalf("frontend 族共用 80 不该报端口重复，实际：%v", errs)
	}
	if errs := checkPort80Owners(ports); len(errs) != 0 {
		t.Fatalf("frontend 族共用 80 不该报「非 frontend 占用」，实际：%v", errs)
	}
}

func TestCheck_带外容器互斥对共用端口放行(t *testing.T) {
	// ⚠️ 这条是 Task 4 实测修出来的：_infra- 之间是互斥替换件，允许同端口
	ports := []PortRow{
		{Repo: "_infra-rustfs", HTTPPort: "9000", GRPCPort: "-"},
		{Repo: "_infra-minio", HTTPPort: "9000", GRPCPort: "-"},
	}
	if errs := checkPortDuplicates(ports); len(errs) != 0 {
		t.Fatalf("_infra- 之间共用端口不该报错，实际：%v", errs)
	}
}

func TestCheck_组件与带外容器撞端口仍报错(t *testing.T) {
	ports := []PortRow{
		{Repo: "mdm-customer", HTTPPort: "5432", GRPCPort: "9090"},
		{Repo: "_infra-postgres", HTTPPort: "5432", GRPCPort: "-"},
	}
	errs := Check(ports, nil)
	if !containsSubstr(errs, "5432") {
		t.Fatalf("组件与带外容器撞端口应该报错，实际：%v", errs)
	}
}

func TestCheck_非frontend占用80报错(t *testing.T) {
	ports := []PortRow{{Repo: "mdm-customer", HTTPPort: "80", GRPCPort: "-"}}
	errs := Check(ports, nil)
	if !containsSubstr(errs, "80") {
		t.Fatalf("非 frontend 占用 80 应该报错，实际：%v", errs)
	}
}

func TestCheck_schemas里的repo必须在ports里(t *testing.T) {
	ports := []PortRow{{Repo: "mdm-customer", HTTPPort: "8080", GRPCPort: "9090"}}
	schemas := []SchemaRow{{Repo: "ghost-component", Schema: "ghost", Role: "ghost_rw", ShellLoginRole: "shell_go_core"}}
	errs := Check(ports, schemas)
	if !containsSubstr(errs, "ghost-component") {
		t.Fatalf("schemas 里多出的 repo 应该报错，实际：%v", errs)
	}
}

func TestCheck_schema与role命名规则(t *testing.T) {
	ports := []PortRow{{Repo: "mdm-customer", HTTPPort: "8080", GRPCPort: "9090"}}
	schemas := []SchemaRow{{Repo: "mdm-customer", Schema: "wrong_name", Role: "mdm_customer_rw", ShellLoginRole: "shell_go_core"}}
	errs := Check(ports, schemas)
	if !containsSubstr(errs, "mdm-customer") {
		t.Fatalf("schema 命名不对应该报错，实际：%v", errs)
	}
}

func TestCheck_shell与shell_login_role不一致报错(t *testing.T) {
	// 第 7 条：ports.tsv 的 shell 列与 schemas.tsv 的 shell_login_role 列必须机械对应
	ports := []PortRow{{Repo: "mdm-customer", HTTPPort: "8080", GRPCPort: "9090", Shell: "go-core"}}
	schemas := []SchemaRow{{Repo: "mdm-customer", Schema: "mdm_customer", Role: "mdm_customer_rw", ShellLoginRole: "shell_go_backoffice"}}
	errs := Check(ports, schemas)
	if !containsSubstr(errs, "mdm-customer") {
		t.Fatalf("shell 与 shell_login_role 不一致应该报错，实际：%v", errs)
	}
}

func TestCheck_shell与shell_login_role一致时放行(t *testing.T) {
	ports := []PortRow{{Repo: "mdm-customer", HTTPPort: "8080", GRPCPort: "9090", Shell: "go-core"}}
	schemas := []SchemaRow{{Repo: "mdm-customer", Schema: "mdm_customer", Role: "mdm_customer_rw", ShellLoginRole: "shell_go_core"}}
	if errs := checkShellConsistency(ports, schemas); len(errs) != 0 {
		t.Fatalf("shell 与 shell_login_role 一致不该报错，实际：%v", errs)
	}
}

// standalone/out-of-band 的行没有 schemas.tsv 对应条目——第 7 条不该
// 因为"找不到对应 schema 行"就报错，那是另一条检查（组件数核对）的事。
func TestCheck_standalone与out_of_band不参与shell一致性检查(t *testing.T) {
	ports := []PortRow{
		{Repo: "frontend-standard", HTTPPort: "80", GRPCPort: "-", Shell: "standalone"},
		{Repo: "_infra-postgres", HTTPPort: "5432", GRPCPort: "-", Shell: "out-of-band"},
	}
	if errs := checkShellConsistency(ports, nil); len(errs) != 0 {
		t.Fatalf("standalone/out-of-band 不该被第 7 条检查误伤，实际：%v", errs)
	}
}

func containsSubstr(errs []string, sub string) bool {
	for _, e := range errs {
		if strings.Contains(e, sub) {
			return true
		}
	}
	return false
}

func TestCheck_组件数不对62报错(t *testing.T) {
	ports := []PortRow{
		{Repo: "mdm-customer", HTTPPort: "8080", GRPCPort: "9090"},
		{Repo: "_infra-postgres", HTTPPort: "5432", GRPCPort: "-"}, // 不计入组件数
	}
	errs := Check(ports, nil)
	if !containsSubstr(errs, "62") {
		t.Fatalf("组件行数不是 62 应该报错，实际：%v", errs)
	}
}
