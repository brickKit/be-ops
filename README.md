# be-ops

BrickEnterprise 装配生成器。**不是 brickKit 组件**，不进 `brickkit.yaml`——它是我们自己的命令行工具，读全部组件的 `assembly.yaml` 产出平台刻意不做、而活不会消失的那些东西（总纲 §2.4，决策 89/93）。

## 11 个产出（编号固定）与子命令

⚠️ **产出 4（外壳合并配置）、产出 7（每外壳环境变量表）、产出 8（`shell-compose` 的 `depends_on`）已全部退休**——三者都是合并部署专用，随着 brickKit 自己的 `servedBy` 机制陆续长出原生能力，一个个被取代：产出 7/8 在 `servedBy` 迁移（阶段四附加 Task 0.2/0.3、0.5）里退休，"依赖地址改写"/外壳间启动顺序改由 brickKit 原生计算；产出 4（`shell-config` 子命令，曾经吸收了产出 7 剩下的"传 configSchema 解析结果"职责）在阶段四附加 Task 0.6 里最后退休——brickKit v0.4.2 新增 `BRICKKIT_SERVED_MEMBERS_CONFIG` 原生环境变量，外壳启动器直接解析它，不再需要这条命令生成一份平行数据。**编号保持不变，不重排**（`permissions`/`data-scopes` 仍按产出 9/10 引用）。完整调研过程见装配仓库 `docs/plans/05a-迁移到servedBy.md` Task 0.2/0.5/0.6。

| 子命令 | 产出 # | 做什么 |
|---|---|---|
| `routes` | 1 | （未实现，已由 `edge` 取代：路由生成进部署条目） |
| `db-script` | 2, 2b | 建库脚本：每组件属主 / 运行两个 LOGIN 角色、schema 归属主、运行角色只有 DML、外壳 NOINHERIT 授权、角色级超时 |
| `features` | 3 | （未实现） |
| `registry` | 6 | 校验全局端口册与 schema 册自洽 |
| `permissions` | 9 | 权限键册 `registry/permissions.tsv`（第 14 章）；`--check`、`--base <已提交版本>` 查只增 |
| `data-scopes` | 10 | 数据权限总表 `registry/data-scopes.tsv`（第 14 章） |
| `config-schema` | — | `component.yaml` configSchema 的协议键段（be-protocol P2.8 / P2.12） |
| `events` | — | `component.yaml` 的 `events:` 段（be-protocol P12.16） |
| `edge` | — | 部署条目的 Traefik 路由标签 / K8s `paths`、`infra/traefik/dynamic/edge.yml`（foundations 18） |
| `resources` | — | 授权声明校验、`registry/resource-types.tsv`（只增）、`registry/resource-catalog.json`（RESOURCE_CATALOG） |
| `data-subjects` | — | `registry/data-subjects.tsv` 覆盖每个组件 `lifecycle.yaml` 的擦除主体 |
| `authzgen` | — | 每个组件的 authzgen 源文件（Go / Python / TS） |
| `openapi` | — | 把资源契约（be-protocol `openapi/resource-authz.yaml`）并进组件发布的 OpenAPI；查每个操作都声明了守卫 |
| `gates` | — | 列出 be-acceptance 门禁（A7）调用的全部 be-ops 命令；`--run` 在本进程里跑 |

## v0.3.0：3.0.0 布局（O1、O2，sdk-redesign §7.7）

be-protocol 钉在 `go.mod`（`github.com/brickKit/be-protocol v1.0.0-rc.1`），`config-keys.yaml`、事件 subject 规则、能力枚举、assembly schema 直接从模块内嵌文件读，不复制。

**生成器一律幂等、输出确定；`--check` 不写文件，不是最新就退出 1。** 门禁调用的命令以 `be-ops gates --root .` 打印的表为准（A7 照它接，不另抄一份）；`be-ops gates --run --root . --permissions-base <文件>` 在本进程里全跑一遍，`--gate <名字>` 只跑选中的：

