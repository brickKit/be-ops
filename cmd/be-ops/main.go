package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/brickKit/be-ops/internal/authzreg"
	"github.com/brickKit/be-ops/internal/dbscript"
	"github.com/brickKit/be-ops/internal/projconf"
	"github.com/brickKit/be-ops/internal/registry"
)

// be-ops 认领平台明确不做的产出（总纲 §2.4、设计书决策 89/93）——固定编号
// "11 个"，不是这张表现在还剩的项数：产出 4/7/8（外壳合并专用）已经随
// brickKit 自己的 servedBy 机制陆续长出原生能力、一个个退休（最后一个
// "shell-config" 子命令在阶段四附加 Task 0.6 一并删除），完整过程见
// 装配仓库 docs/plans/04b-验证记录.md。编号不重排，因为 registry/*.tsv
// 等别处按编号引用产出 9/10。它不是 brickKit 组件，不进 brickkit.yaml。
var subcommands = map[string]string{
	"registry":      "校验全局端口册与 schema 册自洽（产出 6）",
	"db-script":     "产出建库脚本：DATABASE/SCHEMA/ROLE/授权/外壳登录角色（产出 2）",
	"routes":        "产出网关路由表，两个出口按组件是否进外壳分流（产出 1）",
	"features":      "产出 feature 清单，写进 IAM 适配层的 enabledComponents（产出 3）",
	"permissions":   "产出权限键册 registry/permissions.tsv（产出 9，第 14 章）",
	"data-scopes":   "产出数据权限总表 registry/data-scopes.tsv（产出 10，第 14 章）",
	"config-schema": "生成 component.yaml configSchema 的协议键段（be-protocol P2.8；--check 供门禁 protocol-config-scan）",
	"events":        "生成 component.yaml 的 events 段（be-protocol P12.16；--check 供门禁 events-declaration-scan）",
	"resources":     "校验 permissions/resources/requires_capabilities，维护 registry/resource-types.tsv（只增）并产出 RESOURCE_CATALOG",
	"data-subjects": "校验 registry/data-subjects.tsv 覆盖各组件 lifecycle.yaml 的擦除主体",
	"authzgen":      "生成每个组件的 authzgen 源文件（Go/Python/TS；--check 供门禁 authzgen-fresh）",
	"openapi":       "把资源契约（be-protocol openapi/resource-authz.yaml）并进组件发布的 OpenAPI，并查每个操作都声明了守卫（缺 x-be-permission 即失败；--check 供门禁 openapi-fresh）",
	"gates":         "列出门禁（be-acceptance A7）调用的全部 be-ops --check 命令；--run 在本进程里跑一遍",
	"edge":          "从 edge_routes 生成部署条目的 Traefik 路由标签 / K8s paths 与 be-edge 中间件文件（--check 供门禁 edge-routes-fresh）",
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "registry":
		err = runRegistry(os.Args[2:])
	case "db-script":
		err = runDBScript(os.Args[2:])
	case "permissions":
		err = runPermissions(os.Args[2:])
	case "data-scopes":
		err = runDataScopes(os.Args[2:])
	case "config-schema":
		err = runManifestGen("config-schema", os.Args[2:], configGenerator)
	case "resources":
		err = runResources(os.Args[2:])
	case "data-subjects":
		err = runDataSubjects(os.Args[2:])
	case "edge":
		err = runEdge(os.Args[2:])
	case "authzgen":
		err = runAuthzgen(os.Args[2:])
	case "openapi":
		err = runOpenAPI(os.Args[2:])
	case "gates":
		err = gates(os.Args[2:], os.Stdout)
	case "events":
		err = runManifestGen("events", os.Args[2:], func(string) (generator, error) { return eventsGen{}, nil })
	default:
		fmt.Fprintf(os.Stderr, "子命令 %q 尚未实现\n", os.Args[1])
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "✗", err)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("be-ops —— BrickEnterprise 装配生成器")
	fmt.Println()
	fmt.Println("子命令：")
	for k, v := range subcommands {
		fmt.Printf("  %-14s %s\n", k, v)
	}
}

