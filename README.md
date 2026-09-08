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

## 现状（阶段三 Task 3）

已实现 5 个：`registry check`（产出 6）、`db-script`（产出 2、2b）、`gen`（产出 5，含 `resources[].bindings` 自动挂载）、`permissions`（产出 9）、`data-scopes`（产出 10）。均已用真实数据跑通——`registry/ports.tsv`（62 组件）+ `schemas.tsv`（54 行），`db-script` 产出的 SQL 已对真实 PostgreSQL 执行两遍验证幂等；`permissions`/`data-scopes` 已对阶段二五个真实组件的 `assembly.yaml` 跑通，产出 21 条权限键 + 4 条数据权限声明。其余 5 个（`routes`/`features`/`shell-config`/`shell-env`/`shell-depends`）留待各自先决条件成熟（路由设计落地）时再实现。

### `permissions`/`data-scopes` 的判据（`internal/authzreg`）

- **`permissions.tsv` 只增不改**（导读四张钉死的表之一）：已发布的 key 永远保留，重跑本命令绝不删行——
  只增量合并（新 key 直接加、已有 key 允许刷新 title/type/owner_component，`deprecated` 列本命令从不
  设置也不清空）。这次扫描没声明、也没标 `deprecated` 的"孤儿" key 会打印警告，但仍然保留在表里，交给人
  决定要不要手工打墓碑。同一个 key 被两个不同组件声明会直接报错（权限键必须全局唯一）。
- **`data-scopes.tsv` 纯派生**（`registry/README.md`：不需要防改）：每次全量重生成，不接受历史基线。
- **省略 `data_scopes` 段当场报错**（导读第 22 条）：不需要也要显式写 `data_scopes: none`，省略 ≠ none。
  区分"完全省略"与"标量 none"靠 `*dataScopesField`（指针类型）——`yaml.v3` 对文档里不存在的字段保留
  指针零值 `nil`，只有字段真的出现过才会分配并调用 `UnmarshalYAML`（见 `internal/authzreg/load.go` 注释）。

⚠️ **`v0.1.2` 修了一个真实数据踩出来的坑**：`dbscript.Gen()` 原来只对表做 `ALTER DEFAULT PRIVILEGES ... ON TABLES`，没管 `BIGSERIAL` 自增列背后的 SEQUENCE——PostgreSQL 里两者是独立的权限对象，只授权表会让每一张用自增主键的表（全项目标准写法，§11.2.1）第一次 `INSERT` 就报 `permission denied for sequence`。`mdm-customer` 真的跑 `Create` 才暴露，现在两条 `ALTER DEFAULT PRIVILEGES` 都会产出。

## 三条生成器铁律（写进实现与测试时必须守）

1. Docker labels / K8s annotations 的值必须是字符串（`"true"` 不是 `true`）。
2. 没买的组件从 `brickkit.yaml` 整条删掉，不写 `enabled: false`（级联关闭的坑）。
3. 聚合型组件（`infra-bff-mobile`、`infra-notification`）的依赖必须全部 `optional: true`。

见总纲 §2.4 完整说明。