| 门禁（be-acceptance） | 命令 | 比较方式 |
|---|---|---|
| `protocol-config-scan` | `be-ops config-schema --check --root .`（组件仓库里：`--component .`） | 语义：协议键的名字、type、default、secret、mount、required 成员；另查自有键的 P2.4 / P2.12；外壳另查 stopGracePeriodSeconds 不低于任何成员（P19.9） |
| `events-declaration-scan` | `be-ops events --check --root .` | 集合：publishes = 事件契约减去 poke，subscribes = fixtures 的 `events.consumes` |
| `openapi-fresh` | `be-ops openapi --check --root .` | 资源契约区段逐字节；每个非 `x-be-internal` 操作都有守卫 |
| `authzgen-fresh` | `be-ops authzgen --check --root .` | 逐字节 |
| `resources-fresh` | `be-ops resources --check --root .` | 校验 + `resource-types.tsv` / `resource-catalog.json` 逐字节 |
| `data-subjects-cover` | `be-ops data-subjects --root .` | `data-subjects.tsv` 覆盖每个 `lifecycle.yaml` 的擦除主体 |
| `permissions-append-only` | `be-ops permissions --check --root . --base <git show HEAD:registry/permissions.tsv 的文件>` | 逐字节最新 + 相对已提交版本只增 |
| `edge-routes-fresh` | `be-ops edge --check --root .` | 语义：每个部署条目由 be-ops 拥有的字段（`traefik.*` 标签、`expose` / `hostname` / `tlsSecret` / `paths`、`k8s.ingressAnnotations`），`edge.yml` 逐字节；外加 OpenAPI 路径都在声明的前缀下 |
| `registry-check` | `be-ops registry check --root .` | 端口册与 schema 册自洽 |

