# 现场部署与测试手册

适用版本：MO Search Lab v0.14.0。现场运行需要一个可访问的 MO SQL 服务。

## 1. 当前交付状态

- v0.14.0 源码已推送，性能测量实现提交为 `0290fd7`。
- GitHub 当前只有 `datasets-v1` 数据 Release，尚未发布 v0.14.0 二进制 Release。工具包由准备人员构建后交付，操作人员无需安装 Go。
- `datasets-v1` 包含向量过滤、全文检索、两路同时查询三项。**向量性能与召回需要另外交付 GIST health Top-100 包**。
- 现场不需要安装 ES、模型或 Python。Python 仅用于准备机的数据处理；浏览器报告可以离线打开。

建议交付时统一装成下列目录。现场人员使用该目录里的二进制，不使用源码工作目录或 `prev_vec.py`：

```text
mo-search-lab-field/
  mo-search-lab
  packs/
    smoke/                         # 8 行流程检查
    gist_1m_health_top100_v1/       # 向量性能、召回和稳定性
    ...                           # 可选的过滤、全文和两路测试包
  docs/
  README.md
  LICENSE
  SHA256SUMS                      # 工具包文件校验清单
```

## 2. 准备人员：下载、解压并制作交付目录

下载在能访问 GitHub 或准备机 SSH 的机器上完成。离线现场直接使用本节最终生成的完整交付包。

以下命令在 Linux 准备机使用 Bash 执行，可以先从个人电脑 SSH 登录；在同一终端执行。先设置本地目录，后续命令使用完整路径，不依赖当前工作目录：

```bash
SEARCH_LAB_FIELD="$HOME/mo-search-lab-field"
SEARCH_LAB_DOWNLOADS="$HOME/mo-search-lab-downloads"
mkdir -p "$SEARCH_LAB_FIELD" "$SEARCH_LAB_DOWNLOADS/tool" \
  "$SEARCH_LAB_DOWNLOADS/datasets-v1" "$SEARCH_LAB_FIELD/packs"
```

### 2.1 获取并解压工具

**方式 A：从现有构建机下载，无需 Go。** 当前 v0.14.0 工具压缩包已在准备机生成，尚无 GitHub 二进制下载地址。以下以现有 SSH 入口为例；如果数据位于其他准备机，替换用户名、主机和端口。

在接收文件的 Linux 准备机执行：

```bash
scp -P 30043 \
  mo@shanghai.idc.matrixorigin.cn:/home/mo/mo-search-lab/dist/mo-search-lab-v0.14.0-linux-amd64.tar.gz \
  mo@shanghai.idc.matrixorigin.cn:/home/mo/mo-search-lab/dist/mo-search-lab-v0.14.0-linux-amd64.tar.gz.sha256 \
  "$SEARCH_LAB_DOWNLOADS/tool/"

cd "$SEARCH_LAB_DOWNLOADS/tool"
sha256sum -c mo-search-lab-v0.14.0-linux-amd64.tar.gz.sha256
tar -xzf mo-search-lab-v0.14.0-linux-amd64.tar.gz \
  -C "$SEARCH_LAB_FIELD" --strip-components=1
cd "$SEARCH_LAB_FIELD"
sha256sum -c SHA256SUMS
./mo-search-lab version
```

应显示 `v0.14.0`。需要自定义 SSH 私钥时，在 `scp` 命令中追加 `-i /path/to/key`。

<details>
<summary>方式 B：准备机从源码构建工具包</summary>

准备机使用 Linux、Git、Make、Go 1.26.4 或能自动下载该工具链的 Go 环境。下列构建目录应为新目录：

```bash
git clone https://github.com/matrixorigin/mo-search-lab.git "$HOME/mo-search-lab-build"
cd "$HOME/mo-search-lab-build"
git checkout 0290fd7
make release VERSION=v0.14.0 TARGET_OS=linux TARGET_ARCH=amd64

cp dist/mo-search-lab-v0.14.0-linux-amd64.tar.gz \
   dist/mo-search-lab-v0.14.0-linux-amd64.tar.gz.sha256 \
   "$SEARCH_LAB_DOWNLOADS/tool/"
cd "$SEARCH_LAB_DOWNLOADS/tool"
sha256sum -c mo-search-lab-v0.14.0-linux-amd64.tar.gz.sha256
tar -xzf mo-search-lab-v0.14.0-linux-amd64.tar.gz \
  -C "$SEARCH_LAB_FIELD" --strip-components=1
cd "$SEARCH_LAB_FIELD"
./mo-search-lab version
```

