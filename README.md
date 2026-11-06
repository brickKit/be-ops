# be-ops

BrickEnterprise 装配生成器。**不是 brickKit 组件**，不进 `brickkit.yaml`——它是我们自己的命令行工具，读全部组件的 `assembly.yaml` 产出平台刻意不做、而活不会消失的那些东西（总纲 §2.4，决策 89/93）。

## 11 个产出（编号固定），6 个子命令

⚠️ **产出 4（外壳合并配置）、产出 7（每外壳环境变量表）、产出 8（`shell-compose` 的 `depends_on`）已全部退休**——三者都是合并部署专用，随着 brickKit 自己的 `servedBy` 机制陆续长出原生能力，一个个被取代：产出 7/8 在 `servedBy` 迁移（阶段四附加 Task 0.2/0.3、0.5）里退休，"依赖地址改写"/外壳间启动顺序改由 brickKit 原生计算；产出 4（`shell-config` 子命令，曾经吸收了产出 7 剩下的"传 configSchema 解析结果"职责）在阶段四附加 Task 0.6 里最后退休——brickKit v0.4.2 新增 `BRICKKIT_SERVED_MEMBERS_CONFIG` 原生环境变量，外壳启动器直接解析它，不再需要这条命令生成一份平行数据。**编号保持不变，不重排**（`permissions`/`data-scopes` 仍按产出 9/10 引用）。完整调研过程见装配仓库 `docs/plans/05a-迁移到servedBy.md` Task 0.2/0.5/0.6。

| 子命令 | 产出 # | 做什么 |
|---|---|---|
| `routes` | 1 | 网关路由表（三个出口，按组件是否进外壳分流） |
| `db-script` | 2, 2b | 建库脚本：`DATABASE`/`SCHEMA`/`ROLE`/授权/外壳登录角色 + `bindings` |
| `features` | 3 | 部署的 `features` 文件：`brickkit.yaml` 选中的组件与精确版本；边缘（`be-edge`）只给已登录的会话（协议 20 UI25）。`be-ops features --root <装配项目> --out <文件>` |
| `registry` | 6 | 校验全局端口册与 schema 册自洽 |
| `permissions` | 9 | 权限键册 `registry/permissions.tsv`（第 14 章） |
| `data-scopes` | 10 | 数据权限总表 `registry/data-scopes.tsv`（第 14 章） |

⚠️ **产出 5/11（`gen`，生成 `brickkit.yaml`）已退休（v0.2.0）**：`brickkit.yaml` / `deploy.yaml` / `config/` 现在由 `brickkit add` 等命令维护，be-ops 不再生成。`db-script` 的外壳登录角色（`shell_go_core` / `shell_go_backoffice` / `shell_go_infra` / `shell_py_render`）与 `shell/be/<name>/component.yaml` 的 `shell.members` 对应：登录角色总是产出，成员的 `GRANT <成员角色> TO <外壳登录角色>` 随清单里的成员自动出现。

## `recover`：整套恢复（协议 15 的第 1–10 步）

`be-ops recover <步骤> --project <BrickKit 项目>`：`status`、`announce`（原因、批准人、T）、`abandon`、`freeze`、`restore-db`、`advance`、`redeliver`、`replay`、`keys`、`reconcile`、`check`、`loss`、`open-read`、`open`（`be-ops recover` 不带参数列出全部用法）。