- 不带 `--component` 时，`config-schema` / `events` / `authzgen` / `openapi` 只处理 `assembly.yaml` 声明了 `protocol` 的组件和外壳（`components/*/*`、`shell/*/*`；3.0.0 组件、1.1.0 外壳）；`--all` 连 2.x 一起。**1.1.0 外壳因此要有一个 `assembly.yaml`，至少写 `id` 和 `protocol: "1.0"`。**
- **profile 怎么定**（与 be-protocol `conformance-cases.yaml` 的选取规则一致）：core / obs / err 恒有；auth = 有非 `public` 的 OpenAPI 操作、`auth: required` 的边缘路由或已声明的 auth 键；grpc = 名为 `grpc` 的额外端口；events-pub / events-sub = 事件契约 / fixtures；db（连同 jobs、lifecycle）= 声明了 `PG_SCHEMA` 或 `PG_HOST`；blob = 声明了 `S3_BUCKET` 或 `S3_URL`。`applies_when` 键：拥有带 `share` 的资源类型时自动加 `AUTHZ_GRPC_URL`，其余（`IAM_GRPC_URL` 等）声明了就保留。
- **外壳自身的协议键段**：profile = core / obs / err ∪ 各成员的 profile（成员从 `--root` 的 `components/<id>` 读）；键 = core / obs / err 的全部键，加上并集里出现的进程级键——P19.3 的共享地址（`AUTHZ_URL`、`IAM_URL`、`IAM_ISSUER`、`TENANT_ID`、`PG_HOST`、`PG_PORT`、`PG_DATABASE`、`EVENT_BUS_URL` / `NATS_URL`）、外壳自己的登录（`PG_USER`、`PG_PASSWORD_FILE`）、物理池（`PG_POOL_MAX` 默认 40、`PG_POOL_MIN_IDLE`、`PG_CONN_MAX_*`）。schema、属主、迁移、消费者参数、`*_GRPC_URL`、对象存储、jobs、lifecycle 都是成员各自的，外壳不声明。
- **资源契约合并（`openapi`）**：声明了 `resources` 的组件，在 servers url 为 `/<id>` 的那份 `contracts/*.openapi.yaml` 的 `paths` 块末尾、两行 `# >>> be-ops: resource contract …` / `# <<< be-ops: resource contract` 标记之间写入 `_authz/check`、`_authz/explain`，并给每个带 `share` 规则的资源类型写一组 `_shares/<type>/{id}`（写操作的守卫是该类型的 share 键）；`$ref` 全部内联，手写行逐字节不动，没有 `resources` 时删除区段，手写路径与契约撞车报错。
- **守卫失败即关闭**：每个非 `x-be-internal`（或 `provider` 标签）的操作必须声明 `x-be-permission`——本组件声明的 page / action 键、`authenticated` 或 `public`；缺失、未声明的键、field 键、占位符都是问题，从不按"默认受保护"放过。
- **`permissions.tsv` 的 `delegable` 列**：列按表头名字读，只在末尾追加；每行至少写前五列，后加的列只写到该行最后一个非空列，所以加列只改表头、旧行逐字节不变。值取自 `assembly.yaml` 的 `delegable`（`true` / `false`，空 = 未声明）；空格子可以填，已发布的值不改。`--base` 的只增规则：表头只能在末尾加列，旧键不删，非空旧值（type、owner_component、deprecated 墓碑、delegable）不变，title 是展示文字可以改。父仓库 `make permissions` 里"git diff 不许有减号行"那条检查要换成 `--base`（加列会改表头）。
- **事件总线 `be_bus`（db-script）**：项目的 `EVENT_BUS_URL` 是 `postgres://…/<库>?schema=be_bus` 时才建（全项目只能一个地址，P12.12；schema 必须叫 be_bus）。DDL 原样取自钉住的 be-protocol `ddl/be_bus.sql`，另加一个 DEFAULT 分区；NOLOGIN 角色 `be_bus_owner` 拥有 schema 和全部对象；声明了总线的组件运行角色、以及自身 EVENT_BUS_URL 是该队列的外壳登录角色得到 `USAGE` + 四张表 `SELECT, INSERT, UPDATE, DELETE` + 序列 `USAGE`，属主角色一概没有。分区维护（谁建新分区、谁删旧分区）规范还没定，见 be-protocol rc.2 待定项。
- **poke 不进 `events:`**：事件契约里标 `x-signal: true` 或带 `transport` 的条目、顶层 `signals` / `x-signals` 都不算发布。
- **edge**：设置文件 `infra/edge.yaml`（`host`、`tlsSecret`、`entrypoints`、`rate`、`ingressAnnotations`）。Docker 上的 router 规则是 ``Host(`h`) && PathRegexp(`^/prefix(/|$)`)``，优先级 = 前缀长度，名字 `<scope>-<name>-<i>` 不带版本；外壳条目同时带运行中成员的 router（服务端口是成员端口）。已用 Traefik v3.6.25 + Docker 29.7.1 真机验证（段边界、外壳成员、Host、413、剥头）；**Traefik v3.3.7 连不上 Docker 29**（客户端 API 1.24），foundations 18 写的"≥ 3.2"不够。
- **authzgen** 默认路径：Go `backend/internal/authzgen/authzgen.go`（`besdk.PermKey` / `besdk.ResourceType` 常量 + `CatalogJSON`）、Python `backend/app/authzgen.py`（`Final` 常量 + `CATALOG_JSON`）、TS `src/authzgen.ts`（`as const` + `catalog`）；常量名去掉 domain（`erp.sales.pricing.read` → `SalesPricingRead` / `SALES_PRICING_READ`）。
- **db-script**：名字全部来自 `config/<scope>-<name>.yaml`（`PG_OWNER_USER`、`PG_USER`、`PG_SCHEMA`、`PG_DATABASE`，`$var:` 经 `config/vars.yaml` 和部署文件 `vars:`）；口令只以 psql 变量出现，`be-ops db-script --password-vars` 打印 `<变量>\t<env:NAME|file:PATH>` 给 db-init 先 `\set`。真库测试：`docker run -d --name sdkb-ops-pg16 -e POSTGRES_PASSWORD=x postgres:16-alpine` 后 `make test-pg`。

## 现状

产出 4/7/8 已全部退休，见上方说明。其余命令的现状记录起点是阶段三 Task 3：已实现 `registry check`（产出 6）、`db-script`（产出 2、2b）、`permissions`（产出 9）、`data-scopes`（产出 10）。均已用真实数据跑通——`registry/ports.tsv`（62 组件）+ `schemas.tsv`（54 行），`db-script` 产出的 SQL 已对真实 PostgreSQL 执行两遍验证幂等；`permissions`/`data-scopes` 已对阶段二五个真实组件的 `assembly.yaml` 跑通，产出 21 条权限键 + 4 条数据权限声明。`routes`/`features` 仍留待各自先决条件成熟（路由设计落地）时再实现。

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
