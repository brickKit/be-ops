package main

import (
	"fmt"
	"os"
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
		fmt.Println("be-ops —— BrickEnterprise 装配生成器")
		fmt.Println()
		fmt.Println("子命令：")
		for k, v := range subcommands {
			fmt.Printf("  %-14s %s\n", k, v)
		}
		os.Exit(2)
	}
	fmt.Fprintf(os.Stderr, "子命令 %q 尚未实现\n", os.Args[1])
	os.Exit(1)
}
