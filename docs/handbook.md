# 现场部署与测试

## 1. 一次安装全部资源

在能访问 GitHub 的 **Linux x86_64** 机器上执行：

```bash
curl -fL https://github.com/matrixorigin/mo-search-lab/releases/download/v0.14.1/setup.sh -o setup.sh
bash setup.sh
```

脚本会自动下载完整资源、校验、解压。中断后重新执行 `bash setup.sh`，已完成的附件不重复下载。安装不需要 root、Go、Python、ES 或模型。

安装结果：

```text
mo-search-lab-v0.14.1-linux-amd64/
  mo-search-lab
  packs/
    vector/       # GIST1M 向量性能、召回、重复稳定性
    filter/       # GIST1M 串行过滤检查
    fulltext/     # T2Ranking 全文性能与相关性
    dual-query/   # 向量、全文两路同时查询
    smoke/        # 8 行流程检查
  docs/
  BUNDLE.json     # 版本、来源提交、测试包身份
  DATASETS.md     # 公开数据来源与许可说明
```

下载约 4 GB，完整 CSV 唯一内容约 15.72 GB；MO 服务端需要额外的数据和索引存储。共享 CSV 在资源包中只保存一份，解压时保留硬链接。

## 2. 开始测试

需要一个可访问的 MO SQL 服务，测试账号允许建库、建表、建索引、导入、查询和删除本次测试库。

```bash
cd mo-search-lab-v0.14.1-linux-amd64
./mo-search-lab ui --launch --reports reports \
  --host 10.0.0.10 --port 6001 --user BENCH_USER --password '你的密码'
```

替换 SQL 地址、端口、账号与密码。在界面中：

1. 首次默认勾选全部四项常用测试，可在测试列表按空格调整。
2. 默认 1 并发；按需勾选项目下的 4、8 并发。过滤与稳定性保持串行。
3. 选择“开始运行”，查看当前项目、阶段、轮次和耗时。
4. 完成后查看报告。`Esc` / `Ctrl-C` 取消时等待清理和报告保存。

常规性能默认每轮预热 10 秒、目标测量 30 秒、1 轮。“更多设置”可以调整时长，正式性能记录可选 3 轮。导入、建索引只执行一次；多个场景、并发档位和独立稳定性检查会增加整体时间。

## 3. 查看报告

```bash
./mo-search-lab ui --reports reports
```

`d` 选择历史运行；左右键切换该运行内的报告；Tab 切页面；`?` 查看指标说明。删除只在 `d` 历史列表中进行，`x` 预览、`y` 确认。

每次运行自动生成 `report.html`，无需先执行 `render`。把整个报告目录拷回电脑，直接打开 HTML；`report.json` 保留原始测量与执行证据。多项目运行各有一份报告，`batch.json` 记录整次运行的关系。

性能页展示单并发延迟、吞吐、各轮 QPS、样本量与波动。质量和稳定性是观测值；低召回或结果变化不会单独决定执行失败。QPS 使用包含在途请求收尾的实际耗时，质量计算在计时结束后执行。详见 [测量口径](performance-measurement.md)。

## 离线现场

在有网络的机器先运行安装脚本，将**整个安装目录**打包带到现场即可：

```bash
tar -czf mo-search-lab-offline.tar.gz mo-search-lab-v0.14.1-linux-amd64
```

现场解压后直接执行第 2 节，不需要互联网：

```bash
tar -xzf mo-search-lab-offline.tar.gz
cd mo-search-lab-v0.14.1-linux-amd64
./mo-search-lab ui --launch --reports reports --host MO_SQL_HOST --password '你的密码'
```

也可以把该 Release 的 `setup.sh`、`SHA256SUMS`、`archive.sha256` 和所有 `.partNN` 附件放在同一目录带到现场，执行 `bash setup.sh --offline`；脚本完成校验和解压，无需手动合并。

## 可选操作

只读检查连接、版本和配置：

```bash
./mo-search-lab inspect --host MO_SQL_HOST --password '你的密码' \
  --report-dir reports/environment-001
```

快速验证建库、导入、索引、查询及报告流程：

```bash
./mo-search-lab run --host MO_SQL_HOST --password '你的密码' \
  --pack packs/smoke --measure-duration 0 --report-dir reports/smoke-001
```

只跑向量性能与召回：

```bash
./mo-search-lab run --host MO_SQL_HOST --password '你的密码' \
  --pack packs/vector --report-dir reports/vector-001
```

每次使用新的报告目录。测试库默认自动清理，报告保留；调查时可选 `--keep-db`。环境采集自动执行，TOML、Grafana/Prometheus 等输入可选，见 [环境说明](environment.md)。

需要临时 HTTP 查看，运行机已有 Python 3 时可执行：

```bash
python3 -m http.server 8766 --bind 127.0.0.1 --directory reports
```

电脑上做 SSH 转发（替换入口）：

```bash
ssh -p SSH_PORT -N -L 127.0.0.1:18766:127.0.0.1:8766 USER@SSH_HOST
```

浏览器访问 `http://127.0.0.1:18766/`。HTTP 服务是可选查看方式，不是测试依赖。
