// 产出 4/7/8（合并清单、每外壳环境变量表、shell-compose 的
// depends_on）——阶段四计划 Task 4。三条子命令共享同一份
// `genyaml.Load` 读出来的组件视图，不各自另起一套 YAML 解析
// （§13.8.2 原话："自己另算的那份，早晚和平台的算法分叉"，这条判据
// 不只适用于"平台的注入结果"，同样适用于"be-ops 自己内部的多个产出
// 该不该共用同一份读取逻辑"）。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/brickKit/be-ops/internal/genyaml"
	"github.com/brickKit/be-ops/internal/shellconfig"
	"github.com/brickKit/be-ops/internal/shelldepends"
	"github.com/brickKit/be-ops/internal/shellenv"
)

// runShellConfig 是 "shell-config --out <path>"：产出合并清单（产出 4）。
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

// runShellDepends 是 "shell-depends --out <path>"：产出 shell-compose
// 的 depends_on（产出 8）。
func runShellDepends(args []string) error {
	fs := flag.NewFlagSet("shell-depends", flag.ExitOnError)
	root := fs.String("root", ".", "装配仓库根目录")
	out := fs.String("out", "", "输出文件路径（JSON）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return fmt.Errorf("用法：be-ops shell-depends --root <path> --out <path>")
	}

	specs, err := genyaml.Load(filepath.Join(*root, "components"))
	if err != nil {
		return err
	}
	deps, err := shelldepends.Gen(specs)
	if err != nil {
		return err
	}
	if err := writeJSON(*out, deps); err != nil {
		return err
	}
	fmt.Printf("✓ %s 已产出（%d 个外壳的启动顺序）\n", *out, len(deps))
	return nil
}

// runShellEnv 是 "shell-env --local-debug-dir <path> --out <path>"：
// 产出每外壳环境变量表（产出 7）。`--local-debug-dir` 指向
// `brickkit up --dry-run` 生成 `local-debug.*.env` 的目录（通常是
// `.brickkit/generated/`）——这一步依赖每个要合并的组件已经在
// `brickkit.yaml` 里被标成 `local: true`（`make gen` 会根据
// `assembly.yaml` 的 `shell` 字段自动写这两行，见 genyaml 的
// Local/LocalPort 计算），不是本命令自己生成的。
func runShellEnv(args []string) error {
	fs := flag.NewFlagSet("shell-env", flag.ExitOnError)
	root := fs.String("root", ".", "装配仓库根目录")
	localDebugDir := fs.String("local-debug-dir", "", "brickkit up --dry-run 生成 local-debug.*.env 的目录")
	out := fs.String("out", "", "输出文件路径（JSON）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" || *localDebugDir == "" {
		return fmt.Errorf("用法：be-ops shell-env --root <path> --local-debug-dir <path> --out <path>")
	}

	specs, err := genyaml.Load(filepath.Join(*root, "components"))
	if err != nil {
		return err
	}

	localDebug := make(map[string]map[string]string)
	for _, s := range specs {
		if s.Shell == "" {
			continue
		}
		// ⚠️ `ComponentSpec.Local` 是 assembly.yaml 的既定意图（"迟早要
		// 合并"），不是"brickkit.yaml 此刻是不是真的写了 local: true"——
		// 外壳分组从阶段一就写死了，但各外壳原子式切换是分任务做的
		// （阶段四 Task 6/7），两者从来不是同一时间点。唯一真实反映
		// "此刻是不是已经切了"的信号是文件是否存在：还没切换的组件，
		// brickkit up --dry-run 根本不会给它生成 local-debug 文件，这是
		// 正常状态，不是"忘了先 dry-run"——所以这里缺文件不当错误处理，
		// 单纯跳过，交给 shellenv.Gen 按"这个外壳的成员是否全部拿到了
		// 数据"去判断整个外壳该不该处理（见该包文档）。真的忘了跑
		// dry-run 时，受影响的外壳会从产出里完全消失，`shell-env` 命令
		// 自己打印的"已产出（N 个外壳）"里 N 会明显偏小，足够引起注意。
		name := "local-debug." + versionedServiceName(s.ID, s.Version) + ".env"
		env, err := readDotEnv(filepath.Join(*localDebugDir, name))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("读 %s 失败：%w", name, err)
		}
		localDebug[s.ID] = env
	}

	shells, err := shellenv.Gen(specs, localDebug)
	if err != nil {
		return err
	}
	if err := writeJSON(*out, shells); err != nil {
		return err
	}
	fmt.Printf("✓ %s 已产出（%d 个外壳）\n", *out, len(shells))
	return nil
}

// versionedServiceName 与 brickKit 自己推导服务名的算法逐字对应
// （总纲 §2.1："/ → -、. → -、全部小写，再接精确版本号"）——
// "mdm/customer"@"1.0.5" → "mdm-customer-1-0-5"，local-debug 文件名的
// 中间那一段。
func versionedServiceName(id, version string) string {
	s := strings.NewReplacer("/", "-", ".", "-").Replace(id + "-" + version)
	return strings.ToLower(s)
}

// envKeyRe 是一个合法 dotenv key 的形状——全大写字母/数字/下划线，
// 不以数字开头（同这个项目自己给环境变量取名的既有约定）。用来把
// "真的是 KEY=VALUE 这一行" 和 "PEM/base64 续行里恰好也带了一个
// `=`" 区分开，见 readDotEnv 的踩坑记录。
var envKeyRe = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// readDotEnv 解析一份 KEY=VALUE 形式的 .env 文件。
//
// ⚠️ 阶段四 Task 6 真机部署 infra-iam-casdoor 时撞到的真实 bug（分两轮
// 才修对，第二轮是真机跑起来才暴露的）：
//
//  1. `appTokenSigningKeyPem` 这类多行 PEM 值，brickKit CLI 生成
//     local-debug 文件时是**原样把换行符写进文件**，不加引号也不转义
//     （`cat -A` 核对过真实文件内容）——本函数原来的实现只认单行
//     `KEY=VALUE`，PEM 后续每一行都因为没有 `=` 被当成无效行悄悄跳过，
//     值被截断成只剩第一行，真机表现是 infra-iam-casdoor 报
//     "appTokenSigningKeyPem 不是合法的 PEM"直接退出。第一轮修复：
//     没有 `=` 的行视为上一个 key 的值的延续。
//  2. 第一轮修复上线后真机再跑一次，PEM 又在**倒数第二行**被截断——
//     Base64 编码的结尾常见 1-2 个 `=` 补齐符（这把真实 RSA 密钥的
//     倒数第二行恰好是 `...XO2UJJaX/FXH` 后面紧跟一个以 `=` 结尾的
//     续行），naive 的"这行有没有 `=`"判据把这种续行也误判成
//     "KEY=VALUE"，多行值被错误地从这里截断、后续内容被拼到一个
//     瞎编出来的假 key 上。真正的判据不是"这行有没有 `=`"，而是
//     "`=` 前面那一段像不像一个合法的 dotenv key"（全大写字母/数字/
//     下划线）——PEM/Base64 续行永远不会长这样，改用 envKeyRe 判定。
func readDotEnv(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	var lastKey string
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.Index(line, "=")
		if idx < 0 || !envKeyRe.MatchString(line[:idx]) {
			if lastKey != "" {
				out[lastKey] += "\n" + line
			}
			continue
		}
		key := line[:idx]
		val := strings.Trim(line[idx+1:], `"`)
		out[key] = val
		lastKey = key
	}
	return out, nil
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}
