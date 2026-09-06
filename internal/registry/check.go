package registry

import (
	"fmt"
	"regexp"
	"strings"
)

// Check 跑全部自洽校验，返回错误消息列表（空 = 全绿）。判据一字不改地从
// infra/scripts/registry-check.sh 移植（阶段一 Task 4 已实测修过两处），
// 加一条 Go 侧才好写的第 7 条。
func Check(ports []PortRow, schemas []SchemaRow) []string {
	var errs []string
	errs = append(errs, checkPortDuplicates(ports)...)
	errs = append(errs, checkPort80Owners(ports)...)
	errs = append(errs, checkSchemaReposExistInPorts(ports, schemas)...)
	errs = append(errs, checkSchemaNaming(schemas)...)
	errs = append(errs, checkShellConsistency(ports, schemas)...)
	errs = append(errs, checkComponentCount(ports)...)
	return errs
}

// checkComponentCount ports.tsv 里非 _infra- 前缀的行必须正好 62 行
// （设计书附录 H）。
func checkComponentCount(ports []PortRow) []string {
	n := 0
	for _, p := range ports {
		if !strings.HasPrefix(p.Repo, "_infra-") {
			n++
		}
	}
	if n != 62 {
		return []string{fmt.Sprintf("ports.tsv 里的组件行数是 %d，应为 62（设计书附录 H）", n)}
	}
	return nil
}

// checkPortDuplicates 端口两两不重复。两个例外：
//
//	① slot:frontend 族共用 80
//	② ⚠️ 实测修正（Task 4）：_infra- 前缀的带外容器互相之间允许同端口——
//	   它们是互斥替换件（rustfs/minio 同为 9000、traefik/nginx 同为 80），
//	   永不同时跑。判据：冲突双方只要有一个不是 _infra- 前缀就是真冲突。
func checkPortDuplicates(ports []PortRow) []string {
	usage := map[string][]string{} // port -> repos
	for _, p := range ports {
		for _, port := range []string{p.HTTPPort, p.GRPCPort} {
			if port == "-" || port == "80" {
				continue
			}
			usage[port] = append(usage[port], p.Repo)
		}
	}
	var errs []string
	for port, repos := range usage {
		if len(repos) < 2 {
			continue
		}
		real := false
		for _, r := range repos {
			if !strings.HasPrefix(r, "_infra-") {
				real = true
				break
			}
		}
		if real {
			errs = append(errs, fmt.Sprintf("端口重复：%s: %s", port, strings.Join(repos, " ")))
		}
	}
	return errs
}

// checkPort80Owners 用 80 的必须是 frontend-* 或带外网关容器（traefik/
// nginx，两者互斥）。
func checkPort80Owners(ports []PortRow) []string {
	var bad []string
	for _, p := range ports {
		if p.HTTPPort != "80" {
			continue
		}
		if strings.HasPrefix(p.Repo, "frontend-") || strings.HasPrefix(p.Repo, "_infra-") {
			continue
		}
		bad = append(bad, p.Repo)
	}
	if len(bad) == 0 {
		return nil
	}
	return []string{fmt.Sprintf("非 frontend/带外网关占用 80：%s", strings.Join(bad, " "))}
}

// checkSchemaReposExistInPorts schemas.tsv 里的 repo 必须在 ports.tsv 里存在。
func checkSchemaReposExistInPorts(ports []PortRow, schemas []SchemaRow) []string {
	known := make(map[string]bool, len(ports))
	for _, p := range ports {
		known[p.Repo] = true
	}
	var errs []string
	for _, s := range schemas {
		if !known[s.Repo] {
			errs = append(errs, fmt.Sprintf("schemas.tsv 里的 %s 不在 ports.tsv 里", s.Repo))
		}
	}
	return errs
}

var repoToSchemaReplacer = strings.NewReplacer("-", "_")

// checkSchemaNaming schema = repo 的 - 换 _；role = schema + _rw。
func checkSchemaNaming(schemas []SchemaRow) []string {
	var errs []string
	for _, s := range schemas {
		wantSchema := repoToSchemaReplacer.Replace(s.Repo)
		if s.Schema != wantSchema {
			errs = append(errs, fmt.Sprintf("%s 的 schema 应为 %s，实际 %s", s.Repo, wantSchema, s.Schema))
		}
		wantRole := s.Schema + "_rw"
		if s.Role != wantRole {
			errs = append(errs, fmt.Sprintf("%s 的 role 应为 %s，实际 %s", s.Repo, wantRole, s.Role))
		}
	}
	return errs
}

var shellNormalizeRe = regexp.MustCompile(`-`)

// checkShellConsistency 是第 7 条：ports.tsv 的 shell 列与 schemas.tsv 的
// shell_login_role 列必须机械对应（go-core ↔ shell_go_core）。对不上说明
// 有人只改了一张表——外壳的连接池会借出错误的登录角色，症状是「某个模块
// 查自己的表报 permission denied」，极难联想到是册子不同步。
//
// standalone/out-of-band 两种 shell 值不参与这条检查——它们本来就没有
// schemas.tsv 对应条目（frontend-*/bff-mobile 不建 schema，带外容器不是
// 组件），那是「组件数核对」该管的事，不是这里。
func checkShellConsistency(ports []PortRow, schemas []SchemaRow) []string {
	schemaByRepo := make(map[string]SchemaRow, len(schemas))
	for _, s := range schemas {
		schemaByRepo[s.Repo] = s
	}
	var errs []string
	for _, p := range ports {
		if p.Shell == "standalone" || p.Shell == "out-of-band" || p.Shell == "" {
			continue
		}
		s, ok := schemaByRepo[p.Repo]
		if !ok {
			continue // 没有 schema 条目（如 blueprint），不属于本条管辖
		}
		want := "shell_" + shellNormalizeRe.ReplaceAllString(p.Shell, "_")
		if s.ShellLoginRole != want {
			errs = append(errs, fmt.Sprintf(
				"%s：ports.tsv 的 shell=%s 应对应 shells_login_role=%s，schemas.tsv 里实际是 %s",
				p.Repo, p.Shell, want, s.ShellLoginRole))
		}
	}
	return errs
}