- 恢复的状态记在运行档案（`recovery.step`，签名、对象锁）：每一步追加一条，状态是这些记录按顺序折叠的结果，所以它不随恢复的数据库回退；每一步先按协议 15 的状态表检查能不能做（`internal/recovery/state.go`），可以重做。
- 阶段经共用变量 `RECOVERY_PHASE` 下发、换阶段 `brickkit up`（A140），工具读每个容器的自述核对全部成员到了这个阶段（现在只认 Docker）。
- 第 3 步经 `infra/scripts/pg-backup.sh` 把实例恢复到临时实例，逐个 schema 拷回，审计的不拷；第 4 步按 P12.5 推进两种恢复代次（先写运行档案）；第 5 步删掉流，以 `redelivering` 重启，等每个消费者记下新的库纪元（它们按自己的进度向生产者要补发，线协议 P6.39），列出补发覆盖不了的缺口。
- 第 6 步（A141、A142）：`replay` 以 `replaying` 重启（身份成员启动时结束恢复前的会话，6.6），核验告警渠道里的意图（运行档案的签名在收到它的时刻按信任材料历史成立、写入者就是签名证书的服务身份（线协议 P12.4）且就是记录者才重放；其余列复核，所涉主体在 6.5 关闭，6.2；历史不用的信任记录告警并记进这一步，alpha.14 起），然后在部署网络里起一个 `alpine` 容器跑同一个二进制的 `replay-calls`（成员的系统面只在部署网络里可达：别名是 ID 加版本、端口取清单里名为 `grpc` 的那个）：读证据存储的全部记录、与告警渠道的合并，按 P12.22 排好调用各属主的 `ReplayService`（`order.go`），等授权成员应用来源（6.4），执行它推迟的只有意图的收紧、做不变量检查（6.5），清单写进运行档案；属主答 `deferred` 的（依赖的来源还没应用，例如授权成员解除身份侧处置时那次冻结还没重放到它那里）在 6.4 之后按原顺序再问一次，仍推迟的列复核（alpha.12 起，线协议 P12.22）；身份产品不在恢复组内时，凭据收紧由属主对照产品核对（6.7，线协议 P12.20，alpha.10 起）：答 `consistent` 的不用人看，`inconsistent` 的列复核、不关闭主体；`keys`（6.8）给配置里指着签名密钥文件的组件（身份成员）写一把新的当前密钥与新的备用，旧文件改名留在旁边。工具的二进制要以 `CGO_ENABLED=0` 构建（挂进 `alpine` 里运行）。只认 Docker；Kubernetes 上调用部分要作为集群里的一个 Job 运行，还没做。
- 第 7–10 步（alpha.8 起，`verify.go`、`verifycalls.go`、`verifyplan.go`；线协议第 13、14 章）：`reconcile` 以 `verifying` 重启（对外命令只查询），对每个发对外命令的成员（清单声明 `EXTERNAL_COMMANDS_QUERY_ONLY`）调 `ReconcileService`，带 T、往前留出的时长（`--window`，默认 1 小时）、证据存储里 T 之后它的发出记录与故障时刻（`--failed-at`）；`check` 调每个有库的成员的 `CheckService.Check`，全部通过才算通过；`loss` 读审计存储里 T 之后的记录（审计组件的 `RecordService.List`），业务变更与敏感读取按对象问属主 `Present`（版本取记录里最高的），列出丢了、更低、无法判断的（按用户与对象），各来源产生序号的空位与最后一条之后到故障时刻的窗口，以及第 7 步“外部有本地无”“可能已执行”的参考号（禁止重新录入）；`open-read`（`--approver`，检查没有全部通过时还要 `--reason`）以 `read_only` 重启；核对或只读开放中发现差异，`freeze --approver --reason` 重新冻结（alpha.11 起，RC75），回到恢复中，第 7 步起重做；`loss` 另列 T 之后的双通道失效（证据存储里类别 `evidence_double_failure` 的记录点名的收紧，alpha.11 起，线协议 P12.23），重放不处理它们；`open`（`--approver`）取每个成员的 `Numbers` 与审计里 T 之后的 `document_number`，较大者高于属主的就 `AdvanceNumbers`，然后去掉阶段重启，恢复结束。状态表的守卫“至少一名授权管理员能登录”由 `open-read` 核对（alpha.13 起，RC84）：在部署网络里问授权成员的状态，待恢复（可用管理员为零）时拒绝开放，先做部署所有者的管理员恢复；“全部验证方已刷新公钥集合”仍由批准人确认、记进开放记录。版本不认识某个阶段的成员（清单的枚举里没有）在那个阶段被停下（与没有恢复模式的一样）。
- `backup-record --finished-at T --backup ID`（alpha.15 起，`backuprecord.go`）不是恢复的一步：备份工具（`infra/scripts/pg-backup.sh`）每次备份成功后写一条运行档案的 `backup` 记录，组件释放归档单元的在线行时读它（线协议 P15.21）。
- 配置：环境变量里的运行档案（`RUN_PROFILE_*`）、部署签发的 `ops/recover` 服务证书（`SERVICE_TLS_*`）、实例的管理员 DSN（`RECOVER_PG_ADMIN`）、`BUS_URL`；部署身份取项目的 `config/vars.yaml`。
- 测试：`go test ./internal/recovery/`（运行档案、命令、成员的阶段与第 6–10 步的调用用替身；gRPC 的调用者 `calls.go`、`verifycalls.go` 的后半只在演练里走）；`go test -tags integration ./internal/recovery/`（第 4、5 步对真的库，`BE_SDK_TEST_PG_ADMIN`）。真的运行档案与外部命令在真机格的演练里走一遍。

