// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0.
package main

import (
	"flag"
	"fmt"
	"io"
)

const rootHelp = `MO Search Lab · MO 全文与向量评测、问题复现与调查

用法：
  mo-search-lab <命令> [参数]
  mo-search-lab help [命令]

命令：
  ui        交互式启动测试、检查环境、查看和管理历史报告
  run       按数据包执行测试，保存 JSON 和 HTML 报告
  inspect   只读采集 MO 版本、节点和配置，保存环境报告
  validate  校验数据包及输入 SHA-256，不连接 MO
  render    从已有 report.json 重新生成 HTML，不重跑测试
  version   显示工具版本

开始使用：
  mo-search-lab ui --launch --reports reports --packs packs --password '你的密码'
  mo-search-lab ui --reports reports
  mo-search-lab run --pack packs/smoke --password '你的密码' --report-dir reports/smoke-001

默认测量全部普通查询、1 并发；稳定性最多 5 条查询各重复 30 次。
质量和稳定性作为观测值；数据准备、SQL 或清理错误影响完成状态。
SQL 密码通过 --password 传入；数据包另行准备。
使用 mo-search-lab <命令> --help 查看参数和示例。
`

var commandHelp = map[string]string{
	"ui": `用法：mo-search-lab ui [参数]

启动新任务或查看保存的报告：
  mo-search-lab ui --launch --reports reports --packs packs --password '你的密码'
  mo-search-lab ui --reports reports
  mo-search-lab ui --report-dir reports/benchmark-时间
  mo-search-lab ui --reports reports --plain --section stability

新任务默认勾选已安装常用测试，每个数据集默认 1 并发。
在测试列表中按空格勾选数据集及额外 4、8 并发；过滤检查保持串行。
--launch 直接打开表单；不加时先看最新报告，无报告时自动打开表单。
--host/--port/--user/--password 用于预填新任务；查看报告无需连接 MO。
--concurrency 仅筛选已有报告，运行并发在测试列表中选择。

报告按键：
  d            选择历史 run；↑↓ 选择，Enter 打开，Esc 返回
  ←/→          切换该 run 内的测试报告
  Tab/Shift-Tab 切换报告页；1..6 直达概览、并发、质量、稳定性、SQL、环境
  l / t        启动新任务 / 返回本次测试结果
  p / c        概览或并发页切分位数 / 并发或质量页筛选并发
  ? / q        指标说明 / 退出；任务运行中 Esc/Ctrl-C 取消并等待清理
历史列表中 x/Delete 预览整次删除，y 确认，Enter/n/Esc 取消。
--plain 仅输出所选页面；--launch 需要交互式终端。
`,
	"run": `用法：mo-search-lab run --pack DIR [参数]

校验输入 → 建测试库 → 导入 → 建索引 → 预热 → 测量 → 清理 → 保存报告。
--pack 必填；连接默认 127.0.0.1:6001、root、空密码，其他参数可省略。
默认全部普通查询各测 1 次、1 并发、预热 5 次；稳定性最多 5 条各重复 30 次。
召回、相关性及集合/顺序变化均为观测值，不设验收断言。
输出目录必须是新路径；--keep-db 可保留测试库用于调查。

示例：
  mo-search-lab run --pack packs/smoke --password '你的密码' --report-dir reports/smoke-001
  mo-search-lab run --pack /data/gist-health --concurrency-levels 1,4,8
  mo-search-lab run --pack /data/gist-health --query-limit 20 --stability-repeat 10
  mo-search-lab run --pack /data/gist-t2-workload --mixed-scenarios vector,fulltext

减少查询数或重复数仍会导入全部数据并建立索引。
稳定性始终串行，预算独立于普通查询；多并发复用同一次导入和索引。
`,
	"inspect": `用法：mo-search-lab inspect [参数]

只读采集 MO 版本、构建、可见节点、缓存与检索配置，保存环境报告。
不建测试库或索引；连接默认 127.0.0.1:6001、root、空密码。
TOML、部署声明和 Grafana/Prometheus 配置均为可选，缺失信息标明未知。

示例：
  mo-search-lab inspect --host 10.0.0.10 --password '你的密码' --report-dir reports/environment-001
  mo-search-lab inspect --mo-config /path/to/cn.toml --mo-config /path/to/tn.toml
  mo-search-lab ui --report-dir reports/environment-001
`,
	"validate": `用法：mo-search-lab validate --pack DIR

校验 manifest、场景、查询与全部导入文件的 SHA-256，不连接 MO。
百万级数据包的校验需要读取完整数据文件。

示例：mo-search-lab validate --pack packs/smoke
`,
	"render": `用法：mo-search-lab render --report-dir DIR [参数]

读取指定报告目录的 report.json，更新 report.html，不连接 MO 或重跑测试。
保留原始 JSON、测量身份与状态；只能筛选已经测量的场景和并发。
此处 --report-dir 是单份报告目录，不是多项运行的汇总目录。

示例：
  mo-search-lab render --report-dir reports/gist-001
  mo-search-lab render --report-dir reports/gist-001 --concurrency-levels 1 --scenario-ids vector
`,
	"version": "用法：mo-search-lab version\n\n显示当前工具版本。\n",
}

func writeRootHelp(w io.Writer) { fmt.Fprint(w, rootHelp) }

func configureCommandHelp(fs *flag.FlagSet) {
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), commandHelp[fs.Name()])
		fmt.Fprintln(fs.Output(), "\n参数：")
		fs.PrintDefaults()
	}
}
