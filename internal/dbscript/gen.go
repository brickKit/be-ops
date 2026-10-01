// Package dbscript 产出建库脚本：CREATE DATABASE + 每组件 CREATE SCHEMA /
// {schema}_archive / CREATE ROLE / 授权 + 4 个外壳登录角色（总纲 §2.4
// 产出 2，随产出 2b 一起）。外壳登录角色对成员组件角色的 GRANT 来自各
// 外壳 shell/be/<name>/component.yaml 的 shell.members。平台不建库不建 schema（§2.7.2 ①），这一层
// 补上平台刻意不做的那一半。
package dbscript

import (
	"fmt"
	"regexp"
	"strings"
)

// Row 是 registry/ports.tsv + schemas.tsv 联合出的一行——一个组件要建的
// schema/role/所属外壳。
type Row struct {
	Repo           string
	Schema         string
	Role           string
	ShellLoginRole string // 仅用于校验名字合法；GRANT 不再取自这一列，而取自外壳清单
}

// Shell 是 shell/be/<name>/component.yaml 读出来的外壳：Name 如 go-core，
// Members 是成员组件 ID（已去掉 @版本，如 erp/sales）。
type Shell struct {
	Name    string
	Members []string
}

// LoginRole 外壳登录角色名：shell_ + 名字里的 - 换成 _（go-core → shell_go_core）。
func (s Shell) LoginRole() string { return loginRoleOf(s.Name) }

func loginRoleOf(name string) string {
	return "shell_" + strings.ReplaceAll(name, "-", "_")
}

// memberRepo 把组件 ID（erp/sales）换成 schemas.tsv 里的 repo 名（erp-sales）。
func memberRepo(id string) string { return strings.ReplaceAll(id, "/", "-") }

var identRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// defaultShellNames 是固定的 4 个外壳（阶段 06：go-core / go-infra /
// go-backoffice / py-render），即使 shell/be/ 下还没有清单也要先建登录
// 角色；清单里出现的其它外壳会并进来。
var defaultShellNames = []string{"go-core", "go-backoffice", "go-infra", "py-render"}