// runRegistry 是 "registry check"：判据从 infra/scripts/registry-check.sh
// 原样移植，脚本已退休为薄壳。
func runRegistry(args []string) error {
	// ⚠️ 实测踩坑：flag.Parse 遇到第一个非 flag 参数就停止解析——
	// "check" 排在 --root 前面时，--root 会被静默忽略、root 停在默认值
	// "."，而不是报错。子命令词（"check"）必须在 fs.Parse 之前先摘掉，
	// 不能让 flag 包自己去踩这颗雷。
	if len(args) < 1 || args[0] != "check" {
		return fmt.Errorf("用法：be-ops registry check --root <path>")
	}
	fs := flag.NewFlagSet("registry", flag.ExitOnError)
	root := fs.String("root", ".", "装配仓库根目录")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	ports, err := registry.LoadPorts(filepath.Join(*root, "registry", "ports.tsv"))
	if err != nil {
		return err
	}
	schemas, err := registry.LoadSchemas(filepath.Join(*root, "registry", "schemas.tsv"))
	if err != nil {
		return err
	}
	errs := registry.Check(ports, schemas)
	if len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, "✗", e)
		}
		return fmt.Errorf("端口册与 schema 册不自洽（%d 条）", len(errs))
	}
	fmt.Printf("✓ 端口册与 schema 册自洽（%d 个组件 + 带外容器/外壳自身，端口两两不重复）\n", countComponents(ports))
	return nil
}

// countComponents 只是给命令行输出报个数用的，跟 checkComponentCount
// 内部校验用的判据保持一致（_infra-/_shell- 两个前缀都不算进"组件"
// 这个词，避免打印出来的数字跟"应该是 62"这句话对不上，误导人）。
func countComponents(ports []registry.PortRow) int {
	n := 0
	for _, p := range ports {
		if strings.HasPrefix(p.Repo, "_infra-") || strings.HasPrefix(p.Repo, "_shell-") {
			continue
		}
		n++
	}
	return n
}

// runDBScript 是 "db-script --out <path> [--database <名字>] [--deploy <部署文件>] [--password-vars]"：
// 产出幂等建库脚本（产出 2）。每个组件的属主角色、运行角色、schema、库名全部取自项目配置
// （config/<scope>-<name>.yaml 的 PG_OWNER_USER / PG_USER / PG_SCHEMA / PG_DATABASE，$var: 经
// config/vars.yaml 和部署文件的 vars: 解析），不按命名规则推导（be-protocol P10.1）。
// --database 把所有 PG_DATABASE 换成另一个库（本地测试库 brickkit_test_db）；--password-vars 只
// 打印 "<psql 变量>\t<env:NAME|file:PATH>"，供 db-init 先 \set 口令（be-ops 从不读口令的值）。
func runDBScript(args []string) error {
	fs := flag.NewFlagSet("db-script", flag.ExitOnError)
	root := fs.String("root", ".", "装配仓库根目录")
	out := fs.String("out", "", "输出文件路径")
	database := fs.String("database", "", "把所有组件的 PG_DATABASE 换成这个库（本地测试库用 brickkit_test_db）")
	deploy := fs.String("deploy", "deploy.yaml", "取其 vars: 覆盖 config/vars.yaml 的部署文件（相对 --root；空 = 不用）")
	pwVars := fs.Bool("password-vars", false, "只打印口令变量清单")
	if err := fs.Parse(args); err != nil {
		return err
	}
	dp := ""
	if *deploy != "" {
		dp = filepath.Join(*root, *deploy)
		if _, err := os.Stat(dp); err != nil {
			dp = ""
		}
	}
	p, err := projconf.Load(*root, dp)
	if err != nil {
		return err
	}
	plan, err := dbscript.Build(p, *database)
	if err != nil {
		return err
	}
	if *pwVars {
		for _, l := range dbscript.PasswordVars(plan) {
			fmt.Println(l)
		}
		return nil
	}
	if *out == "" {
		return fmt.Errorf("用法：be-ops db-script --root <path> --out <path> [--database <名字>] [--password-vars]")
	}
	sql, err := dbscript.Render(plan)
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, []byte(sql), 0o644); err != nil {
		return err
	}
	fmt.Printf("✓ 建库脚本已产出：%s（%d 个有库组件，%d 个外壳登录角色）\n", *out, len(plan.Components), len(plan.Shells))
	return nil
}

