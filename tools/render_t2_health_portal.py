#!/usr/bin/env python3
"""Publish a paired T2Ranking page from immutable measured reports (off-site)."""
import argparse
import html
import json
import math
import os
from pathlib import Path
import re
import tempfile
from render_single_concurrency import CHART_CSS, render_chart


def quality_metric_help(panel_class="detail-panel", scroll_class="chart-scroll") -> str:
    return f'''<details id="quality-metric-help" class="{panel_class}"><summary>指标说明：DCG、nDCG 与 MRR</summary>
<p><strong>DCG 看整体排序质量；MRR 看第一个相关结果出现得有多早。</strong></p>
<div class="{scroll_class}"><table><tr><th>指标</th><th>关注什么</th><th>后面的相关结果是否影响分数</th></tr>
<tr><td>DCG@K</td><td>前 K 条中，相关程度高的结果是否排在前面</td><td>会；位置越靠后，贡献越小</td></tr>
<tr><td>nDCG@K</td><td>实际 DCG 与该问题理想排序的 DCG 之比</td><td>会；归一化到 0～1，越高越好</td></tr>
<tr><td>MRR@K</td><td>第一个相关结果的排名倒数，再对所有问题取平均</td><td>第一个相关结果之后的结果不影响</td></tr></table></div>
<p>当前采用线性收益：第 r 名的贡献为“相关性等级 ÷ log₂(r + 1)”，累加前 K 名得到 DCG。nDCG = DCG ÷ 理想排序的 DCG；理想排序按这个问题的全部标注等级从高到低排列。</p>
<p>MRR 中，第一个相关结果在第 1 名贡献 1，在第 2 名贡献 0.5，在第 10 名贡献 0.1；前 K 条没有相关结果则贡献 0，最后对所有问题取平均。</p>
<p><strong>例子：</strong>同一个问题有两篇相关文档，等级分别为 3 和 2。比较前 3 条返回结果：</p>
<div class="{scroll_class}"><table><tr><th>结果（相关性等级）</th><th>DCG@3</th><th>nDCG@3</th><th>这个问题对 MRR 的贡献</th></tr>
<tr><td>A：[3，2，0]</td><td>约 4.262</td><td>1.000</td><td>1</td></tr>
<tr><td>B：[3，0，0]</td><td>3.000</td><td>约 0.704</td><td>1</td></tr></table></div>
<p>两组第一条都找到了相关文档，对 MRR 的贡献相同；A 的第二条也相关，因此 DCG 和 nDCG 更高。</p>
<p><strong>本报告口径：</strong>DCG/nDCG 使用等级 0～3，等级 1～3 都有收益；MRR 将等级 ≥2 视为相关，只看前 10 条。未标注文档按 0 处理。报告展示 nDCG@10、nDCG@100 和 MRR@10；nDCG 不能理解为准确率百分比。</p>
</details>'''