## `trust`：部署信任域的签发者（线协议 P12.24–P12.27，A148）

`be-ops trust <命令> --project <BrickKit 项目>`：`init`（部署的 CA）、`issue --component ID [--role R]`（服务凭据：组件的 SPIFFE ID 与 DNS 名）、`rotate`（下一把 CA，由当前的签名、下一把接受）、`retire --fingerprint`（接受窗口之后把轮换掉的 CA 移出信任根，不是吊销）、`revoke --fingerprint [--ca]`、`flush`、`history`（`be-ops trust` 不带参数列出用法）。

- 信任材料只记在运行档案（`trust.ca`、`trust.credential`，由 CA 自己的私钥签名）：它在部署之外、对象锁、不随恢复回退；整套恢复的 6.2 按收到每条记录的时刻对照这段历史核验（受信的 CA 从信任根沿双签的轮换往前，吊销按接收时刻减声明的时钟偏差）。告警渠道是运行档案的一个类别，证据存储不留副本。
- 顺序：签发与轮换先写运行档案、得到成功，才写出凭据或新 CA（写不进就什么都不发布）；吊销不推迟：CA 先移出信任根，再写记录，写不进时记录留在 `--dir/pending/`、命令报错，运行档案可达后 `flush` 补写（接收时刻是补写的时刻）。
- 吊销当前的 CA（运行档案不可达、无法先轮换出新链时）让部署没有 CA：文件改名为 `*.revoked`，`init` 建新的，全部凭据重新签发。服务凭据的吊销在运行时还不生效（SDK 没有吊销列表，连接按证书有效期接受；安全 03 的连接处置），要给组件签发新凭据；恢复里由历史判它签的记录无效。
- 文件：`--dir`（默认 `<项目>/.secrets/trust`）放 CA 私钥，不挂进任何组件、不在恢复组里；`--bundle`（默认 `<项目>/.secrets/tls/ca.pem`）是组件读的信任根（`SERVICE_TLS_CA_FILE`）。环境变量是运行档案的 `RUN_PROFILE_*`；部署身份取项目的 `config/vars.yaml`。
- 测试：`go test ./internal/trust/`（真的 `runprofile.Store` 对一个内存里的对象存储替身：签发、轮换、退役、吊销、运行档案不可达时的留存与补写，都按历史核对）。

## 现状

产出 4/7/8 已全部退休，见上方说明。其余命令的现状记录起点是阶段三 Task 3：已实现 `registry check`（产出 6）、`db-script`（产出 2、2b）、`permissions`（产出 9）、`data-scopes`（产出 10）。均已用真实数据跑通——`registry/ports.tsv`（62 组件）+ `schemas.tsv`（54 行），`db-script` 产出的 SQL 已对真实 PostgreSQL 执行两遍验证幂等；`permissions`/`data-scopes` 已对阶段二五个真实组件的 `assembly.yaml` 跑通，产出 21 条权限键 + 4 条数据权限声明。`features` 已实现（底层先行第 2 项）；`routes` 仍留待路由设计落地时再实现。

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
