# 统一离线资源包

[下载 v0.14.0 完整资源](https://github.com/matrixorigin/mo-search-lab/releases/tag/v0.14.0)。工具和四项常用测试数据统一交付：

| 安装目录 | 测试 |
| --- | --- |
| `packs/vector` | GIST1M 100 万条向量：性能、召回、重复结果稳定性 |
| `packs/filter` | GIST1M 串行 PRE/POST 过滤检查 |
| `packs/fulltext` | T2Ranking 230 万段落、500 条冻结查询与标注 |
| `packs/dual-query` | 向量与全文 SQL 同时运行，复用过滤、全文的 CSV |
| `packs/smoke` | 8 行流程样本 |

安装只需：

```bash
curl -fL https://github.com/matrixorigin/mo-search-lab/releases/download/v0.14.0/setup.sh -o setup.sh
bash setup.sh
```

脚本自动下载、断点续传、校验并解压全部内容。GitHub 单附件小于 2 GiB，底层分卷由脚本处理，用户无需手动合并或单独选择数据包。离线安装和测试步骤见 [Handbook](handbook.md)。

数据 CSV 唯一内容约 27.76 GB，下载缓存另计；MO 数据与索引需要额外空间。CSV 硬链接在归档和解压时保留，重复引用不重复存储。

## 来源与冻结身份

GIST 来自 INRIA/IRISA TexMex。T2Ranking 来自 [原项目](https://github.com/THUIR/T2Ranking)，原发布方标明 Apache-2.0。数据包保留 `source.json`、原始输入哈希、冻结查询和真值；资源包带有 `DATASETS.md`、许可副本与 `BUNDLE.json`。

目录名是便于现场使用的通用名称，包内 manifest、SQL、数据、查询和哈希保持原有身份。运行器在执行前验证输入，不设置召回或稳定性验收断言。

历史 [datasets-v1](https://github.com/matrixorigin/mo-search-lab/releases/tag/datasets-v1) 继续保留，供旧交付引用。新部署使用统一包，无需再安装历史分卷。

新增复现包可以在包目录添加可选 `pack-info.json`，提供 `name`、`summary`、`scale` 和 `featured`；CLI 自动发现，显示信息不修改冻结输入。