// runPermissions 是 "permissions --root <path> [--check] [--base <file>]"：从各组件 assembly.yaml
// 的 permissions 段聚合出 registry/permissions.tsv（产出 9）。⚠️ 这张表只增不改——已发布的 key
// 永远保留，本命令绝不删行，见 internal/authzreg 包文档。--check 不写文件，不是最新就失败；
// --base 是已提交的版本（git show HEAD:registry/permissions.tsv），结果相对它必须只增
// （authzreg.AppendOnly：列只在末尾追加、旧键不删、非空的旧值不变）。
func runPermissions(args []string) error {
	fs := flag.NewFlagSet("permissions", flag.ExitOnError)
	root := fs.String("root", ".", "装配仓库根目录")
	check := fs.Bool("check", false, "不写文件；不是最新就退出 1")
	base := fs.String("base", "", "已提交的 permissions.tsv；结果相对它必须只增")
	if err := fs.Parse(args); err != nil {
		return err
	}
	tsvPath := filepath.Join(*root, "registry", "permissions.tsv")

	existing, err := authzreg.ReadPermissionsTSV(tsvPath)
	if err != nil {
		return err
	}
	decls, err := authzreg.LoadAssemblyDecls(filepath.Join(*root, "components"))
	if err != nil {
		return err
	}
	rows, warnings, err := authzreg.GenPermissions(existing, decls)
	if err != nil {
		return err
	}
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "⚠", w)
	}
	content := authzreg.RenderPermissionsTSV(rows)
	stale, err := writeOrCheck(tsvPath, content, *check)
	if err != nil {
		return err
	}
	var problems []string
	if stale {
		problems = append(problems, tsvPath+" is not current: run be-ops permissions")
	}
	if *base != "" {
		old, err := os.ReadFile(*base)
		if err != nil {
			return err
		}
		p, err := authzreg.AppendOnly(old, content)
		if err != nil {
			return err
		}
		problems = append(problems, p...)
	}
	return report("permissions", problems, len(decls))
}

// runDataScopes 是 "data-scopes --root <path>"：从各组件 assembly.yaml
// 的 data_scopes 段聚合出 registry/data-scopes.tsv（产出 10）。这张表纯
// 派生、不需要防改，每次全量重生成（registry/README.md）。
//
// ⚠️ 省略 data_scopes 段的组件会让 LoadAssemblyDecls 报错——不需要也要
// 显式写 data_scopes: none（导读第 22 条）。
func runDataScopes(args []string) error {
	fs := flag.NewFlagSet("data-scopes", flag.ExitOnError)
	root := fs.String("root", ".", "装配仓库根目录")
	if err := fs.Parse(args); err != nil {
		return err
	}
	decls, err := authzreg.LoadAssemblyDecls(filepath.Join(*root, "components"))
	if err != nil {
		return err
	}
	rows := authzreg.GenDataScopes(decls)
	tsvPath := filepath.Join(*root, "registry", "data-scopes.tsv")
	if err := authzreg.WriteDataScopesTSV(tsvPath, rows); err != nil {
		return err
	}
	fmt.Printf("✓ %s 已产出（%d 条数据权限声明）\n", tsvPath, len(rows))
	return nil
}