def render(root: Path, ngram: str, gojieba: str, functional: str) -> str:
    reports = [(name, json.loads((root / folder / "report.json").read_text()), folder)
               for name, folder in (("ngram", ngram), ("gojieba", gojieba))]
    function_report = json.loads((root / functional / "report.json").read_text())
    series = []
    signatures = []
    sources = []
    stability_budgets = []
    stable_fragments = []
    style = ""
    success = errors = changes = 0
    incomplete = False
    for parser, report, folder in reports:
        source = json.loads((root / folder / "source.json").read_text())
        sources.append(source)
        if not any(re.search(rf"\bWITH\s+PARSER\s+{parser}\b", sql, re.I)
                   for sql in report.get("index_sql", [])):
            raise ValueError(f"measured index configuration does not identify {parser}")
        page = (root / folder / "report.html").read_text()
        style = page.split("<style>", 1)[1].split("</style>", 1)[0]
        fragment = re.search(r'<section id="stability".*?</section>', page, re.S)
        if fragment:
            body = fragment.group().replace('id="stability"', f'id="stability-{parser}"')
            body = body.replace('href="report.json"', f'href="../{folder}/report.json"')
            stable_fragments.append(f'<h2 class="parser-heading">{parser} · 重复结果检查</h2>' + body)
        if report.get("cleanup") != "dropped" or any(stage.get("error") for stage in report["stages"]):
            incomplete = True
        for scene in report["scenarios"]:
            if scene.get("error") or not scene["results"]:
                incomplete = True
            for result in scene["results"]:
                if result["sql_succeeded"]:
                    success += 1
                else:
                    errors += 1
            if scene["oracle"] == "stable_multiset":
                changes += sum(not result["pass"] and result["sql_succeeded"] for result in scene["results"])
        ordinary = [scene for scene in report["scenarios"] if scene["oracle"] == "qrels"]
        repeated = [scene for scene in report["scenarios"] if scene["oracle"] == "stable_multiset"]
        if {scene["id"] for scene in repeated} != {"anli_sql_tfidf_stability", "anli_sql_bm25_stability"}:
            raise ValueError("paired portal requires measured stability for both algorithms")
        for scene in repeated:
            if not scene.get("check_order") or not scene.get("allow_empty"):
                raise ValueError("paired stability must explicitly check order and allow empty rankings")
            stability_budgets.append((scene["selected_queries"], scene["repetitions"]))
        for algorithm in ("tfidf", "bm25"):
            profiles = sorted((scene for scene in ordinary if scene["id"] == f"anli_sql_{algorithm}"),
                              key=lambda scene: scene["effective_concurrency"])
            if [scene["effective_concurrency"] for scene in profiles] != [1, 4, 8]:
                raise ValueError("paired portal requires measured levels 1/4/8 for each algorithm")
            if any(scene.get("top_k") != 100 or scene.get("ndcg_gain") != "linear" or scene.get("relevant_grade") != 2 or
                   scene.get("quality_mode") != "observe" for scene in profiles):
                raise ValueError("paired portal requires explicit observational T2Ranking metrics")
            expected_algorithm = "TF-IDF" if algorithm == "tfidf" else "BM25"
            if any(not any(re.search(rf"ft_relevancy_algorithm\s*=\s*'{expected_algorithm}'", sql, re.I)
                           for sql in scene.get("session_sql", [])) for scene in profiles):
                raise ValueError("measured session scoring algorithm differs from the chart label")
            if any(not scene["sql_successes"] for scene in profiles):
                raise ValueError("no successful latency samples; inspect the standalone failed-run report")
            baseline = profiles[0]
            samples = [result for result in baseline["results"] if result["sql_succeeded"]]
            if not samples or any("quality" not in result for result in samples):
                raise ValueError("quality measurements are absent; do not publish a fabricated curve")
            signatures.extend((report["inputs"][0]["sha256"], scene["queries_sha256"]) for scene in profiles)
            if any(not math.isfinite(value) or not 0 <= value <= 1
                   for result in samples for value in result["quality"].values()):
                raise ValueError("quality measurements must be finite values in 0..1")
            series.append({"label": f"{parser} / {'TF-IDF' if algorithm == 'tfidf' else 'BM25'}",
                           "parser": parser, "algorithm": algorithm,
                           "metrics": {key: sum(result["quality"][key] for result in samples) / len(samples)
                                       for key in ("ndcg", "ndcg_at_10", "recall", "mrr_at_10")},
                           "empty": sum(not result.get("ids") for result in samples),
                           "samples": len(samples),
                           "queries": len({result["id"] for result in samples}),
                           "profiles": [{key: scene[key] for key in ("effective_concurrency", "qps", "p90_ms", "p95_ms", "p99_ms", "sql_successes", "sql_failures")}
                                        for scene in profiles]})
    if len(set(signatures)) != 1:
        raise ValueError("paired corpus/query digests differ")
    if reports[0][1]["inputs"][0]["rows"] != reports[1][1]["inputs"][0]["rows"]:
        raise ValueError("paired corpus sizes differ")
    rows = reports[0][1]["inputs"][0]["rows"]
    candidate_count = sources[0]["candidate_queries"]
    skipped_count = len(sources[0]["application_skipped_queries"])
    if any(source["query_ids"] != sources[0]["query_ids"] or
           source["application_skipped_queries"] != sources[0]["application_skipped_queries"] for source in sources):
        raise ValueError("paired query selection provenance differs")
    if len(set(stability_budgets)) != 1:
        raise ValueError("paired stability budgets differ")
    stable_queries, stable_repeats = stability_budgets[0]
    functional_checks = sum(scene["executions"] for scene in function_report["scenarios"])
    functional_failures = sum(scene["failures"] for scene in function_report["scenarios"])
    functional_complete = function_report["status"] == "passed" and functional_checks > 0
    health = "通过" if not incomplete and not errors and functional_complete else "异常 / 未完成"
    stable = "未完成" if incomplete or errors else ("未发现变化" if not changes else f"{changes} 次检查未通过")
    links = "".join(f'<a href="../{folder}/report.html">{parser} 完整报告</a><a href="../{folder}/report.json">{parser} 原始记录</a>'
                    for parser, _, folder in reports)
    source_links = "".join(f'<a href="../{folder}/source.json">{parser} 数据与分词来源</a>'
                           f'<a href="../{folder}/comparison.json">{parser} 指标独立复算</a>'
                           for parser, _, folder in reports)
    numbers = "".join(f'<tr><td>{html.escape(item["label"])}</td><td>{item["queries"]}</td>'
                      + "".join(f'<td>{item["metrics"][key]:.4f}</td>' for key in ("ndcg_at_10", "ndcg", "recall", "mrr_at_10"))
                      + f'<td>{item["empty"]}/{item["samples"]}</td></tr>' for item in series)
    payload = json.dumps(series, ensure_ascii=False).replace("<", "\\u003c").replace("&", "\\u0026")
    reference_record = '<details id="es-reference" class="detail-panel"><summary>参考记录：ES 本机实测</summary><p><a href="t2ranking-es.html">查看 ES 实测数据与机器配置</a></p></details>' if (root / 'datasets/t2ranking-es.html').is_file() else ''
    single_concurrency = render_chart([{"label": item["label"],
                                       "profile": next(profile for profile in item["profiles"]
                                                       if profile["effective_concurrency"] == 1)}
                                      for item in series])
    workload_fragment = root / 'datasets/database-workloads-fragment.html'
    workload_panel = workload_fragment.read_text() if workload_fragment.is_file() else ''
    workload_style = re.search(r'<style id="database-chart-style">.*?</style>',workload_panel,re.S)
    workload_style = workload_style.group() if workload_style else ''
    workload_panel = workload_panel.replace(workload_style,'') if workload_style else workload_panel
    return f'''<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>T2Ranking · anli 场景评测</title><style>{style}
*{{box-sizing:border-box}}body{{background:#f3f5f6;color:#213442}}main{{max-width:1220px;padding:36px 24px 64px}}.eyebrow{{font-size:12px;letter-spacing:.13em;color:#63747d}}h1{{font-size:34px;letter-spacing:-.03em;margin:12px 0}}.title-tail{{display:inline-block}}.lead{{max-width:85ch}}.dataset-selector{{display:flex;align-items:center;gap:18px;background:#fff;border-left:3px solid #193b55;padding:16px;margin:24px 0}}select{{max-width:100%;padding:9px 12px;font-size:14px;font-family:inherit;border:1px solid #becbd3;background:#fff;border-radius:4px;color:#213442}}.comparison-grid{{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:20px}}.comparison-grid .card{{margin:12px 0}}.comparison-grid h2{{font-size:20px;margin:0 0 6px}}.plot{{display:block;width:100%;min-width:300px}}.plot text{{fill:#5c6c7d;font:20px ui-monospace,Consolas,monospace}}.plot-legend{{display:flex;gap:8px 20px;flex-wrap:wrap;font-size:13px;margin:12px 0}}.plot-legend span{{display:flex;gap:7px;align-items:center}}.plot-legend i{{width:10px;height:10px;border-radius:2px}}.case-scope{{border-left:3px solid #087f8c;padding:8px 18px;margin:24px 0;max-width:95ch}}.links{{display:flex;gap:12px 20px;flex-wrap:wrap;font-size:13px;margin:20px 0}}.parser-heading{{margin-top:36px}}.control{{margin:10px 0}}.status-flag{{color:#8a6028}}.finding strong{{font-size:23px}}a:focus-visible,select:focus-visible{{outline:2px solid #087f8c;outline-offset:3px}}
{CHART_CSS}
@media(max-width:900px){{.comparison-grid{{grid-template-columns:1fr}}}}@media(max-width:600px){{main{{padding:24px 16px}}h1{{font-size:28px}}.dataset-selector{{display:block}}.dataset-selector label{{display:block;margin-bottom:8px}}.dataset-selector select{{width:100%}}}}
</style>{workload_style}</head><body><main><header><span class="eyebrow">MATRIXONE / ANLI FULLTEXT WORKLOAD</span><h1>全文检索：<span class="title-tail">健康、质量与稳定性</span></h1><p class="lead">同一公开语料、同一批冻结查询，比较索引分词器与相关性算法。应用先按 anli 规则分词，再运行其全文 SQL。</p><small>{rows:,} 段文本 · {series[0]['queries']} 条固定开发集查询 · Top-100 · MO 4.2.1 · 单 CN · 8 CPU / 16 GiB</small></header>
<nav class="dataset-selector"><label for="dataset-select">测试集</label><select id="dataset-select" data-current="t2ranking.html" onchange="location.href=this.value"><option value="gist1m.html">GIST1M · 向量检索</option><option value="t2ranking.html" selected>T2Ranking · anli 场景 · 完整语料</option></select></nav>
{single_concurrency}
<div class="plot-legend" id="legend"></div><section id="comparison" class="comparison-grid"><article class="card"><h2>检索质量</h2><p class="chart-note">并发 1 · 0–1 · 越高越好；空结果率越低越好</p><div class="control"><label for="quality-metric">指标 </label><select id="quality-metric"><option value="ndcg_at_10">nDCG@10</option><option value="ndcg">nDCG@100</option><option value="recall">Recall@100</option><option value="mrr_at_10">MRR@10</option><option value="empty_rate">空结果率</option></select></div><svg id="quality-plot" class="plot" viewBox="0 0 600 280" role="img" aria-label="四组配置的相关性与召回率对照"></svg></article><article class="card"><h2>查询吞吐</h2><p class="chart-note">成功 SQL / 秒 · 越高越好</p><svg id="qps-plot" class="plot" style="margin-top:58px" viewBox="0 0 600 280" role="img" aria-label="并发 1、4、8 的 QPS"></svg></article><article class="card"><h2>尾部延迟</h2><p class="chart-note">毫秒 · 越低越好</p><div class="control"><label for="latency-metric">分位数 </label><select id="latency-metric"><option value="p90_ms">P90</option><option value="p95_ms" selected>P95</option><option value="p99_ms">P99</option></select></div><svg id="latency-plot" class="plot" viewBox="0 0 600 280" role="img" aria-label="并发 1、4、8 的延迟分位数"></svg></article></section>
<p class="chart-note">每个并发档位执行同一批 {series[0]['queries']} 条查询，测量一次、预热 5 次。各配置顺序执行，缓存和运行顺序可能影响延迟；百分位使用最近秩法，质量空结果以 0 计入。悬停可核对实测数值。</p>
{workload_panel}
{quality_metric_help()}
<details class="detail-panel"><summary>检索质量完整数值 · 并发 1</summary><div class="chart-scroll"><table><tr><th>配置</th><th>查询数</th><th>nDCG@10</th><th>nDCG@100</th><th>Recall@100</th><th>MRR@10</th><th>空结果</th></tr>{numbers}</table></div></details>
{''.join(stable_fragments)}
{reference_record}
<details class="detail-panel"><summary>测量范围与数据来源</summary><p>完整公开语料＋固定种子的开发集抽样，衡量 anli 的全文检索环节。{candidate_count} 条候选中，{skipped_count} 条分词后为空，按应用规则跳过；最终冻结 {series[0]["queries"]} 条可搜索查询。应用分词已提前冻结，现场工具为独立二进制。候选放大、Top-K 后低分过滤沿用 anli SQL，权限为合成 OPEN，时间固定。</p><p>客户实际 parser 和评分配置尚未确认；本次不包含真实权限选择率、详情读取、向量融合和精排。单 CN 测量没有验证多 CN sort 修复。未标注段落按零收益评分，不意味着其实际无关；成绩不能与官方全测试集直接比较。</p></details>
<div class="links">{links}{source_links}<a href="../{functional}/report.html">功能检查结果</a><a href="../{functional}/report.json">功能检查原始记录</a><a href="../{ngram}/environment.json">测试部署与资源配置</a><a href="../{ngram}/query-inputs.jsonl">{series[0]["queries"]} 条冻结查询与人工标注</a><a href="../downloads/mo-retrieval-bench-v0.7.0-linux-amd64.tar.gz">下载独立二进制 · v0.7.0</a></div><details class="detail-panel"><summary>历史 10 万段落／10 条查询对照</summary><p><a href="../t2ranking-anli-health-20261006/report.html">历史 anli 对照报告</a> · <a href="../t2ranking-100k-final-current-binary/report.html">历史整句 Top-10 报告</a></p><p>历史 nDCG 使用指数收益，当前使用线性收益；历史原始 JSON 保留。</p></details>
<script id="measured-series" type="application/json">{payload}</script><script>
const series=JSON.parse(document.getElementById('measured-series').textContent),colors=['#087f8c','#315eb4','#b96a23','#9d4762'];
document.getElementById('legend').innerHTML=series.map((s,i)=>`<span><i style="background:${{colors[i]}}"></i>${{s.label}}</span>`).join('');
const fmt=v=>v.toFixed(2),axis=max=>[0,.25,.5,.75,1].map(t=>`<line x1="65" x2="550" y1="${{225-t*190}}" y2="${{225-t*190}}" stroke="#e1e8ed"/><text x="56" y="${{229-t*190}}" text-anchor="end">${{max>=100?Math.round(t*max):fmt(t*max)}}</text>`).join('');
function bars(){{const key=document.getElementById('quality-metric').value;document.getElementById('quality-plot').innerHTML=axis(1)+series.map((s,i)=>{{const v=key==='empty_rate'?s.empty/s.samples:s.metrics[key],x=88+i*117;return `<g><title>${{s.label}} · ${{key}} ${{v.toFixed(4)}}</title><rect x="${{x}}" y="${{225-v*190}}" width="72" height="${{v*190}}" fill="${{colors[i]}}"/><text x="${{x+36}}" y="${{Math.max(20,218-v*190)}}" text-anchor="middle">${{v.toFixed(3)}}</text><text x="${{x+36}}" y="250" text-anchor="middle">${{s.parser}}</text><text x="${{x+36}}" y="269" text-anchor="middle">${{s.algorithm==='tfidf'?'TF-IDF':'BM25'}}</text></g>`}}).join('')}}
function lines(id,key){{const max=Math.max(1,...series.flatMap(s=>s.profiles.map(p=>p[key])))*1.15;let body=axis(max);body+=[1,4,8].map((c,i)=>`<text x="${{85+i*220}}" y="253" text-anchor="middle">${{c}}</text>`).join('')+'<text x="300" y="277" text-anchor="middle">客户端并发</text>';series.forEach((s,i)=>{{const points=s.profiles.map((p,j)=>[85+j*220,225-p[key]/max*190,p]);body+=`<polyline points="${{points.map(p=>p.slice(0,2).join(',')).join(' ')}}" fill="none" stroke="${{colors[i]}}" stroke-width="3"/>`;body+=points.map(([x,y,p])=>`<circle cx="${{x}}" cy="${{y}}" r="5" fill="${{colors[i]}}"><title>${{s.label}} · 并发 ${{p.effective_concurrency}} · ${{key}} ${{fmt(p[key])}}</title></circle>`).join('')}});document.getElementById(id).innerHTML=body}}
document.getElementById('quality-metric').addEventListener('change',bars);document.getElementById('latency-metric').addEventListener('change',()=>lines('latency-plot',document.getElementById('latency-metric').value));bars();lines('qps-plot','qps');lines('latency-plot','p95_ms');window.addEventListener('pageshow',()=>{{document.getElementById('dataset-select').value='t2ranking.html'}});
</script></main></body></html>'''


if __name__ == "__main__":
    cli = argparse.ArgumentParser(description=__doc__)
    cli.add_argument("--reports-root", type=Path, required=True)
    cli.add_argument("--ngram-report", required=True)
    cli.add_argument("--gojieba-report", required=True)
    cli.add_argument("--functional-report", required=True)
    args = cli.parse_args()
    target = args.reports_root / "datasets/t2ranking.html"
    body = render(args.reports_root, args.ngram_report, args.gojieba_report, args.functional_report)
    temporary = None
    try:
        with tempfile.NamedTemporaryFile(mode="w", encoding="utf-8", dir=target.parent,
                                         prefix=".t2ranking-", suffix=".html", delete=False) as stream:
            temporary = Path(stream.name)
            stream.write(body)
        os.replace(temporary, target)
    finally:
        if temporary is not None:
            temporary.unlink(missing_ok=True)
    print(f"Published paired report: {target}")
