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
CONN=(--host 10.0.0.10 --port 6001 --user BENCH_USER)
read -r -s -p 'MO 密码：' MO_BENCH_PASSWORD
export MO_BENCH_PASSWORD

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

| 默认设置 | 值 |
| --- | --- |
| 普通查询 | 所选 pack 的全部查询，各测量一次 |
| 客户端并发 | 1、4、8 |
| 预热 | 每个场景/并发档位 5 次，不计入测量 |
| 包内稳定性场景 | 最多 5 条查询，各重复 30 次，并发 1 |
| 单查询超时 | 3 分钟；导入/建索引为该预算的 10 倍 |

时间紧时追加 `--query-limit 20 --stability-repeat 10`。这减少查询次数，仍导入全量数据和建立索引。

### 看报告

终端使用 `ui --reports reports`：`d` 选择数据集/运行记录，`1`～`6` 切换页面，`p` 切换 P90/P95/P99，`q` 退出。

浏览器直接打开每个运行目录中的 `report.html`。在远程机器执行时，通过 SSH/SFTP 把 `reports` 拷回电脑再打开。HTML 自包含，可离线查看；`report.json` 保存原始测量、SQL、输入校验和和执行计划。

## 数据包

| Pack | 数据 | 主要用途 |
| --- | --- | --- |
| GIST filtered v7 | 100 万条、960 维公开向量 | IVF 召回、PRE/POST 过滤、1/4/8 并发、重复稳定性 |
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

默认版本为 v0.8.3；可用 `make release VERSION=v0.8.3` 显式指定。构建和测试均使用本仓库的 `go.mod`、`go.sum`，关闭父目录 Go workspace。

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

客户新问题通常通过新增场景 JSON、固定查询 JSONL、真值/断言及 manifest 校验和加入数据包，再用同一运行器执行。新增指标或检查类型时扩展运行器和对应测试。

详细命令、pack schema、测量口径和离线准备流程见 [运行器参考](cmd/mo-search-lab/README.md)。迁移来源与验证见 [独立仓库设计](docs/design/standalone_repository.md)，当前名称与兼容说明见 [MO Search Lab](docs/design/search_lab_name.md)。

## License

继承原工具的 [Apache License 2.0](LICENSE) 和 Matrix Origin 版权声明。公开数据集的许可与来源随各自数据包记录。
