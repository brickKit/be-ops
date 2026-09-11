package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/brickKit/be-ops/internal/authzreg"
	"github.com/brickKit/be-ops/internal/dbscript"
	"github.com/brickKit/be-ops/internal/genyaml"
	"github.com/brickKit/be-ops/internal/registry"
)

// be-ops 认领平台明确不做的 11 个产出（总纲 §2.4、设计书决策 89/93）。
// 它不是 brickKit 组件，不进 brickkit.yaml。
var subcommands = map[string]string{
	"registry":      "校验全局端口册与 schema 册自洽（产出 6）",
	"db-script":     "产出建库脚本：DATABASE/SCHEMA/ROLE/授权/外壳登录角色（产出 2）",
	"gen":           "产出 brickkit.yaml，含 slot 互斥与 channel 多选校验（产出 5）",
	"routes":        "产出网关路由表，两个出口按组件是否进外壳分流（产出 1）",
	"features":      "产出 feature 清单，写进 IAM 适配层的 enabledComponents（产出 3）",
	"permissions":   "产出权限键册 registry/permissions.tsv（产出 9，第 14 章）",
	"data-scopes":   "产出数据权限总表 registry/data-scopes.tsv（产出 10，第 14 章）",
	"shell-config":  "产出外壳合并配置：谁进哪个外壳、端口、迁移顺序（产出 4）",
	"shell-env":     "产出每外壳一份环境变量表（产出 7）——最容易被漏掉的一件",
	"shell-depends": "产出 shell-compose 的 depends_on：外壳之间的启动顺序（产出 8）",
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
	case "gen":
		err = runGen(os.Args[2:])
	case "permissions":
		err = runPermissions(os.Args[2:])
	case "data-scopes":
		err = runDataScopes(os.Args[2:])
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
	fmt.Printf("✓ 端口册与 schema 册自洽（%d 个组件 + 带外容器，端口两两不重复）\n", countComponents(ports))
	return nil
}

func countComponents(ports []registry.PortRow) int {
	n := 0
	for _, p := range ports {
		if len(p.Repo) < 7 || p.Repo[:7] != "_infra-" {
			n++
		}
	}
	return n
}

// runDBScript 是 "db-script --out <path> [--database <名字>]"：产出幂等
// 建库 SQL（产出 2）。⚠️ `--database` 默认 `brickkit_db`（生产/真机部署
// 走的那个库）——本地开发要给测试单独建一个隔离库时才需要显式传，比如
// `--database brickkit_test_db`，见根 AGENTS.md"测试库与演示库分开"。
func runDBScript(args []string) error {
	fs := flag.NewFlagSet("db-script", flag.ExitOnError)
	root := fs.String("root", ".", "装配仓库根目录")
	out := fs.String("out", "", "输出文件路径")
	database := fs.String("database", "brickkit_db", "目标库名（本地测试库用 brickkit_test_db）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return fmt.Errorf("用法：be-ops db-script --root <path> --out <path> [--database <名字>]")
	}

	schemas, err := registry.LoadSchemas(filepath.Join(*root, "registry", "schemas.tsv"))
	if err != nil {
		return err
	}
	rows := make([]dbscript.Row, 0, len(schemas))
	for _, s := range schemas {
		rows = append(rows, dbscript.Row{
			Repo: s.Repo, Schema: s.Schema, Role: s.Role, ShellLoginRole: s.ShellLoginRole,
		})
	}
	sql, err := dbscript.Gen(rows, *database)
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, []byte(sql), 0o644); err != nil {
		return err
	}
	fmt.Printf("✓ 建库脚本已产出：%s（%d 个组件，目标库 %s）\n", *out, len(rows), *database)
	return nil
}

// runGen 是 "gen --out brickkit.yaml"：扫描 components/ 下所有组件，产出
// 装配清单（产出 5）。components/ 里有什么就装什么——「没买」的正确做法
// 是从源头不把那个组件的 submodule 加进来，不是这里再过滤一次购买清单
// （决策 98）。
func runGen(args []string) error {
	fs := flag.NewFlagSet("gen", flag.ExitOnError)
	root := fs.String("root", ".", "装配仓库根目录")
	out := fs.String("out", "brickkit.yaml", "输出文件路径")
	if err := fs.Parse(args); err != nil {
		return err
	}

	specs, err := genyaml.Load(filepath.Join(*root, "components"))
	if err != nil {
		return err
	}
	ids := make([]string, len(specs))
	for i, s := range specs {
		ids[i] = s.ID
	}
	yamlOut, err := genyaml.Gen(specs, ids)
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, []byte(yamlOut), 0o644); err != nil {
		return err
	}
	fmt.Printf("✓ %s 已产出（%d 个组件）\n", *out, len(specs))
	return nil
}

// runPermissions 是 "permissions --root <path>"：从各组件 assembly.yaml
// 的 permissions 段聚合出 registry/permissions.tsv（产出 9）。⚠️ 这张表
// 只增不改——已发布的 key 永远保留，本命令绝不删行，见 internal/authzreg
// 包文档。
func runPermissions(args []string) error {
	fs := flag.NewFlagSet("permissions", flag.ExitOnError)
	root := fs.String("root", ".", "装配仓库根目录")
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
	if err := authzreg.WritePermissionsTSV(tsvPath, rows); err != nil {
		return err
	}
	fmt.Printf("✓ %s 已产出（%d 条权限键）\n", tsvPath, len(rows))
	return nil
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
