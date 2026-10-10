# 公开数据包 datasets-v1

从 [GitHub Release](https://github.com/matrixorigin/mo-search-lab/releases/tag/datasets-v1) 下载以下文件到同一目录：

```text
mo-search-lab-datasets-v1.tar.gz.part01
mo-search-lab-datasets-v1.tar.gz.part02
mo-search-lab-datasets-v1.tar.gz.part03
SHA256SUMS
archive.sha256
README.md
DATASETS.json
LICENSE_NOTICES.md
verification.json
```

数据包约 4 GB，包含 GIST filtered v7、T2Ranking anli ngram v3 和 GIST/T2 paired SQL workload v7。二进制单独下载或构建。每个分卷小于 2 GiB，三个 pack 的共享 CSV 在归档中只存一份。

## 合并、解压和校验

```sh
# 校验下载的分卷与元数据
sha256sum -c SHA256SUMS

cat mo-search-lab-datasets-v1.tar.gz.part01 \
    mo-search-lab-datasets-v1.tar.gz.part02 \
    mo-search-lab-datasets-v1.tar.gz.part03 \
    > mo-search-lab-datasets-v1.tar.gz

# 校验合并后的完整压缩包
sha256sum -c archive.sha256
tar -xzf mo-search-lab-datasets-v1.tar.gz

# 校验解压后的输入文件
cd mo-search-lab-datasets-v1
sha256sum -c SHA256SUMS
```

完整解压到同一文件系统，保留共享 CSV 硬链接。解压后唯一文件内容约 15.73 GB；MO 存储另需容纳导入数据及索引。

## 使用

建议使用当前工具版本，指定所选 pack 的路径（新默认值与交互选项见 [Quick Start](../README.md)）：

```text
mo-search-lab-datasets-v1/packs/gist_1m_filtered_v7
mo-search-lab-datasets-v1/packs/t2ranking_anli_full_500q_ngram_v3
mo-search-lab-datasets-v1/packs/gist_t2_sql_workload_v7
```

两路同时查询的 pack 追加 `--mixed-scenarios vector,fulltext`。默认并发 1，需要多并发时显式追加 `--concurrency-levels 1,4,8`；包内稳定性场景默认最多 5 条查询各重复 30 次。完整连接与报告查看步骤见 [Quick Start](../README.md)。

## 交互式选择

`ui` 的测试项目字段按 `Enter` 或 `Ctrl-P` 进入列表，首次默认勾选全部已安装常用测试。`↑↓` 移动、`空格` 勾选、`s` 全选或清空当前列表、`Enter` 确认；所选测试依次运行，各自保存报告。日常检查优先显示向量性能与召回、向量过滤检查、全文检索评测、两路同时查询，焦点项目显示数据规模与测量范围；`a` 展开其它实验和历史版本，这些需主动勾选。datasets-v1 安装后显示过滤、全文、两路这三项，向量性能与召回需另外安装对应 health Top-100 包。`ui --pack DIR` 只预选指定包。

新增客户复现包可在包目录添加可选 `pack-info.json`，无需修改已冻结的 manifest 或大数据文件：

```json
{
  "name": "客户召回稳定性复现",
  "summary": "重复相同输入，检查返回 ID 与顺序。",
  "scale": "100 条固定输入",
  "featured": true
}
```

该文件用于界面展示，最大 16 KiB；新运行会将 `name` 保存为可选的 `dataset_name`，即使原数据包不在现场，CLI 和网页报告仍能显示这个名称；`featured: true` 将此包加入常用列表。执行和原始记录中的 `dataset` 仍使用 manifest 中的身份，SQL、查询、真值与校验和保持不变。没有显示文件的自定义包仍可在列表或通过路径选择；如果目录内没有常用包，则直接展示全部可用包。

## 来源与版本

- GIST1M：原始 INRIA/IRISA TexMex 向量与邻居数据，转换为 CSV，加入人工可见性字段和过滤真值；原始文件 SHA-256 在 pack 的 `source.json`。
- T2Ranking：[THUIR/T2Ranking](https://github.com/THUIR/T2Ranking)，原发布方标明 Apache-2.0。保留完整 2,303,643 段落，冻结 500 条 dev 查询与标注，按 anli 查询形态预处理，使用 ngram 与 TF-IDF/BM25 场景。
- Paired workload：复用上面两份 CSV，运行两路独立数据库 SQL。

归档内包含 `DATASETS.json`、各 pack 的 `source.json`、`manifest.json`、输入 SHA-256、来源说明及 T2Ranking 许可副本。数据包不含客户记录、模型、执行二进制或实测报告。质量以观测值报告；500 条查询样本不代表官方全测试集结果。

默认只测 1 并发。在 CLI 测试列表中，各适用数据集下有可选的 4、8 并发项，默认不勾选；过滤检查保持串行。工具记录召回和稳定性变化，不执行包内验收断言，旧包无需重新生成。
