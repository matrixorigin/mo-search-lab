# MO Search Lab

MO 全文与向量能力的性能评测、问题复现和调查工具。当前支持现场检索健康检查：连接已有 MatrixOne 服务，导入公开数据包，测量检索质量、延迟、吞吐和重复结果稳定性，生成原始 JSON、HTML 图表报告和交互式终端报告。

这是独立 Git 仓库和独立 Go module。运行器通过 MySQL 协议连接 MO；源码和构建依赖均不引用 MatrixOne 内核。

## 现场 Quick Start

现场只需 Linux x86_64 二进制、数据包和可访问的 MO SQL 服务。账号需要建库、建表、建索引、导入、查询及删库权限。数据包已提前准备，现场无需 Go、Python、ES、模型或互联网。

解压交付包后：

```bash
sha256sum -c SHA256SUMS
./mo-search-lab version

# Bash；替换地址、端口及账号。
CONN=(--host 10.0.0.10 --port 6001 --user BENCH_USER --password '你的密码')

./mo-search-lab run "${CONN[@]}" \
  --pack packs/gist_1m_filtered_v7 --report-dir reports/gist-001
./mo-search-lab run "${CONN[@]}" \
  --pack packs/t2ranking_anli_full_500q_ngram_v3 --report-dir reports/t2-001
./mo-search-lab run "${CONN[@]}" \
  --pack packs/gist_t2_sql_workload_v7 --mixed-scenarios vector,fulltext \
  --report-dir reports/mixed-001

./mo-search-lab ui --reports reports
```

完整现场包包含上面三个 pack。`make release` 生成的工具包只包含二进制和 `packs/smoke` 小样本，用于验证流程；大数据包另外交付。完整现场包解压后约 15.73 GB，共享 CSV 只存一份。MO 存储另需容纳导入数据和索引。

每个 `run` 自动完成校验、建测试库、导入、建索引、预热、测量、生成报告及删测试库。使用 `--keep-db` 可保留测试库。报告目录每次用新路径。

打开生成的 `report.html`，通过顶部 **性能测试 / 运行环境** Tab 查看图表和同次运行的环境配置；环境页保留诊断详情与原始记录下载入口。HTML 可离线打开，无外部依赖。

| 默认设置 | 值 |
| --- | --- |
| 普通查询 | 所选 pack 的全部查询，各测量一次 |
| 客户端并发 | 默认 1；CLI 按数据集勾选额外的 4、8，并发过滤检查保持串行 |
| 预热 | 每个场景/并发档位 5 次，不计入测量 |
| 包内稳定性场景 | 最多 5 条查询，各重复 30 次，并发 1 |
| 单查询超时 | 3 分钟；导入/建索引为该预算的 10 倍 |

需要多并发时，`run` 显式追加 `--concurrency-levels 1,4,8`。

时间紧时追加 `--query-limit 20`。这减少查询次数，仍导入全量数据和建立索引。

运行只记录性能、召回/相关性和重复结果变化，不设通过阈值。数据准备或 SQL 执行错误仍显示运行失败；原始记录的 `measurement_mode: observe` 表明该模式，差异写入 `observations`。旧数据包的 SQL、查询、真值和校验和仍沿用，包内验收阈值和计划断言不执行。

### 看报告

终端使用 `ui --reports reports`：`l` 启动新任务，`d` 选择历史运行，`←/→` 切换该次运行内的测试报告，`Tab` / `Shift-Tab` 切换报告页，`1`～`6` 直达页面，在概览/并发页按 `p` 切换 P90/P95/P99，`q` 退出。

清理旧报告：按 `d` 打开历史列表，`↑↓` 选中一次运行，按 `x` 查看整次删除范围，再按 `y` 确认；`Enter`、`n` 或 `Esc` 取消。删除入口仅在历史列表中显示并生效。确认后永久删除该次运行的全部报告目录、JSON、HTML、附带记录及 `batch.json`，列表自动刷新；其他运行和数据包保留。删除前检查整组报告，含数据文件、未确认条目或已变化文件的目录会拒绝删除。

也可直接从交互式界面开始，无需先运行 `run` 或 `inspect`：

```bash
./mo-search-lab ui --launch --reports reports --packs packs --password '你的密码'
```

填写 SQL 地址、端口、账号及密码，选择“性能测试”或“环境检查”。`Enter` 编辑字段，测试项目按 `Enter` 或 `Ctrl-P` 进入勾选列表，选中“开始运行”后按 `Enter` 启动。`run`、`inspect` 和 `ui` 均使用 `--password` 直接传入 SQL 密码；UI 遮蔽显示并允许修改，留空表示空密码，密码不写入报告。默认跑全部查询，各项目只测 1 并发；在测试列表中按空格勾选各数据集下面的 4、8 并发选项。更多设置可减少查询数或提供 TOML/监控配置。两路同时查询项目自动使用 `vector,fulltext`，其它项目保持各自的默认设置；单选数据包时可手动填写两路场景。

所选项目依次执行，运行页显示当前第几项、阶段和耗时。`Esc` / `Ctrl-C` 取消后等待当前项目清理和保存，后续项目停止，已生成的报告保留。多项完成后进入本次结果列表，`Enter` 查看所选报告，`t` 返回列表；单项完成后直接打开报告。每项生成独立目录，汇总目录的 `batch.json` 保存执行状态，历史报告保持可查看。空报告目录会自动打开新任务表单；`--plain` 保持只读输出模式。

