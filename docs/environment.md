# MO 环境、缓存配置与监控

## 先看环境

```sh
./mo-search-lab inspect --host 10.0.0.10 --port 6001 --user BENCH_USER \
  --password '你的密码' \
  --mo-config /path/to/cn1.toml --mo-config /path/to/cn2.toml \
  --environment-file /path/to/deployment.json \
  --report-dir reports/environment-001
./mo-search-lab ui --report-dir reports/environment-001 --section environment
# 纯文本展开诊断详情
./mo-search-lab ui --report-dir reports/environment-001 --section environment \
  --plain --environment-details
```

SQL 密码通过 `--password` 直接传入，省略表示空密码；交互式 UI 遮蔽显示并可修改，密码不写入报告。`inspect` 仅执行只读 SQL：`VERSION()`、版本注释、构建时间、Git 提交、选定会话默认变量、`SHOW BACKEND SERVERS` 及 `mo_catalog.mo_configurations` 的允许字段。不建库、不建索引、不导入或压测。版本查询失败时检查失败；额外信息不支持或权限不足时展示未知与原因。

`run` 默认也采集这些信息，三个环境参数均可追加到原有 `run` 命令。所有参数可省略；仅连接 SQL 时服务端硬件、完整拓扑及部署类型保持未知。SQL 可见 CN 与客户端并发、SQL 端点数分别记录；受账号、标签和路由影响，不能单凭它确认整个集群的 CN/TN 数量。

## 自动获取节点上报配置

v0.9.1 起，连接 SQL 即可从 `mo_catalog.mo_configurations` 获取节点上报记录，无需提供 TOML：

- 按节点类型和 UUID 记录 CN、TN、LOG、PROXY 的配置上报节点。它们与 `SHOW BACKEND SERVERS` 的账号可见 CN 分开保存，不能用于推断节点当前健康状态或拓扑完整性。
- 相关 FileService 的名称、后端、内存/磁盘缓存容量、磁盘缓存路径，保留 `current_value` 与 `default_value`。v0.9.2 起 SQL 按节点与 FileService 名称排除 ETL、TMP，不假设它们的数组位置；TOML 也不提取这两类服务。v0.9.3 起不采集或展示跨节点缓存开关。
- MO 配置内存上限、Metadata 缓存容量、Frontend 内存池上限与 Pipeline 内存参数。逻辑内存上限不等于容器或机器的内存配额；v0.9.2 起不采集 Iceberg 配置。