// Gen 产出幂等的建库 SQL，分三段：建库 / 外壳登录角色 / 每组件
// schema+archive+role+授权。
//
// ⚠️ 三段顺序不能变：CREATE DATABASE 必须在事务外、且单独执行一次
// （PG 不能在一个库内部创建它自己）；外壳登录角色必须先于组件角色存在，
// 因为后面的 GRANT <组件角色> TO <外壳角色> 需要外壳角色已经建好。
//
// database 是目标库名——默认部署场景永远是 brickkit_db，但本地开发
// 还需要给测试单独建一个隔离库（跟真机演示数据物理分开，不是靠"记得
// 跑清理脚本"这种约定）。⚠️ 只有第 1/2 段用得到这个参数（CREATE
// DATABASE 语句本身 + `\connect` 目标）；第 3 段（schema/role/授权）
// 天然库无关——PostgreSQL 的 ROLE 是**集群级**对象，不属于任何一个
// database，`\connect` 到哪个库执行，schema 就建在哪个库，role 不需要
// 重建（已经在 brickkit_db 建过一次，全局可见）；只有 SCHEMA 本身是
// per-database 对象，必须在每个库里各建一份。
//
// shells 是外壳清单；为空时只建登录角色，不产出任何成员 GRANT。
func Gen(rows []Row, database string, shells []Shell) (string, error) {
	if database == "" {
		database = "brickkit_db"
	}
	if !identRe.MatchString(database) {
		return "", fmt.Errorf("非法 database 名：%q", database)
	}
	for _, r := range rows {
		if !identRe.MatchString(r.Schema) {
			return "", fmt.Errorf("非法 schema 名：%q（来自 %s）", r.Schema, r.Repo)
		}
		if !identRe.MatchString(r.Role) {
			return "", fmt.Errorf("非法 role 名：%q（来自 %s）", r.Role, r.Repo)
		}
		if !identRe.MatchString(r.ShellLoginRole) {
			return "", fmt.Errorf("非法 shell login role 名：%q（来自 %s）", r.ShellLoginRole, r.Repo)
		}
	}

	// 登录角色 = 默认 4 个 ∪ 清单里的外壳，保持稳定顺序
	var loginRoles []string
	seen := map[string]bool{}
	for _, n := range defaultShellNames {
		loginRoles = append(loginRoles, loginRoleOf(n))
		seen[loginRoleOf(n)] = true
	}
	for _, sh := range shells {
		if !seen[sh.LoginRole()] {
			loginRoles = append(loginRoles, sh.LoginRole())
			seen[sh.LoginRole()] = true
		}
	}
	for _, lr := range loginRoles {
		if !identRe.MatchString(lr) {
			return "", fmt.Errorf("非法 shell login role 名：%q", lr)
		}
	}

	var b strings.Builder

	// ═══ 第 1 段：建库。必须单独连到 postgres 库执行一次 ═══
	// ⚠️ 平台只会打印这一句，不会执行（§2.7.2 ①：建库要 CREATEDB 权限，
	//    让每个组件的运行期账号都有它是全平台提权；且 PG 不能在一个库
	//    内部创建它自己）。
	b.WriteString("-- ═══ 第 1 段：建库。必须单独连到 postgres 库执行一次 ═══\n")
	fmt.Fprintf(&b, "SELECT 'CREATE DATABASE %s'\n", database)
	fmt.Fprintf(&b, " WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = '%s')\\gexec\n\n", database)

	// ═══ 第 2 段：外壳登录角色。连到目标库执行 ═══
	b.WriteString("-- ═══ 第 2 段：外壳登录角色。连到目标库执行 ═══\n")
	fmt.Fprintf(&b, "\\connect %s\n\n", database)
	for _, shell := range loginRoles {
		// ⚠️ 实测修正：psql 的 :'var' 变量替换在 DO $$ ... $$ 块内部不生效
		// ——这是 psql 的设计行为（避免破坏函数体里可能出现的字面量），
		// 不是 bug。原计划把 PASSWORD :'pw_xxx' 直接写在 DO 块里，实测
		// 报 "syntax error at or near ":""，因为那个冒号原样送到了服务端。
		// 正解：DO 块内只管"存在性判断 + 建角色（不带密码）"，密码用一条
		// 顶层 ALTER ROLE 单独设——顶层语句不在 $$ 里，替换正常生效，
		// 而且 ALTER ROLE 天然幂等，重跑不会报错。
		fmt.Fprintf(&b, "DO $$ BEGIN\n")
		fmt.Fprintf(&b, "  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='%s') THEN\n", shell)
		fmt.Fprintf(&b, "    CREATE ROLE %s LOGIN;\n", shell)
		fmt.Fprintf(&b, "  END IF;\n")
		fmt.Fprintf(&b, "END $$;\n")
		fmt.Fprintf(&b, "ALTER ROLE %s PASSWORD :'pw_%s';\n\n", shell, shell)
	}

	// ═══ 第 3 段：每组件 schema + archive schema + role + 授权 ═══
	b.WriteString("-- ═══ 第 3 段：每组件 schema + archive schema + role + 授权 ═══\n")
	for _, r := range rows {
		archive := r.Schema + "_archive"
		fmt.Fprintf(&b, "-- %s\n", r.Repo)
		fmt.Fprintf(&b, "CREATE SCHEMA IF NOT EXISTS %s;\n", r.Schema)
		fmt.Fprintf(&b, "CREATE SCHEMA IF NOT EXISTS %s;\n", archive)
		fmt.Fprintf(&b, "DO $$ BEGIN\n")
		fmt.Fprintf(&b, "  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='%s') THEN\n", r.Role)
		// ⚠️ NOLOGIN：组件角色只用于 SET LOCAL ROLE 切换，从不登录
		// （§13.3 铁律二）——它不出现在 brickkit.yaml 的资源凭据里。
		fmt.Fprintf(&b, "    CREATE ROLE %s NOLOGIN;\n", r.Role)
		fmt.Fprintf(&b, "  END IF;\n")
		fmt.Fprintf(&b, "END $$;\n")
		// ⚠️ USAGE 与 CREATE 分两条 GRANT，不是一条 "GRANT USAGE, CREATE"——
		// 这是照测试断言改的（测试要求 "GRANT USAGE ON SCHEMA … TO …" 作为
		// 独立可搜索的一条），两种写法对 PostgreSQL 语义完全等价。
		fmt.Fprintf(&b, "GRANT USAGE ON SCHEMA %s TO %s;\n", r.Schema, r.Role)
		fmt.Fprintf(&b, "GRANT CREATE ON SCHEMA %s TO %s;\n", r.Schema, r.Role)
		fmt.Fprintf(&b, "GRANT USAGE ON SCHEMA %s TO %s;\n", archive, r.Role)
		fmt.Fprintf(&b, "GRANT CREATE ON SCHEMA %s TO %s;\n", archive, r.Role)
		// ⚠️ 实测踩坑：GRANT ... ON TABLES 不连带 BIGSERIAL 自增列背后的
		// SEQUENCE——两者是 PostgreSQL 里独立的权限对象。§11.2.1 的强制
		// 字段规范里没有一张表不用自增主键，只给 ON TABLES 会让每个组件
		// 第一次 INSERT 就报 "permission denied for sequence"。
		fmt.Fprintf(&b, "ALTER DEFAULT PRIVILEGES IN SCHEMA %s\n  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO %s;\n", r.Schema, r.Role)
		fmt.Fprintf(&b, "ALTER DEFAULT PRIVILEGES IN SCHEMA %s\n  GRANT USAGE, SELECT ON SEQUENCES TO %s;\n", r.Schema, r.Role)
		fmt.Fprintf(&b, "ALTER DEFAULT PRIVILEGES IN SCHEMA %s\n  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO %s;\n", archive, r.Role)
		fmt.Fprintf(&b, "ALTER DEFAULT PRIVILEGES IN SCHEMA %s\n  GRANT USAGE, SELECT ON SEQUENCES TO %s;\n", archive, r.Role)
		b.WriteString("\n")
	}

	// ═══ 第 4 段：外壳登录角色 → 成员组件角色（§13.3 铁律二） ═══
	// 外壳登录角色才能 SET ROLE 成成员的角色；成员清单来自外壳清单。
	roleByRepo := map[string]string{}
	for _, r := range rows {
		roleByRepo[r.Repo] = r.Role
	}
	for _, sh := range shells {
		fmt.Fprintf(&b, "-- 外壳 %s 的成员\n", sh.Name)
		for _, m := range sh.Members {
			role, ok := roleByRepo[memberRepo(m)]
			if !ok {
				continue // 无库组件（无 schema 行）不需要 GRANT
			}
			fmt.Fprintf(&b, "GRANT %s TO %s;\n", role, sh.LoginRole())
		}
		b.WriteString("\n")
	}

	return b.String(), nil
}
