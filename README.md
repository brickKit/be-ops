# be-ops

BrickEnterprise 装配生成器。**不是 brickKit 组件**，不进 `brickkit.yaml`——它是我们自己的命令行工具，读全部组件的 `assembly.yaml` 产出平台刻意不做、而活不会消失的那些东西（总纲 §2.4，决策 89/93）。

## 11 个产出，10 个子命令

| 子命令 | 产出 # | 做什么 |
|---|---|---|
| `routes` | 1 | 网关路由表（三个出口，按组件是否进外壳分流） |
| `db-script` | 2, 2b | 建库脚本：`DATABASE`/`SCHEMA`/`ROLE`/授权/外壳登录角色 + `bindings` |
| `features` | 3 | Feature 清单，写进 IAM 适配层的 `enabledComponents` |
| `shell-config` | 4 | 外壳合并配置：谁进哪个外壳、端口、迁移顺序 |
| `gen` | 5, 11 | `brickkit.yaml` 生成（含 slot/channel 校验）+ `authzBundleUrl` 注入 |
| `registry` | 6 | 校验全局端口册与 schema 册自洽 |
| `shell-env` | 7 | 每外壳一份环境变量表（最容易被漏掉的一件） |
| `shell-depends` | 8 | `shell-compose` 的 `depends_on`：外壳之间的启动顺序 |
| `permissions` | 9 | 权限键册 `registry/permissions.tsv`（第 14 章） |
| `data-scopes` | 10 | 数据权限总表 `registry/data-scopes.tsv`（第 14 章） |

## 现状（阶段一 Task 6）

只有子命令清单骨架，逐个实现从 **Task 8** 起（先做 `db-script`/`gen`/`registry` 三个先决产出）。

## 三条生成器铁律（写进实现与测试时必须守）

1. Docker labels / K8s annotations 的值必须是字符串（`"true"` 不是 `true`）。
2. 没买的组件从 `brickkit.yaml` 整条删掉，不写 `enabled: false`（级联关闭的坑）。
3. 聚合型组件（`infra-bff-mobile`、`infra-notification`）的依赖必须全部 `optional: true`。

见总纲 §2.4 完整说明。
