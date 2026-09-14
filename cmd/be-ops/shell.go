// 产出 4（合并清单）——阶段四计划 Task 4，阶段四附加 Task 0.2 改版。
//
// ⚠️ 原来这里还有产出 7（每外壳环境变量表）/产出 8（shell-compose 的
// depends_on）两条子命令，`servedBy` 落地后两者都退休了：
//   - 产出 7 的"依赖地址改写"那部分被 brickKit 原生的 servedBy 合并
//     取代（外壳容器自己的 environment 里已经是改写好的地址）；
//   - 产出 7 的"给每个模块传它自己的 configSchema 解析结果"这部分，
//     数据来源从"读 brickKit 生成的 local-debug.*.env"改成了
//     "genyaml.LoadBrickkitConfig + MergeConfig"，直接并进了产出 4
//     （见下面 runShellConfig），不再需要单独一条命令、单独一份产出；
//   - 产出 8 的外壳间启动顺序被 brickKit 原生的 depends_on 改写取代。
//
// 完整调研过程见装配仓库 docs/plans/04b-验证记录.md Task 0.2。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/brickKit/be-ops/internal/genyaml"
	"github.com/brickKit/be-ops/internal/shellconfig"
)

// runShellConfig 是 "shell-config --out <path>"：产出合并清单（产出 4，
// 阶段四附加 Task 0.2 起同时承担原产出 7 的 configSchema 传递职责）。
func runShellConfig(args []string) error {
	fs := flag.NewFlagSet("shell-config", flag.ExitOnError)
	root := fs.String("root", ".", "装配仓库根目录")
	out := fs.String("out", "", "输出文件路径（JSON）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return fmt.Errorf("用法：be-ops shell-config --root <path> --out <path>")
	}

	specs, err := genyaml.Load(filepath.Join(*root, "components"))
	if err != nil {
		return err
	}
	overrides, err := genyaml.LoadBrickkitConfig(filepath.Join(*root, "brickkit.yaml"))
	if err != nil {
		return err
	}
	for i := range specs {
		specs[i].Config = genyaml.MergeConfig(specs[i].ConfigDefaults, overrides[specs[i].ID])
	}

	shells, err := shellconfig.Gen(specs)
	if err != nil {
		return err
	}
	if err := writeJSON(*out, shells); err != nil {
		return err
	}
	fmt.Printf("✓ %s 已产出（%d 个外壳）\n", *out, len(shells))
	return nil
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}