ARM64 运行机将 `TARGET_ARCH=amd64` 改为 `TARGET_ARCH=arm64`，并相应使用文件名中的 `linux-arm64`。

</details>

工具包约 4.7 MiB，只附带 8 行流程样本。接下来安装实际数据包。

### 2.2 获取 GIST 向量性能与召回包

**该包目前未发布到 GitHub，需要从现有准备机获取。** 整包已冻结，包含 manifest、查询、真值、来源记录和约 12.04 GB 的 CSV。

从另一台 Linux 准备机或运行机，通过 SSH 下载整个目录，直接安装到 `packs/`，此方式不需要解压：

```bash
scp -P 30043 -r \
  mo@shanghai.idc.matrixorigin.cn:/d/mo-worktrees/retrieval-bench-data/packs/gist_1m_health_top100_v1 \
  "$SEARCH_LAB_FIELD/packs/"
```

如果就在当前准备机操作，使用本地复制即可：

```bash
cp -a /d/mo-worktrees/retrieval-bench-data/packs/gist_1m_health_top100_v1 \
  "$SEARCH_LAB_FIELD/packs/"
```

以上两种方式选一种。只复制场景 JSON 无法运行，必须保留整个目录。运行器在测试前自动校验包内输入 SHA-256。

<details>
<summary>可选：先压缩 GIST，再下载、校验和解压</summary>

先通过 SSH 登录现有准备机，在**准备机**执行以下命令。压缩会读取约 12 GB 数据，准备时间单独计算：

```bash
mkdir -p /d/mo-worktrees/retrieval-bench-data/exports
cd /d/mo-worktrees/retrieval-bench-data/packs
tar -czf /d/mo-worktrees/retrieval-bench-data/exports/gist_1m_health_top100_v1.tar.gz \
  gist_1m_health_top100_v1
cd /d/mo-worktrees/retrieval-bench-data/exports
sha256sum gist_1m_health_top100_v1.tar.gz \
  > gist_1m_health_top100_v1.tar.gz.sha256
```

回到**接收文件的 Linux 准备机**，在已设置 `SEARCH_LAB_FIELD` 和 `SEARCH_LAB_DOWNLOADS` 的终端执行：

```bash
mkdir -p "$SEARCH_LAB_DOWNLOADS/gist"
scp -P 30043 \
  mo@shanghai.idc.matrixorigin.cn:/d/mo-worktrees/retrieval-bench-data/exports/gist_1m_health_top100_v1.tar.gz \
  mo@shanghai.idc.matrixorigin.cn:/d/mo-worktrees/retrieval-bench-data/exports/gist_1m_health_top100_v1.tar.gz.sha256 \
  "$SEARCH_LAB_DOWNLOADS/gist/"
cd "$SEARCH_LAB_DOWNLOADS/gist"
sha256sum -c gist_1m_health_top100_v1.tar.gz.sha256
tar -xzf gist_1m_health_top100_v1.tar.gz -C "$SEARCH_LAB_FIELD/packs"
```

</details>

只需要 GIST 性能与召回时，可直接跳到第 2.4 节归档交付。

### 2.3 可选：下载另外三个测试项目