字段定义见 [MO 系统表文档](https://docs.matrixorigin.cn/mo/zh/latest/MatrixOne/Reference/System-tables.html)。MO 源码通过节点心跳向 HAKeeper 上报配置快照；在本次验证的 4.2.1（`d239386`）中，节点启动后前 50 次成功心跳携带这份配置，HAKeeper 随后保留它，视图没有配置上报时间。参见 [配置生成与发送计数](https://github.com/matrixorigin/matrixone/blob/d2393868a7aaa6343518d80849fe4696aa3577e9/pkg/util/dump_config.go#L267)。本工具标注“节点启动配置快照，上报时间未知”，不把采集时间当成配置更新时间，也不保证快照反映动态修改、缓存实际启用或实时占用。仅查询白名单字段，并再次检查返回字段，排除存储凭据；不执行 `SELECT *`。最多 256 个上报节点、2048 行筛选后的配置，超限或无记录时标记未采集并保留原因。

例：TOML 没有写 `memory-capacity`，SQL 上报 SHARED 快照值和默认值均为 `536870912`，主体表格显示 `512 MiB`，展开后可分别核对快照、默认值和文件显式值。`nil`、零值和未采集分开处理，不补成零。

## 报告结构

v0.9.8 起，性能报告顶部提供 **性能测试 / 运行环境** 两个 Tab，默认展示性能图表。“运行环境”直接展示同一次运行保存的环境信息及诊断详情，无需额外展开。切换支持键盘左右方向键、Home/End 和 `#report-environment` 链接；打印或禁用 JavaScript 时两个部分均可阅读。历史报告没有环境记录时说明未采集；`inspect` 保持独立环境页面。

网页与 CLI 使用同一份整理后的视图：

1. **版本与部署**：MO 版本、连接入口、构建身份、部署方式、资源配额与作用范围、SQL 可见 CN 和配置上报节点。
2. **节点与缓存**：相同角色、相同配置合并为一行，例如 `CN ×10`、`LOG ×3`；表中容量均为每节点值，不累加。名称统一为内存缓存、Metadata 缓存、磁盘缓存；Metadata 缓存按节点展示，存储缓存按 FileService 展示。非 Working CN 的采集时状态直接提示，具体节点放在诊断详情；完整 UUID 和地址保留在原始记录。
3. **检索参数**：按全文、IVF、HNSW、向量过滤策略分组，注明它们是初始 SQL 会话值。v0.9.5 追加 IVF 训练采样比例/最大迭代次数、HNSW 建索引/搜索线程与容量参数、默认 Pre-filter/Auto 策略。仅展示服务实际返回的变量，不从代码默认值补齐旧版本或历史记录。
4. **资源监控**：接入状态、监控目标、查询时间窗和资源曲线。

v0.9.7 将补充信息收敛为一个“诊断详情”：仅展示配置与默认值/文件显式值的差异、非 Working 的 SQL 可见 CN、SQL 探测失败原因。有配置文件值时才显示该列；文件缺省不会被误报为冲突，Metadata 原始零配置与按环境推算的默认容量不会被误报为差异。差异比较使用原始值，容量舍入相同时补充字节值，避免隐藏真实差异。没有诊断条目时不展示空展开项。

全部节点 UUID/IP、文件身份/校验和、逐节点内存来源和重复声明只在原始 JSON 保留。纯 `inspect` 页面不展示客户端配置；性能报告用一行说明压测客户端的主机、系统、CPU，客户端资源不当作 MO 配额。显式声明的 MO 节点 CPU/内存配额按相同角色与配额合并，展示每节点值。

CLI 环境页按 `e` 展开/收起诊断详情，纯文本用 `--environment-details`。性能运行身份、输入哈希和测量参数仍在各自的测量详情中可追溯。

历史报告也按这个结构展示，隐藏已排除的配置；重渲染只更新 HTML，保留原始 JSON。

既有数据集汇总页面可在准备阶段加入独立 `inspect` 结果：

```sh
python3 tools/render_report_tabs.py --reports-root /data/reports \
  --environment-report environment-001 --layout-report benchmark-001
```

`benchmark-001` 与 `environment-001` 的 HTML 先由当前二进制生成。脚本复用二进制的环境视图和 Tab 控件，更新首页、GIST1M、T2Ranking 汇总页；页面明确标注“独立环境检查”和采集时间，不把其他环境的配置当作性能测量环境。原始 JSON 不变。T2 汇总发布命令也接受这两个参数。客户现场新跑的单次性能报告自带 Tab，无需 Python。

Metadata 缓存未设置或配置为 `0` 时，v0.9.6 根据受测节点的系统总内存与已确认的 MO 默认规则显示实际数值，例如 `2 GiB（环境默认，推算）`。默认值规则如下：

| 系统总内存 | Metadata 缓存默认容量 |
|---|---:|
| <2 GiB | 总内存的 ¼ |
| 2～<16 GiB | 512 MiB |
| 16～<32 GiB | 1 GiB |
| ≥32 GiB | 2 GiB |

当前规则已核对 MO v4.2.1（`d2393868a`）。Git 提交存在时按已核对的提交匹配；未返回 Git 提交时可按准确的发布版本匹配。其他构建显示“默认容量未知”，待核对后增加规则。Linux 的默认算法读取 `/proc/meminfo` 的系统总内存，不能替换为 Pod 内存 limit、K8s allocatable 或工具机器的内存。缺少服务端系统总内存时也显示“默认容量未知”。

通过 `--environment-file` 提供 `system_memory_total_bytes` 和可选 `system_memory_source`。单机可放在顶层并注明 `scope`；多节点应放在 `nodes` 中，按角色与 SQL 节点 UUID 匹配，节点值优先于顶层值。K8s 可以导出 Pod 到 Node 的对应关系和 Node `status.capacity.memory`，裸机可以从 MO 实际运行环境导出 `/proc/meminfo`。报告保存每节点原始输入及其来源；相同默认容量按角色合并，容量不累加。

快照列保留“配置值 0”含义，主体缓存表及默认值列显示按环境推算的容量。显式配置优先于默认推算；TOML 缺省单独标明，不能证明运行时已加载。推算值表示当时环境的初始默认容量，运行时容量覆盖未确认。参见 [默认容量算法](https://github.com/matrixorigin/matrixone/blob/d2393868a7aaa6343518d80849fe4696aa3577e9/pkg/objectio/cache.go#L100) 和 [配置覆盖条件](https://github.com/matrixorigin/matrixone/blob/d2393868a7aaa6343518d80849fe4696aa3577e9/cmd/mo-service/config.go#L304)。原始零值保留。

`lists`、距离类型、HNSW 的 `M/ef_construction/ef_search`、全文 parser 等属于具体索引定义，当前 `inspect` 不枚举客户表/索引；测试场景的建索引和查询 SQL 可用于核对本工具实际采用的设置。IVF 训练、HNSW 容量有索引级覆盖时，初始会话值不能代表该索引最终配置。已核对的版本中 `ivf_preload_entries` 和 MySQL 兼容的 `ft_min_word_len` 等仅有变量定义，未发现检索实现读取，因此不把它们当作有效调优项加入报告。

## 提供实际 MO TOML

配置文件需要在工具运行机器上可读；远程部署可由运维从实际挂载文件、ConfigMap 或裸金属配置目录导出。工具不会通过 SQL、SSH、Docker 或 K8s 自动读取远端文件。

`--mo-config` 可重复，最多 16 个文件。每份记录文件名、SHA256、节点类型/UUID，并提取：

- 相关 `[[fileservice]]` 的 `name`、`backend`，跳过 ETL、TMP。
- `[fileservice.cache]` 的 `memory-capacity`、`disk-capacity`、`disk-path`。
- `[metacache]` 的 `memory-capacity`。
- `cn.Pipeline` 的 `host-size`、`guest-size`、`batch-size`；`cn.frontend.mempoolMaxSize`。

```toml
service-type = "CN"
[cn]
uuid = "cn1"
[[fileservice]]
name = "SHARED"
backend = "S3"
[fileservice.cache]
memory-capacity = "512MB"
disk-capacity = "8GB"
disk-path = "/data/cache"
```

报告以 `512 MiB`、`8 GiB` 展示配置容量。与 MO 的 `ByteSize` 一致，MB/GB 按 1024 的幂解释；原始容量与字节数保留在 JSON。原始零值保留，Metadata 的零覆盖值显示为使用默认容量；未提供的字段保留为文件缺省。文件值不证明进程已加载，也不等于实测占用；不同节点/不同 FileService 分开显示，不自动相加。报告保存筛选后的配置，不复制 TOML 原文或存储凭据。

## 补充部署与资源声明

`deployment.json` 示例：

```json
{
  "deployment": "kubernetes",
  "scope": "namespace customer-mo / CN pods",
  "cn_count": 2,
  "tn_count": 1,
  "nodes": [
    {"role": "CN", "id": "cn1", "cpu_limit": 8, "memory_limit_bytes": 17179869184, "system_memory_total_bytes": 68719476736, "system_memory_source": "host /proc/meminfo"},
    {"role": "CN", "id": "cn2", "cpu_limit": 8, "memory_limit_bytes": 17179869184}
  ],
  "notes": "节点配额由运维导出；此次仅测试所选 SQL 入口"
}
```

部署类型为 `docker`、`kubernetes`、`bare-metal` 或 `unknown`。单节点部署也可在顶层填写 `cpu_limit`、`memory_limit_bytes`，此时必须用 `scope` 说明配额属于哪一节点/容器。可补充 `host`、`os`、`cpu_model`。这些值均标注“客户声明”，不会覆盖 SQL 实测证据。客户端 OS、架构、主机名及可用逻辑 CPU 另行采集。

## 可选 Grafana / Prometheus

`monitoring.json` 示例（需要按现场目标修改）：

```json
{
  "provider": "grafana",
  "url": "https://grafana.example.com",
  "datasource_uid": "prometheus-uid",
  "token_env": "MO_BENCH_MONITOR_TOKEN",
  "scope": "namespace=customer-mo, pod=cn-.*",
  "step": "15s",
  "timeout": "5s",
  "queries": [
    {
      "name": "CN CPU 使用量",
      "query": "sum by (pod) (rate(container_cpu_usage_seconds_total{namespace=\"customer-mo\",pod=~\"cn-.*\",container=\"main\"}[1m]))",
      "unit": "cores"
    },
    {
      "name": "CN 内存工作集",
      "query": "sum by (pod) (container_memory_working_set_bytes{namespace=\"customer-mo\",pod=~\"cn-.*\",container=\"main\"})",
      "unit": "bytes"
    }
  ]
}
```

```sh
read -r -s -p '监控 Token：' MO_BENCH_MONITOR_TOKEN
export MO_BENCH_MONITOR_TOKEN
./mo-search-lab run --host 10.0.0.10 --port 6001 --user BENCH_USER \
  --pack packs/gist_1m_filtered_v7 --monitoring-config monitoring.json \
  --mo-config cn1.toml --report-dir reports/gist-001
```

cAdvisor 指标、namespace、pod、container 标签仅是示例。裸金属可改成目标实例的 process/node 指标或现场已有 MO 指标。查询必须明确限定受测服务；`scope` 是报告中的声明，不会自动改写 PromQL。

Grafana 支持唯一或唯一默认 Prometheus 数据源的自动发现；有歧义时填写 UID。只读代理和发现接口见 [Grafana 官方 API](https://grafana.com/docs/grafana/latest/developer-resources/api-reference/http-api/api-legacy/data_source/)。直接连接 Prometheus 时将 `provider` 改为 `prometheus`，URL 指向基础地址并删除 `datasource_uid`，使用 [Prometheus `query_range` API](https://prometheus.io/docs/prometheus/latest/querying/api/)。URL 可包含部署子路径。

Token 从指定环境变量读取，不写入报告；匿名服务可不设置 Token。监控没有配置时不发起 HTTP 请求。`inspect` 校验监控配置，但不运行负载或取时间曲线。

监控在查询阶段结束、测试库清理完成后按对应时间窗回查；时间窗包含计划和预热，排除导入/建索引。HTML 展示每条 series 的时间曲线、样本数与 min/mean/max；CLI 环境页展示来源和统计。失败、空结果、NaN 或超限均保留状态，不补成零。

采集最多 8 条查询、每条最多 16 个 series，每个 series 最多 1000 个时间点，总计最多 64000 点；单次响应最多 4 MiB，整个采集最多 30 秒。长运行自动增大 step。

## 输出

新运行输出 `report.json`、`report.html`、`environment.json`。环境也完整嵌入 `report.json.environment`；HTML/CLI 查看时不会再访问 SQL 或监控。旧报告继续兼容，缺少环境字段时展示未采集；不会对历史记录补造信息。
