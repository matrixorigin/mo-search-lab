# 架构与扩展

MO Search Lab 是独立 Go 程序，通过 MySQL 协议连接 MatrixOne。现场交付由可执行文件和本地数据包组成。

## 执行流程

```text
CLI / 交互表单
  → 读取数据包、校验输入、规划测试
  → 连接 MO、采集环境
  → 建测试库、导入数据、建索引
  → 获取执行计划、预热、测量
  → 计算质量与稳定性、清理测试库
  → 保存 JSON、生成 HTML、展示终端报告
```

向量性能、过滤与两路查询共用同一份 GIST CSV，导入时按各自表结构选择列。

同一数据包的场景、并发档位和轮次复用一次导入与建索引。普通性能按时间测量，过滤与稳定性串行按次数执行。具体计时和统计口径见 [性能测量](performance-measurement.md)。

多项目任务依次运行，每项有独立报告；`batch.json` 记录一次运行所含项目。报告浏览按运行、测试报告、报告页三层组织。HTML 和 CLI 读取同一份原始记录，支持离线查看。

## 代码入口

代码位于 `cmd/mo-search-lab/`，按责任拆分文件：

| 文件 | 责任 |
| --- | --- |
| `main.go`、`help.go` | 命令、参数与入口 |
| `pack.go` | 数据包结构、路径和哈希校验 |
| `profiles.go`、`run.go` | 测试规划、数据准备与执行 |
| `stability.go` | 固定查询的重复结果检查 |
| `environment*.go` | SQL、TOML、资源声明与监控采集 |
| `report*.go` | 原始结果整理与 HTML 图表 |
| `terminal*.go` | 任务表单、运行进度、报告与历史管理 |

## 数据包格式

```text
my-case/
  manifest.json
  source.json
  documents.csv
  search.json
  search.jsonl
  pack-info.json
```

`manifest.json` 定义 `schema_version`、`dataset`、建表 SQL（`ddl`）、导入项（`loads`）、建索引 SQL（`indexes`）和场景引用（`scenarios`）。导入项包含表名、预期行数及 CSV 的路径和 SHA-256；schema 4 可提供 `columns` 将 CSV 字段映射到表列，空字符串表示跳过该字段，例如 `["id", "", "embedding"]`。场景引用也包含路径和 SHA-256。引用路径限定在包内。

场景 JSON 定义：

| 字段 | 用途 |
| --- | --- |
| `id`、`route` | 场景身份；普通 SQL 使用 `route: "sql"` |
| `sql`、`args` | 参数化查询及参数名称顺序 |
| `queries` | 查询 JSONL 的路径和 SHA-256 |
| `top_k`、`id_column` | 返回上限及结果 ID 列 |
| `session_sql` | 各执行连接上的会话设置 |
| `oracle` | 质量或稳定性指标类型 |

每条查询包含 `id`、`params`，并按指标提供 `exact_ids`、`relevance` 或 `allowed_id_ranges`。

| `oracle` | 记录内容 |
| --- | --- |
| `ann_recall` | 相对精确 Top-K 真值的召回率 |
| `qrels` | 人工相关性标注下的 nDCG、Recall、MRR |
| `exact_ids` | 与固定答案的差异 |
| `nonempty` | 是否返回结果 |
| `stable_multiset` | 重复结果的 ID 多重集合；`check_order` 同时记录顺序变化 |

稳定性场景可设置 `min_repetitions`、`expected_rows` 和 `allow_empty`。所有重复结果均保留，比较基准为同一查询首次成功结果。质量与结果变化写入观测；数据准备、SQL 或清理错误影响执行状态。

`source.json` 保存公开来源、版本、许可、预处理、ID 映射及真值生成方式。可选 `pack-info.json` 提供 `name`、`summary`、`scale`、`featured`，供 CLI 展示与选择。

完整小样本见 [smoke 数据包](https://github.com/matrixorigin/mo-search-lab/blob/main/cmd/mo-search-lab/testdata/smoke/manifest.json)，结构定义见 [pack.go](https://github.com/matrixorigin/mo-search-lab/blob/main/cmd/mo-search-lab/pack.go)。

## 加入客户问题

1. 固定复现数据、SQL、会话参数、查询输入和对照答案，写明来源与预期观察。
2. 添加场景和查询 JSONL，更新 manifest 的 SHA-256；已有冻结数据包通过新目录发布新版本。
3. 执行 `./mo-search-lab validate --pack /path/to/my-case`。
4. 放入 `packs/`，在交互 CLI 的测试列表中选择运行；也可直接执行 `run --pack /path/to/my-case`。

多 CN 稳定性调查可提供 `--query-endpoints cn1:6001,cn2:6001`，轮换端点重复同一查询，保存端点与物理计划。判断实际 CN 参与情况时结合部署信息和执行计划。

新增 SQL 或输入通常只需扩展数据包。新增指标类型时，修改包校验、指标计算和报告展示，并用固定小样本验证。

## 准备与发布

`tools/` 中的数据准备脚本在发布机执行：`prepare_public_pack.py` 准备公开输入，`prepare_anli_pack.py` 生成应用分词的全文场景，`prepare_filtered_gist.py` 准备过滤真值和两路查询包。参数通过各脚本 `--help` 查看。

```bash
make check
make release-full PACKS_ROOT=/data/packs OUTPUT_DIR=/data/releases/new-release
```

`scripts/release.sh` 构建工具，`tools/package_field_bundle.py` 校验并归档四个常用包，保留共享 CSV 硬链接、来源与许可，生成分卷和 SHA-256 清单；`scripts/setup.sh` 完成现场安装。发布机使用 Go、Python 3.12+、GNU tar 和 gzip。

运行环境的输入格式与采集规则见 [环境配置](environment.md)，现场操作见 [部署手册](handbook.md)。