终端界面按分组对齐字段，`›` 标出当前选择，底部显示当前操作提示并固定“开始运行”按钮。报告图表的数值列对齐，环境信息按标签和值分列；小窗口优先保留焦点与快捷键。沿用终端字体和背景，设置 `NO_COLOR=1` 可关闭颜色。

首次打开表单默认勾选本地已安装的全部常用项目：向量性能与召回、向量过滤检查、全文检索评测、两路同时查询。列表内 `↑↓` 移动，`空格` 勾选，`s` 全选或清空当前列表，`Enter` 确认，`Esc` 恢复进入前的选择。当前项目显示数据来源、规模和测量范围；`a` 展开小样本、分词对比、参数实验和历史版本，这些需主动勾选。在测试项目字段按 `p` 可指定单个路径；`ui --pack DIR` 也只预选该包。完整 datasets-v1 交付包含其中的过滤、全文与两路测试，未安装的项目不会显示。

浏览器直接打开每个运行目录中的 `report.html`。在远程机器执行时，通过 SSH/SFTP 把 `reports` 拷回电脑再打开。HTML 自包含，可离线查看；`report.json` 保存原始测量、SQL、输入校验和和执行计划。

### MO 环境与配置

`run` 自动采集 MO 版本、构建信息、选定 SQL 默认变量、账号可见 CN 及节点上报缓存/内存配置（含默认值）。也可先进行只读检查：

```bash
./mo-search-lab inspect "${CONN[@]}" \
  --mo-config /path/to/cn.toml --report-dir reports/environment-001
```

`--mo-config` 可重复提供多个节点 TOML，展示内存/磁盘缓存等显式配置。`--environment-file` 补充部署与资源声明，`--monitoring-config` 接入有明确目标的 Grafana/Prometheus 曲线；三者都可用于 `run`。报告区分 SQL、文件配置、声明、监控与客户端来源，未采集的信息显示未知。格式和示例见 [环境与配置说明](docs/environment.md)。

环境报告按版本与部署、节点与缓存、检索参数、资源监控分区。配置差异、异常节点和采集失败集中在“诊断详情”；完整文件身份与校验和保留在原始 JSON。CLI 环境页按 `e`，纯文本追加 `--environment-details`。节点配置标为启动上报快照，不采集 Iceberg、ETL 或 TMP 配置。

## 数据包

公开数据包：[datasets-v1 Release](https://github.com/matrixorigin/mo-search-lab/releases/tag/datasets-v1)。下载、分卷合并及校验步骤见 [数据包分发说明](docs/datasets.md)。

| Pack | 数据 | 主要用途 |
| --- | --- | --- |
| GIST filtered v7 | 100 万条、960 维公开向量 | IVF 召回、串行 PRE/POST 过滤、重复稳定性 |
| T2Ranking anli ngram v3 | 230 万段落、500 条冻结查询与相关性标注 | anli 查询形态的全文 TF-IDF/BM25 质量、并发与稳定性 |
| GIST/T2 SQL workload v7 | 共享前两份 CSV | 两路数据库 SQL 同时执行时的延迟和吞吐 |

数据包和运行结果存放在源码仓库之外。运行时通过 `--pack` 和 `--report-dir` 指定位置。共享交付应保留 CSV 硬链接。

质量分数作为观测值，不设统一验收线。稳定性与召回/相关性分别检查。并发数表示客户端并发请求，CN 数量由客户部署决定。

## 开发与构建

构建使用 `go.mod` 指定的 Go 版本；离线准备脚本的测试使用 Python 3.10+。现场交付不需要这些开发环境。

```bash
make build        # 生成 ./mo-search-lab，纯 Go 静态构建
make check        # Go 测试、vet、格式检查及 Python 脚本测试
make release      # 生成 dist/ 下的 Linux amd64 工具包及 SHA-256
```

默认版本为 v0.13.1；可用 `make release VERSION=v0.13.1` 显式指定。构建和测试均使用本仓库的 `go.mod`、`go.sum`，关闭父目录 Go workspace。

验证小样本数据包：

```bash
./mo-search-lab validate --pack cmd/mo-search-lab/testdata/smoke
```

小样本仅验证执行流程，不用于客户性能结论。

## 目录与扩展

```text
cmd/mo-search-lab/   Go 运行器、报告、终端界面、单元测试、小样本
tools/                   离线数据准备、报告发布、独立复算及其测试
docs/design/             pack/报告/稳定性设计与仓库迁移记录
scripts/                 发布打包
.github/workflows/       独立构建与测试 CI
go.mod / go.sum          工具自己的依赖
```

客户新问题通常通过新增场景 JSON、固定查询 JSONL、真值及 manifest 校验和加入数据包，再用同一运行器执行。新增指标或检查类型时扩展运行器和对应测试。

详细命令、pack schema、测量口径和离线准备流程见 [运行器参考](cmd/mo-search-lab/README.md)。迁移来源与验证见 [独立仓库设计](docs/design/standalone_repository.md)，当前名称与兼容说明见 [MO Search Lab](docs/design/search_lab_name.md)。

## License

继承原工具的 [Apache License 2.0](LICENSE) 和 Matrix Origin 版权声明。公开数据集的许可与来源随各自数据包记录。

历史列表依据 `batch.json` 把同一次运行分组，显示运行时间、报告数量和总耗时；独立报告作为一次运行显示。`ui --report-dir DIR` 支持整次运行目录，也可从其某份报告打开整组。左右切报告时保留当前页面；切换历史运行时优先保留同一数据集。