[GitHub datasets-v1 Release](https://github.com/matrixorigin/mo-search-lab/releases/tag/datasets-v1) 包含向量过滤、全文检索和两路同时查询三个项目。以下下载命令在有互联网的准备机执行。

**方式 A：使用 curl，直接下载所有分卷和校验文件，无需 GitHub CLI。**

```bash
(
set -e
cd "$SEARCH_LAB_DOWNLOADS/datasets-v1"
SEARCH_LAB_DATASET_URL="https://github.com/matrixorigin/mo-search-lab/releases/download/datasets-v1"
for asset in \
  mo-search-lab-datasets-v1.tar.gz.part01 \
  mo-search-lab-datasets-v1.tar.gz.part02 \
  mo-search-lab-datasets-v1.tar.gz.part03 \
  SHA256SUMS archive.sha256 README.md DATASETS.json LICENSE_NOTICES.md verification.json
do
  if [ -f "$asset" ]; then continue; fi
  curl --fail --location --retry 3 --continue-at - \
    --output "$asset.partial" "$SEARCH_LAB_DATASET_URL/$asset"
  mv "$asset.partial" "$asset"
done
)
```

中断后重新执行整个下载段即可：已完成的文件会跳过，未完成的文件通过 `.partial` 断点续传。所有文件下载完成后再进入校验步骤。

<details>
<summary>方式 B：已安装 gh 时，一条命令下载全部资产</summary>

在新下载目录中执行，替代上面的 curl 下载循环：

```bash
gh release download datasets-v1 --repo matrixorigin/mo-search-lab \
  --dir "$SEARCH_LAB_DOWNLOADS/datasets-v1"
```

</details>

**下载完成后，两种方式都执行下面的校验、分卷合并、解压和安装命令。下载或校验出错时，当前命令段会停止：**

```bash
(
set -e
cd "$SEARCH_LAB_DOWNLOADS/datasets-v1"

# 1. 校验下载的三个分卷和元数据。
sha256sum -c SHA256SUMS

# 2. 按固定顺序合并分卷。
cat mo-search-lab-datasets-v1.tar.gz.part01 \
    mo-search-lab-datasets-v1.tar.gz.part02 \
    mo-search-lab-datasets-v1.tar.gz.part03 \
    > mo-search-lab-datasets-v1.tar.gz

# 3. 校验合并后的压缩包；成功后再解压。
sha256sum -c archive.sha256
tar -xzf mo-search-lab-datasets-v1.tar.gz

# 4. 校验解压后的数据，并把整个 packs 树安装到交付目录。
cd mo-search-lab-datasets-v1
sha256sum -c SHA256SUMS
cp -a packs/. "$SEARCH_LAB_FIELD/packs/"
)
```

校验有 `FAILED` 时，先移走下载目录里对应的损坏文件，再重新执行下载段；文件缺失时直接重新执行下载段。全部校验成功后再继续。一次复制整个 `packs/` 树，保留过滤与两路项目共用 CSV 的硬链接。

当前数据量参考（十进制 GB，不含 MO 导入后的存储）：

| 内容 | 大小 |
| --- | --- |
| datasets-v1 三个下载分卷合计 | 约 3.99 GB |
| datasets-v1 三个包解压后的唯一文件内容 | 约 15.73 GB |
| 额外的 GIST health 包 CSV | 约 12.04 GB |
| 四项完整数据的唯一 CSV 内容 | 约 27.76 GB |

完整四项包含两种不同结构的 GIST CSV。准备目录、压缩归档和 MO 的数据/索引都需要另外计算空间，不能只按分卷大小准备磁盘。

### 2.4 检查安装结果、归档并交付

检查已安装的数据包。这里只列出目录，不重复扫描大文件；真正启动测试时运行器会校验完整输入。

```bash
find "$SEARCH_LAB_FIELD/packs" -mindepth 2 -maxdepth 2 -name manifest.json -print
cd "$SEARCH_LAB_FIELD"
./mo-search-lab version
```

只安装工具和 GIST 时应看到 `smoke` 与 `gist_1m_health_top100_v1`。安装全部数据后会看到五个包目录：流程检查和四个实际测试项目。

打包并生成完整交付包的校验文件。下面以第 2 节默认的 `$HOME/mo-search-lab-field` 目录为例：

```bash
tar -C "$HOME" -czf "$SEARCH_LAB_DOWNLOADS/mo-search-lab-field-v0.14.0.tar.gz" \
  mo-search-lab-field
cd "$SEARCH_LAB_DOWNLOADS"
sha256sum mo-search-lab-field-v0.14.0.tar.gz \
  > mo-search-lab-field-v0.14.0.tar.gz.sha256
```

将这两个文件通过离线介质或 SSH/SFTP 交付到客户现场。如果接收机可以 SSH 连接这台准备机，可以从接收机下载；替换实际 SSH 地址、账号及准备机下载目录：

```bash
mkdir -p "$HOME/mo-search-lab-install"
scp -P 30043 \
  mo@shanghai.idc.matrixorigin.cn:/home/mo/mo-search-lab-downloads/mo-search-lab-field-v0.14.0.tar.gz \
  mo@shanghai.idc.matrixorigin.cn:/home/mo/mo-search-lab-downloads/mo-search-lab-field-v0.14.0.tar.gz.sha256 \
  "$HOME/mo-search-lab-install/"
```

完整交付包只有执行本节打包后才存在，不是现有 GitHub Release 资产。压缩包内的 `SHA256SUMS` 用于校验工具文件；各数据包自己的 manifest 保存输入哈希。整个现场归档使用外部 `.sha256` 校验。

## 3. 现场：安装

运行主机需要：

- 与工具包匹配的 Linux CPU 架构，普通用户可执行本地文件。
- 能访问 MO 的 SQL 地址与端口。
- 性能测试账号能创建数据库、表、索引，执行 `LOAD DATA LOCAL INFILE`、查询和删除测试库。
- 客户端磁盘可容纳所选数据和报告；MO 服务端另有导入数据和索引空间。

把收到的压缩包及 `.sha256` 放在同一目录，例如 `$HOME/mo-search-lab-install`。在接收机执行：

```bash
cd "$HOME/mo-search-lab-install"
sha256sum -c mo-search-lab-field-v0.14.0.tar.gz.sha256
tar -xzf mo-search-lab-field-v0.14.0.tar.gz
cd mo-search-lab-field
./mo-search-lab version
```

应显示 `v0.14.0`。现场不需要克隆源码、安装 Go 或重新准备数据。

## 4. 现场：先验证连接与流程

以下示例使用 Bash。修改 SQL 地址、端口、账号和密码；密码直接作为参数传入，不依赖环境变量。

```bash
CONN=(--host 10.0.0.10 --port 6001 --user BENCH_USER --password '你的密码')
```

### 4.1 只读环境检查

```bash
./mo-search-lab inspect "${CONN[@]}" \
  --report-dir reports/environment-001
```

这一步不建库、不导入数据。采集版本、账号可见节点和可获取的配置。部分环境信息显示未知，不等于测试失败；查看报告中的采集原因。

有本地 MO TOML 时可追加 `--mo-config /path/to/cn.toml`。Grafana/Prometheus、部署声明和 TOML 都是可选项，格式见 [环境说明](environment.md)。正式性能运行也会自动采集环境信息。

### 4.2 8 行流程检查

```bash
./mo-search-lab run "${CONN[@]}" \
  --pack packs/smoke --measure-duration 0 \
  --report-dir reports/smoke-001
```

检查建库、导入、索引、查询、清理和报告是否能走通。使用按次数模式，避免对流程样本持续压测。它不能代表百万数据的性能或验证多 CN 问题。

每次使用新的报告目录。重复执行上述示例时，把 `001` 改为新的编号，避免覆盖既有证据。

## 5. 现场：运行实际测试

推荐使用交互界面：

```bash
./mo-search-lab ui --launch "${CONN[@]}" \
  --packs packs --reports reports
```

1. 在“测试项目”字段按 `Enter` 打开列表，`空格` 勾选需要的项目。首次默认选择已安装的常用测试。
2. 首次只选“向量性能与召回”，确认该环境可以完成百万级测试后，再选择其他项目。
3. 默认 1 并发；需要时勾选项目下的 4、8 并发。过滤和稳定性检查保持串行。
4. 在“更多设置”调整测量时长和轮数。常规检查用默认值；正式性能记录可设 3 轮。
5. 选择“开始运行”。界面显示当前项目、阶段、轮次和累计耗时。
6. 完成后直接看报告，多个项目先显示本次结果列表。运行中 `Esc` / `Ctrl-C` 取消后等待清理及报告保存。

### 默认值

| 项目 | 默认行为 |
| --- | --- |
| 普通性能 | 1 并发，每轮预热 10 秒，目标测量 30 秒，1 轮 |
| 多轮 | 每轮独立预热，轮间休息 3 秒；导入、建索引只做一次 |
| 查询池 | 使用包内全部查询，测量窗口内循环执行 |
| 稳定性 | 最多 5 条查询，每条 30 次，独立串行 |
| 过滤 | 按次数串行检查，每条默认 1 次、预热 5 次 |
| 超时 | 单 SQL / 执行计划 3 分钟，导入和建索引 30 分钟 |
| 清理 | 运行结束自动删除本次创建的测试库，保留报告 |

预热、目标测量时间均需等待在途请求收尾。40 秒是单个普通场景一轮的名义预算，不是整个数据包的耗时。多个 probe、并发档位、轮数以及独立稳定性检查会累加；导入、建索引和输入校验另计。

### 脚本方式：单独运行 GIST

```bash
# 默认 1 并发、1 轮。
./mo-search-lab run "${CONN[@]}" \
  --pack packs/gist_1m_health_top100_v1 \
  --report-dir reports/gist-001

# 正式评测：3 轮，追加 4、8 并发。
./mo-search-lab run "${CONN[@]}" \
  --pack packs/gist_1m_health_top100_v1 \
  --rounds 3 --concurrency-levels 1,4,8 \
  --report-dir reports/gist-002
```

时间紧时可设 `--measure-duration 10s --warmup-duration 2s`。`--query-limit` 在持续模式只缩小查询池，不缩短测量窗口，也不减少导入量。独立稳定性预算用 `--stability-query-limit`、`--stability-repeat` 调整。

两路项目在 UI 自动配置两个 SQL 场景。通过 `run` 运行该包时追加 `--mixed-scenarios vector,fulltext`。

## 6. 看报告

### 终端

```bash
./mo-search-lab ui --reports reports
```

- `d`：选择历史运行；`←/→`：切换同一次运行内的报告。
- `Tab`：切换当前报告页面；`1` 概览、`2` 并发、`3` 质量、`4` 稳定性、`5` SQL、`6` 环境、`7` 备注。
- `p`：切换延迟分位数；`?`：指标解释。
- 删除只在 `d` 历史页进行：`x` 预览整次删除范围，`y` 确认。

### 浏览器

每次测试已自动生成 `report.html`，**无需先执行 render**。把整个报告目录通过 SSH/SFTP 拷回电脑，直接打开其中的 HTML 即可。多项目运行有多份 HTML，各报告对应一个项目。

```text
reports/某次运行/
  batch.json                    # 仅多项目运行
  01-某项测试/
    report.json                 # 原始测量与执行证据
    report.html                 # 离线图表
    environment.json            # 有环境证据时附带
  02-另一项测试/
    ...
```

单项目 `run` 的 JSON 和 HTML 直接在指定的 `--report-dir` 下。`render --report-dir 单份报告目录` 仅用于从已有 JSON 更新 HTML，不重跑数据库查询。

### 可选：通过 SSH 转发 HTTP 页面

运行机或跳板机已有 Python 3 时，可以临时启动只监听本机的报告服务；这不是工具运行依赖：

```bash
python3 -m http.server 8766 --bind 127.0.0.1 --directory reports
```

在自己的电脑执行 SSH 转发。以此前使用的 SSH 入口为例：

```bash
ssh -p 30043 -N \
  -L 127.0.0.1:18766:127.0.0.1:8766 \
  mo@shanghai.idc.matrixorigin.cn
```

浏览器打开 `http://127.0.0.1:18766/`，进入对应的运行和报告目录。仅开放 SSH 的机器也适用。SSH 登录有自定义私钥时，在命令中添加 `-i /path/to/key`。

## 7. 怎样理解结果

- 执行完成表示数据准备、SQL 和清理没有错误；召回不足、相关性低或集合/顺序变化作为观测值呈现。
- QPS 使用实际测量耗时，包括最后一批请求收尾。延迟包含客户端、网络和数据库执行及结果读取。
- 质量计算在计时结束后执行。持续模式每轮每条查询首次请求保留结果 ID，报告展示质量样本量和实际查询覆盖率。
- 多轮整体分位数由全部成功延迟样本合并计算；轮间 QPS 标准差与变异系数描述波动。
- 样本上限导致提前结束时，报告明确标记。少于 1000 个成功样本时会提示 P99 尾部证据有限。
- 客户端并发数不等于 CN 数量。GIST 召回衡量索引找回精确近邻的能力，不能代替业务语义相关性评价。

具体口径见 [性能测量说明](performance-measurement.md)。

## 8. 常见情况

| 现象 | 操作 |
| --- | --- |
| 只看到流程检查，没看到百万数据测试 | 当前只有工具包；放入实际数据包后重新打开 UI |
| 有过滤/全文/两路，没有向量性能与召回 | datasets-v1 不含 health 包，补入整个 GIST health 目录 |
| 连接或鉴权失败 | 先运行 `inspect`，核对 SQL 地址、端口、账号与 `--password` |
| 建库、索引或导入失败 | 查看阶段错误；确认账号权限和该 MO 的功能支持 |
| 导入/建索引很慢 | 查看当前阶段与报告阶段耗时；减少查询数不能缩短数据准备 |
| 环境、硬件或监控信息不全 | 检查可选 TOML、部署声明及监控配置；缺失项保留未知和原因 |
| 报告目录已存在 | 换一个新目录，或用 CLI 历史管理确认后删除整次旧运行 |
| 清理失败 | 从报告核对本次自动创建的数据库名，处理该库；不要按模糊前缀批量删库 |
| 需要保留库调查 | 启动前选择“保留测试库”，脚本使用 `--keep-db` |

报告记录原始 SQL、查询/场景哈希、执行计划、版本、环境、耗时和错误。保留整个报告目录作为调查证据。
