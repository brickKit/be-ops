# be-ops

装配与运维的命令行工具（不是 BrickKit 组件）：装配时的生成与校验，整套恢复（`recover`），部署信任域的签发者（`trust`）。每个子命令做什么、产出什么、怎样用，在 [README.md](README.md)；依据是装配项目 be-assembly-standard 的协议 15（整套恢复）、协议 19（注册表）与线协议第 12–15 章。

## 代码地图

`cmd/be-ops/main.go` 只分派子命令：生成类的子命令（`registry`、`db-script`、`permissions`、`data-scopes`、`features`）在这里读参数、调 `internal/` 的包；`recover`、`trust` 整个交给各自包的 `Main`。

| 包 | 子命令 | 负责 |
| --- | --- | --- |
| `internal/registry` | `registry check` | 读 `registry/` 下的全局册子（端口、schema），校验自洽 |
| `internal/dbscript` | `db-script` | 建库脚本：建库，每个组件的 schema 与角色；外壳的成员从 `shell/be/*/component.yaml` 读 |
| `internal/authzreg` | `permissions`、`data-scopes` | 权限键册与数据权限总表；只增不改（读既有的 `registry/permissions.tsv` 对照） |
| `internal/features` | `features` | 部署的 features 文档（协议 20） |
| `internal/recovery` | `recover …` | 整套恢复的第 1–10 步，状态记在运行档案里（签名、对象锁定）；另有 `backup-record`（备份工具写备份记录） |
| `internal/trust` | `trust …` | 部署信任域的签发者（线协议 P12.24–P12.27） |

`internal/recovery` 按恢复的步分文件：

| 文件 | 内容 |
| --- | --- |
| `state.go` | 恢复的状态表：运行档案里的各步记录折叠成当前状态（`Fold`），哪一步此刻允许（`Allowed`）、之后是哪一步（`After`）、重新冻结（`Refreeze`） |
| `recover.go` | `Tool` 与第 1–5 步：`Status`、`Announce`、`Abandon`、`Freeze`、`RestoreDB`、`Advance`；运行档案 |
| `cli.go` | `Main`：子命令与参数；工具自己的服务身份；Docker 上按阶段重启（`DockerPhases`） |
| `project.go` | 读装配项目：哪些组件恢复、各阶段谁不支持要停、谁发对外命令、谁有核对 |
| `order.go` | 第 6 步的顺序（P12.22）：证据记录解析、排出重放计划（`PlanReplay`） |
| `replaystep.go` | 第 6 步在部署机上的部分：以 `replaying` 重启，核验告警渠道，在部署网络里起容器跑 `replay-calls` |
| `replay.go` | 第 6 步在部署网络里的部分：按计划调各属主的 `ReplayService`，等来源应用，推迟的再问一次（`RunReplay`） |
| `calls.go` | 第 6 步的 gRPC 调用（`GRPCCaller`：`Replay`、`Sources`、`ReplayDeferred`、`Check`、`ListEvidence`） |
| `keys.go` | 6.8：身份成员换签名密钥与备用密钥 |
| `verify.go` | 第 7–10 步在部署机上的部分：`Reconcile`、`Check`、`Loss`、`OpenRead`、`Open` |
| `verifyplan.go` | 第 7、9、10 步的纯函数：T 之后发出的、双通道失效、审计记录按对象问属主、判丢失、序号的空位 |
| `verifycalls.go` | 第 7–10 步在部署网络里的调用（`VerifyCalls`；`Reconcile`、`AdminState`、`MemberChecks`、`Present`、`Numbers`） |
| `backuprecord.go` | `backup-record`：运行档案的备份记录（线协议 P15.21），不是恢复的一步 |

每个文件旁边是它的 `_test.go`；`recover_integration_test.go` 是集成测试。

## 构建与测试

- `make test`（`go test ./... -race`）：单元测试。
- `go test -count=1 -tags integration ./internal/recovery/`：真实的 PostgreSQL，环境变量 `BE_SDK_TEST_PG_ADMIN`（与 be-sdk-go 的集成测试同一个，装配项目的 `itenv.sh` 设置）。
- `make import-scan`：不依赖任何组件仓库的代码（be-sdk-go、be-protocol 与组件发布的契约模块除外）。
- 恢复的调用部分在部署网络里的 `alpine` 容器中运行同一个二进制：构建时 `CGO_ENABLED=0`。

## 版本与发布

- 用的 be-sdk-go 与系统里其余的 Go 模块是同一个版本：SDK 升级时跟着一起换（装配项目的 `make version-check` 里 pin-check 会点出落下的）。
- 发布是带注释的标签 `v3.0.0-alpha.N`，每次改动只升最后一位；没有清单文件，版本只在标签上。
