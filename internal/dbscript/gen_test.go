package dbscript

import (
	"strings"
	"testing"
)

func TestGen_每组件三样东西(t *testing.T) {
	sql, err := Gen([]Row{{Repo: "erp-sales", Schema: "erp_sales",
		Role: "erp_sales_rw", ShellLoginRole: "shell_go_core"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`CREATE SCHEMA IF NOT EXISTS erp_sales`,
		// 归档 schema 必须是本组件专属的。不能共用全局 archive——
		// 那会在权限墙上开洞：erp_sales 为了写自己的归档分区拿到该 schema
		// 的写权限，于是也能读到 crm_activity 归档过去的数据（§11.5.4）
		`CREATE SCHEMA IF NOT EXISTS erp_sales_archive`,
		`CREATE ROLE erp_sales_rw`,
		`GRANT USAGE ON SCHEMA erp_sales TO erp_sales_rw`,
		`GRANT USAGE ON SCHEMA erp_sales_archive TO erp_sales_rw`,
		// 外壳登录角色要能 SET ROLE 成组件角色（§13.3 铁律二）
		`GRANT erp_sales_rw TO shell_go_core`,
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("产出里缺少 %q", want)
		}
	}
}

// ⚠️ 实测踩坑：GRANT ... ON TABLES 不会连带 BIGSERIAL 自增列背后的
// SEQUENCE——PostgreSQL 里两者是独立的权限对象。mdm-customer 用真实
// PostgreSQL 跑 Create 时报 "permission denied for sequence
// customers_id_seq"，虽然 customers 表本身的 INSERT/SELECT 都已经
// 授权过。ALTER DEFAULT PRIVILEGES 必须两条都给，只给 ON TABLES 那一条
// 会让每一张用 BIGSERIAL/IDENTITY 主键的表都在这里踩坑——而这是全项目
// 表设计规范的标准写法（§11.2.1），不是个例。
func TestGen_序列也要授权不只是表(t *testing.T) {
	sql, err := Gen([]Row{{Repo: "erp-sales", Schema: "erp_sales",
		Role: "erp_sales_rw", ShellLoginRole: "shell_go_core"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`ALTER DEFAULT PRIVILEGES IN SCHEMA erp_sales`,
		`GRANT USAGE, SELECT ON SEQUENCES TO erp_sales_rw`,
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("产出里缺少 %q，完整产出：\n%s", want, sql)
		}
	}
}

func TestGen_组件角色之间互相看不见(t *testing.T) {
	sql, _ := Gen([]Row{
		{Repo: "erp-sales", Schema: "erp_sales", Role: "erp_sales_rw", ShellLoginRole: "shell_go_core"},
		{Repo: "crm-lead", Schema: "crm_lead", Role: "crm_lead_rw", ShellLoginRole: "shell_go_backoffice"},
	}, "")
	// 权限墙：erp_sales_rw 绝不能拿到 crm_lead 的任何权限
	if strings.Contains(sql, "ON SCHEMA crm_lead TO erp_sales_rw") {
		t.Error("跨组件授权，PG RBAC 权限墙被打穿")
	}
}

func TestGen_幂等(t *testing.T) {
	sql, _ := Gen([]Row{{Repo: "mdm-org", Schema: "mdm_org",
		Role: "mdm_org_rw", ShellLoginRole: "shell_go_core"}}, "")
	// CREATE ROLE 没有 IF NOT EXISTS，必须包在 DO 块里判存在
	if !strings.Contains(sql, "DO $$") {
		t.Error("CREATE ROLE 必须包在 DO 块里做存在判断，否则重跑会报 role already exists")
	}
}

// 平台只打印 CREATE DATABASE，不建库（§2.7.2 ①）。
// 建库语句必须在产出里，且必须与 CREATE SCHEMA 分开——
// PG 不能在一个库内部创建它自己，也不能在同一个事务里 CREATE DATABASE。
func TestGen_建库语句单独一段(t *testing.T) {
	sql, _ := Gen(nil, "")
	if !strings.Contains(sql, "CREATE DATABASE brickkit_db") {
		t.Error("缺少 CREATE DATABASE brickkit_db")
	}
	if strings.Contains(sql, "BEGIN;") &&
		strings.Index(sql, "CREATE DATABASE") > strings.Index(sql, "BEGIN;") {
		t.Error("CREATE DATABASE 不能在事务里")
	}
}

// database 参数为空时默认 brickkit_db（生产/真机部署走的那个库，向后
// 兼容既有调用方）；传自定义名字时必须真的用上，不能被内部忽略——这是
// 本地"测试库与演示库分开"这条约定的地基（AGENTS.md），传错了会让
// 建库脚本悄悄还是建在 brickkit_db 上，测试数据继续混进演示数据。
func TestGen_自定义database名真的生效(t *testing.T) {
	sql, err := Gen([]Row{{Repo: "erp-sales", Schema: "erp_sales",
		Role: "erp_sales_rw", ShellLoginRole: "shell_go_core"}}, "brickkit_test_db")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sql, "CREATE DATABASE brickkit_test_db") {
		t.Error("缺少 CREATE DATABASE brickkit_test_db")
	}
	if !strings.Contains(sql, `\connect brickkit_test_db`) {
		t.Error("缺少 \\connect brickkit_test_db")
	}
	if strings.Contains(sql, "brickkit_db'") {
		t.Errorf("传了自定义 database 名，产出里不该还残留默认库名：\n%s", sql)
	}
}

func TestGen_非法database名报错(t *testing.T) {
	if _, err := Gen(nil, "brickkit-test-db"); err == nil {
		t.Error("database 名带连字符应该报错——identRe 只认小写字母数字下划线")
	}
}

// ⚠️ 实测发现：psql 的 :'var' 替换在 DO $$ ... $$ 块内部不生效（这是
// psql 的设计行为，不是 bug），直接写 PASSWORD :'pw_xxx' 在 DO 块里会
// 报 "syntax error at or near ":""。这条测试锁死修法：密码只能在顶层
// ALTER ROLE 里设，不能出现在任何 DO $$ ... $$ 块内部。
func TestGen_外壳密码不在DO块内部(t *testing.T) {
	sql, err := Gen(nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sql, "ALTER ROLE shell_go_core PASSWORD :'pw_shell_go_core'") {
		t.Error("应该有一条顶层 ALTER ROLE 设密码")
	}
	// 逐个 DO $$ ... $$ 块检查内部有没有混进 PASSWORD :'
	for {
		start := strings.Index(sql, "DO $$")
		if start == -1 {
			break
		}
		end := strings.Index(sql[start:], "END $$;")
		if end == -1 {
			t.Fatal("DO 块没有匹配的 END $$;")
		}
		block := sql[start : start+end]
		if strings.Contains(block, "PASSWORD :'") {
			t.Fatalf("DO 块内部混进了 PASSWORD :'...'，psql 不会在这里做变量替换：\n%s", block)
		}
		sql = sql[start+end+len("END $$;"):]
	}
}
