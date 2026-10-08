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

用 MO Search Lab v0.8.3 或更高版本指定所选 pack 的路径：

```text
mo-search-lab-datasets-v1/packs/gist_1m_filtered_v7
mo-search-lab-datasets-v1/packs/t2ranking_anli_full_500q_ngram_v3
mo-search-lab-datasets-v1/packs/gist_t2_sql_workload_v7
```

两路同时查询的 pack 追加 `--mixed-scenarios vector,fulltext`。默认并发 1、4、8；包内稳定性场景默认最多 5 条查询各重复 30 次。完整连接与报告查看步骤见 [Quick Start](../README.md)。

## 来源与版本

- GIST1M：原始 INRIA/IRISA TexMex 向量与邻居数据，转换为 CSV，加入人工可见性字段和过滤真值；原始文件 SHA-256 在 pack 的 `source.json`。
- T2Ranking：[THUIR/T2Ranking](https://github.com/THUIR/T2Ranking)，原发布方标明 Apache-2.0。保留完整 2,303,643 段落，冻结 500 条 dev 查询与标注，按 anli 查询形态预处理，使用 ngram 与 TF-IDF/BM25 场景。
- Paired workload：复用上面两份 CSV，运行两路独立数据库 SQL。

归档内包含 `DATASETS.json`、各 pack 的 `source.json`、`manifest.json`、输入 SHA-256、来源说明及 T2Ranking 许可副本。数据包不含客户记录、模型、执行二进制或实测报告。质量以观测值报告；500 条查询样本不代表官方全测试集结果。
