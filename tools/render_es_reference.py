#!/usr/bin/env python3
"""Publish a standalone ES measurement and machine configuration record."""
import argparse
import hashlib
import html
import json
import math
import os
from pathlib import Path
import tempfile

from render_t2_health_portal import quality_metric_help


def measured_series(root, es_folder):
    series = []
    es = json.loads((root / es_folder / 'report.json').read_text())
    query_bytes = (root / es_folder / 'query-inputs.jsonl').read_bytes()
    if es['status'] != 'passed' or es.get('indexed_rows') != es['inputs'][0]['rows']:
        raise ValueError('ES run must be complete with the full indexed corpus')
    if hashlib.sha256(query_bytes).hexdigest() != es['queries_sha256']:
        raise ValueError('saved ES query inputs failed digest validation')
    if [json.loads(line)['id'] for line in query_bytes.splitlines()] != es['query_ids']:
        raise ValueError('saved ES query IDs differ from the measured cohort')
    if len(es['query_ids']) != 500 or len(set(es['query_ids'])) != 500:
        raise ValueError('this ES record requires 500 unique queries')
    environment = json.loads((root / es_folder / 'environment.json').read_text())
    if environment['cpu_limit'] <= 0 or environment['memory_limit_bytes'] <= 0 or environment['node_count'] != 1:
        raise ValueError('ES resource configuration must identify the measured single-node fixture')
    definition = es['index_definition']
    if definition['settings']['number_of_shards'] != 1 or definition['settings']['number_of_replicas'] != 0:
        raise ValueError('ES index topology differs from this baseline')
    if definition['mappings']['properties']['body'] != {'type': 'text', 'analyzer': 'cjk', 'similarity': 'BM25'}:
        raise ValueError('ES chart labels must identify the measured CJK/BM25 configuration')
    stable_scenes = []
    for field, label in [('anli_text', 'anli 搜索词'), ('raw_text', '原始问题')]:
        profiles = sorted((s for s in es['scenarios'] if s['id'] == f'es_cjk_{field}'), key=lambda s: s['effective_concurrency'])
        if [s['effective_concurrency'] for s in profiles] != [1, 4, 8]:
            raise ValueError('ES requires exactly one measured profile for levels 1/4/8')
        for scene in profiles:
            repeats = scene.get('repetitions', 1)
            if not 1 <= repeats <= 20 or repeats != es['profile']['repeat']:
                raise ValueError('ES repetition budget differs between profiles')
            if scene['sql_failures'] or scene['sql_successes'] != 500 * repeats or len(scene['results']) != 500 * repeats:
                raise ValueError('ES incomplete/failed profile must not become a measurement curve')
            if [r['id'] for r in scene['results']] != es['query_ids'] * repeats or not all(r['sql_succeeded'] for r in scene['results']):
                raise ValueError('ES profile query cohort or execution outcomes differ')
            if (scene['top_k'], scene['ndcg_gain'], scene['relevant_grade'], scene['quality_mode']) != (100, 'linear', 2, 'observe'):
                raise ValueError('ES quality definitions differ')
            if not all(math.isfinite(scene[key]) and scene[key] > 0 for key in ('qps', 'p90_ms', 'p95_ms', 'p99_ms')):
                raise ValueError('ES measured performance must be finite and positive')
        samples = profiles[0]['results'][:500]
        if any(not math.isfinite(value) or not 0 <= value <= 1 for r in samples for value in r['quality'].values()):
            raise ValueError('invalid ES quality measurements')
        series.append({'label': f'ES / CJK / BM25 / {label}', 'engine': 'ES', 'parser': 'CJK', 'algorithm': 'bm25',
                       'short': label, 'queries': len(samples), 'samples': len(samples), 'repetitions': es['profile']['repeat'],
                       'empty': sum(not r['ids'] for r in samples),
                       'metrics': {key: sum(r['quality'][key] for r in samples) / len(samples)
                                   for key in ('ndcg', 'ndcg_at_10', 'recall', 'mrr_at_10')},
                       'profiles': [{key: s[key] for key in ('effective_concurrency', 'qps', 'p90_ms', 'p95_ms', 'p99_ms', 'sql_successes', 'sql_failures')}
                                    for s in profiles]})
        repeated = [s for s in es['scenarios'] if s['id'] == f'es_cjk_{field}_stability']
        if len(repeated) != 1:
            raise ValueError('missing ES stability route')
        scene = repeated[0]
        if scene['executions'] != 150 or scene['sql_failures'] or not scene['check_order'] or not scene['allow_empty'] or len(scene['results']) != 150:
            raise ValueError('ES stability must complete five queries x30 with order checks')
        stable_scenes.append((label, scene))
    return series, es, stable_scenes


def render(root, es_folder):
    series, es, stable = measured_series(root, es_folder)
    environment = json.loads((root / es_folder / 'environment.json').read_text())
    host = json.loads((root / es_folder / 'host-configuration.json').read_text())
    def row(label, value):
        return f'<tr><th>{html.escape(label)}</th><td>{html.escape(str(value))}</td></tr>'
    hardware_rows = ''.join(row(label, value) for label, value in [
        ('机器', host['host']), ('CPU', host['cpu_model']),
        ('核心 / 线程', f"{host['physical_cores']} 个物理核心 / {host['logical_cpus']} 个逻辑 CPU"),
        ('系统内存', f"{host['memory_total_bytes']/1024**3:.2f} GiB"),
        ('系统 / 架构', f"{host['os']} / {host['architecture']}"), ('内核', host['kernel']),
        ('数据盘', f"{host['storage']['model']} / {host['storage']['transport']} / {host['storage']['device_capacity_bytes']/10**12:.2f} TB"),
        ('数据目录 / 文件系统', f"{host['storage']['mount']} / {host['storage']['filesystem']}"),
        ('机器配置记录时间', host['recorded_at'])])
    deployment_rows = ''.join(row(label, value) for label, value in [
        ('Elasticsearch', es['server']['version']['number']), ('Lucene', es['server']['version']['lucene_version']),
        ('镜像', environment['image']), ('镜像摘要', environment['image_repo_digests'][0]),
        ('容器 CPU 配额', f"{environment['cpu_limit']:g} CPU"),
        ('容器内存上限', f"{environment['memory_limit_bytes']/1024**3:g} GiB"),
        ('JVM 堆', f"{environment['heap_bytes']/1024**3:g} GiB"),
        ('节点 / 主分片 / 副本', f"{environment['node_count']} / {es['index_definition']['settings']['number_of_shards']} / {es['index_definition']['settings']['number_of_replicas']}"),
        ('分词 / 打分', 'CJK / BM25'), ('开始时间', es['started_at']), ('完成时间', es['finished_at'])])
    payload = json.dumps(series, ensure_ascii=False).replace('<', '\\u003c').replace('&', '\\u0026')
    quality_rows = ''.join('<tr><td>' + html.escape(s['label']) + '</td>' + ''.join(f'<td>{s["metrics"][k]:.4f}</td>' for k in ['ndcg_at_10', 'ndcg', 'recall', 'mrr_at_10']) + f'<td>{s["empty"]}/500</td></tr>' for s in series)
    performance_rows = ''.join('<tr><td>' + html.escape(s['label']) + f'</td><td>{p["effective_concurrency"]}</td>' + ''.join(f'<td>{p[k]:.2f}</td>' for k in ['qps', 'p90_ms', 'p95_ms', 'p99_ms']) + '</tr>' for s in series for p in s['profiles'])
    stability = ''
    changed = 0
    for label, scene in stable:
        rows = ''
        for cell in scene['stability']:
            records = [r for r in scene['results'] if r['id'] == cell['query_id']]
            changed += sum(not r['pass'] for r in records)
            cells = ''.join(f'<span class="sample {"ok" if r["pass"] else "bad"}" title="查询 {html.escape(r["id"])} · 第 {r["iteration"]+1} 次 · {r["latency_ms"]:.2f} ms · {"集合和顺序一致" if r["pass"] else "变化或执行失败"}"></span>' for r in records)
            rows += f'<div class="repeat-row"><span>Q {html.escape(cell["query_id"])}</span><div class="samples">{cells}</div><small>集合 {cell["distinct_results"]} 种 / 顺序 {cell["distinct_orders"]} 种</small></div>'
        stability += '<article class="card"><h3>' + html.escape(label) + '</h3>' + rows + '</article>'
    query_template = html.escape(json.dumps(es['search_template'], ensure_ascii=False, indent=2))
    version = html.escape(es['server']['version']['number'])
    repeats = es['profile']['repeat']
    raw_file = 'report.json.gz' if (root / es_folder / 'report.json.gz').is_file() else 'report.json'
    first_run = 't2ranking-es-cjk-full-500q-20261006'
    first_link = f'<a href="../{first_run}/report.json" download>首次 500 次短时测量原始记录</a>' if (root / first_run / 'report.json').is_file() else ''
    index_settings = html.escape(json.dumps(es['index_definition'], ensure_ascii=False, indent=2))
    return '''<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>T2Ranking · ES 本机实测记录</title><style>
*{box-sizing:border-box}body{margin:0;background:#f3f5f6;color:#213442;font-family:ui-sans-serif,"PingFang SC","Microsoft YaHei",sans-serif;line-height:1.55}main{max-width:1280px;margin:auto;padding:34px 24px 64px}h1{font-size:34px;letter-spacing:-.025em;line-height:1.2;margin:12px 0}.tail{display:inline-block}.eyebrow{color:#687b86;font:12px ui-monospace,monospace;letter-spacing:.12em}.lead{max-width:85ch}.meta,.note,small{color:#617481;font-size:13px}.navigation,.controls{display:flex;gap:12px 22px;align-items:center;flex-wrap:wrap;margin:22px 0}.navigation{border-left:3px solid #193b55;background:#fff;padding:14px 18px}a{color:#08677c}select{font:inherit;font-size:14px;padding:8px 12px;border:1px solid #b8c8d1;border-radius:4px;background:white;color:#213442}select:focus-visible,a:focus-visible,summary:focus-visible{outline:2px solid #087f8c;outline-offset:3px}.legend{display:flex;flex-wrap:wrap;gap:9px 22px;margin:18px 0;font-size:13px}.legend span{display:flex;align-items:center;gap:7px}.legend i{height:10px;width:10px;border-radius:2px}.charts{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:18px}.card{background:#fff;padding:20px;border:1px solid #dbe3e8;border-radius:6px;margin:12px 0}h2{font-size:21px;margin:0 0 7px}h3{font-size:16px;margin:0 0 16px}.plot{display:block;width:100%;min-width:260px}.plot text{fill:#60717d;font:17px ui-monospace,monospace}.plot .label{font-size:14px}.control{margin:14px 0}.chart-head{min-height:118px}.details{padding:15px 18px;margin:18px 0;border:1px solid #d1dce3;border-radius:6px;background:#fff}.details summary{cursor:pointer;font-size:14px;font-weight:600}.scroll{overflow-x:auto;margin-top:14px}table{border-collapse:collapse;white-space:nowrap;font-size:13px;width:100%}th,td{padding:10px 14px;border-bottom:1px solid #e2e9ed;text-align:right}th:first-child,td:first-child{text-align:left}pre{font:13px ui-monospace,monospace;background:#f3f6f7;padding:16px;overflow:auto}.repeat{display:grid;grid-template-columns:1fr 1fr;gap:18px}.repeat-row{display:grid;grid-template-columns:65px minmax(0,1fr);gap:7px 10px;margin:12px 0;font-size:12px}.repeat-row small{grid-column:2;font-size:11px}.samples{display:grid;grid-template-columns:repeat(30,minmax(0,1fr));gap:3px;align-items:center}.sample{height:16px;border-radius:2px}.ok{background:#168772}.bad{background:#c04642}.links{display:flex;gap:10px 22px;flex-wrap:wrap;margin-top:22px;font-size:13px}.scope{max-width:100ch}.configuration-table{white-space:normal;table-layout:fixed}.configuration-table th{width:30%}.configuration-table td{text-align:left;overflow-wrap:anywhere}.status{border-left:3px solid #168772;padding:9px 18px;font-size:14px;margin:22px 0}
@media(max-width:1000px){.charts{grid-template-columns:1fr}.chart-head{min-height:0}.repeat{grid-template-columns:1fr}}@media(max-width:600px){main{padding:24px 16px}h1{font-size:28px}.navigation{align-items:flex-start}.card{padding:16px}.controls label{width:100%}.repeat-row{grid-template-columns:55px minmax(0,1fr)}.samples{gap:2px}.sample{height:14px}}
</style></head><body><main><header><span class="eyebrow">T2RANKING / ELASTICSEARCH REFERENCE</span><h1>Elasticsearch：<span class="tail">本机实测记录</span></h1><p class="lead">保存本机公开数据集上的 ES 实测数据、机器配置和查询设置，供后续查阅。客户现场检测报告记录当地服务的表现。</p><p class="meta">''' + f"{es['indexed_rows']:,}" + ''' 段文本 · 500 个固定问题 · Top-100 · ES ''' + version + ''' · CJK / BM25</p><p class="meta">主机：''' + html.escape(host['cpu_model']) + f" · {host['physical_cores']} 核 / {host['logical_cpus']} 线程 · {host['memory_total_bytes']/1024**3:.2f} GiB 内存" + '''</p></header>
<nav class="navigation"><a href="t2ranking.html">返回现场检测报告</a><span>ES 本机实测记录</span></nav>
<details id="machine-configuration" class="details"><summary>机器与 ES 配置</summary><h3>主机硬件与系统</h3><div class="scroll"><table class="configuration-table">''' + hardware_rows + '''</table></div><h3>本次 ES 部署</h3><div class="scroll"><table class="configuration-table">''' + deployment_rows + '''</table></div><p class="note">主机资源与容器配额分别记录。机器配置在测试后补充采集，记录时间如上；这些配置值不表示测试期间的实际资源利用率。</p></details>
<div id="legend" class="legend"></div>
<section class="charts"><article class="card"><div class="chart-head"><h2>检索质量</h2><p class="note">并发 1 · 0–1 · 越高越好，空结果率越低越好</p><div class="control"><label for="quality-metric">指标 </label><select id="quality-metric"><option value="ndcg_at_10">nDCG@10</option><option value="ndcg">nDCG@100</option><option value="recall">Recall@100</option><option value="mrr_at_10">MRR@10</option><option value="empty_rate">空结果率</option></select></div></div><svg id="quality-plot" class="plot" viewBox="0 0 600 280" role="img" aria-label="ES 检索质量"></svg></article>
<article class="card"><div class="chart-head"><h2>查询吞吐</h2><p class="note">成功查询 / 秒 · 越高越好</p></div><svg id="qps-plot" class="plot" viewBox="0 0 600 280" role="img" aria-label="并发 1、4、8 的查询吞吐"></svg></article>
<article class="card"><div class="chart-head"><h2>尾部延迟</h2><p class="note">毫秒 · 越低越好</p><div class="control"><label for="latency-metric">分位数 </label><select id="latency-metric"><option value="p90_ms">P90</option><option value="p95_ms" selected>P95</option><option value="p99_ms">P99</option></select></div></div><svg id="latency-plot" class="plot" viewBox="0 0 600 280" role="img" aria-label="ES 延迟分位数"></svg></article></section>
<p class="note">ES 将 500 个固定问题循环 ''' + str(repeats) + ''' 次，每档 ''' + f'{500 * repeats:,}' + ''' 次请求，预热 5 次。质量图取并发 1 的首轮 500 个结果。客户端耗时包含请求和结果读取；ES took 作为服务端补充记录。缓存和运行顺序可能影响结果。悬停可查看数值。</p>
''' + quality_metric_help('details', 'scroll') + '''
<details class="details"><summary>检索质量完整数值 · 并发 1</summary><div class="scroll"><table><tr><th>配置</th><th>nDCG@10</th><th>nDCG@100</th><th>Recall@100</th><th>MRR@10</th><th>空结果</th></tr>''' + quality_rows + '''</table></div></details>
<details class="details"><summary>吞吐与延迟完整数值</summary><div class="scroll"><table><tr><th>配置</th><th>并发</th><th>QPS</th><th>P90 ms</th><th>P95 ms</th><th>P99 ms</th></tr>''' + performance_rows + '''</table></div></details>
<section><h2>ES 重复结果检查</h2><p class="note">每条路线 5 个问题 × 30 次；同时核对集合和顺序。每个方块是一次实际查询。</p><div class="status">300 次查询完成 · ''' + ('集合和顺序未发现变化' if not changed else f'{changed} 次检查发现变化') + '''</div><div class="repeat">''' + stability + '''</div></section>
<details class="details scope"><summary>测量范围与查询设置</summary><p>使用原生 match OR，在内置 CJK 分词、BM25 上分别输入 anli 搜索词与原始问题；最多返回 100 个 ID，低分阈值为 0.01。本次记录不包含正文读取、真实权限过滤、向量融合或精排。</p><p>导入后刷新并等待活动合并结束，未强制合并；保留正常页缓存，显式关闭请求缓存。磁盘写保护采用剩余 15 / 10 / 5 GiB 阈值。质量使用线性 nDCG 和等级 ≥2 的 Recall/MRR，未标注文档按零收益计算；这里只观察成绩，不设验收线。重复检查来自单节点样本。</p><h3>实际 ES 请求模板</h3><pre>POST /&lt;owned-index&gt;/_search?request_cache=false&amp;allow_partial_search_results=false
''' + query_template + '''</pre><h3>索引创建配置</h3><pre>''' + index_settings + '''</pre><p><a href="https://www.elastic.co/docs/reference/text-analysis/analysis-lang-analyzer">ES CJK 分词</a> · <a href="https://www.elastic.co/docs/reference/elasticsearch/index-settings/similarity">BM25 默认配置</a> · <a href="https://www.elastic.co/docs/reference/query-languages/query-dsl/query-dsl-match-query">match 查询</a></p></details>
<div class="links"><a href="../''' + es_folder + '/' + raw_file + '''" download>ES 原始记录（下载）</a><a href="../''' + es_folder + '''/query-inputs.jsonl">500 条冻结查询与标注</a><a href="../''' + es_folder + '''/host-configuration.json">主机硬件与系统记录</a><a href="../''' + es_folder + '''/environment.json">ES 部署与资源配置</a><a href="../''' + es_folder + '''/verification.json">指标独立复算</a>''' + first_link + '''</div>
<script id="measured-series" type="application/json">''' + payload + '''</script><script>
const all=JSON.parse(document.getElementById('measured-series').textContent),colors=['#233442','#c24b37'];all.forEach((s,i)=>s.color=colors[i]);
function selected(){return all}
function axis(max){return [0,.25,.5,.75,1].map(t=>`<line x1="65" x2="550" y1="${225-t*190}" y2="${225-t*190}" stroke="#e1e8ed"/><text x="56" y="${230-t*190}" text-anchor="end">${max>=100?Math.round(t*max):(t*max).toFixed(2)}</text>`).join('')}
function quality(){const key=document.getElementById('quality-metric').value,items=selected(),step=480/items.length;document.getElementById('quality-plot').innerHTML=axis(1)+items.map((s,i)=>{const value=key==='empty_rate'?s.empty/s.samples:s.metrics[key],width=Math.min(68,step*.62),x=70+i*step+step/2;return `<g><title>${s.label} · ${key} ${value.toFixed(4)}</title><rect x="${x-width/2}" y="${225-value*190}" width="${width}" height="${value*190}" fill="${s.color}"/><text x="${x}" y="${Math.max(20,218-value*190)}" text-anchor="middle">${value.toFixed(3)}</text><text class="label" x="${x}" y="249" text-anchor="middle">${s.engine} ${s.parser}</text><text class="label" x="${x}" y="269" text-anchor="middle">${s.short||(s.algorithm==='tfidf'?'TF-IDF':'BM25')}</text></g>`}).join('')}
function lines(id,key){const items=selected(),max=Math.max(1,...items.flatMap(s=>s.profiles.map(p=>p[key])))*1.15,y=v=>225-v/max*190;let body=axis(max);
body+=[1,4,8].map((c,i)=>`<text x="${85+i*220}" y="253" text-anchor="middle">${c}</text>`).join('')+'<text x="300" y="277" text-anchor="middle">客户端并发</text>';items.forEach(s=>{const pts=s.profiles.map((p,i)=>[85+i*220,y(p[key]),p]);body+=`<polyline points="${pts.map(p=>p.slice(0,2).join(',')).join(' ')}" fill="none" stroke="${s.color}" stroke-width="3"/>`+pts.map(([x,v,p])=>`<circle cx="${x}" cy="${v}" r="5" fill="${s.color}"><title>${s.label} · 并发 ${p.effective_concurrency} · ${key} ${p[key].toFixed(2)}</title></circle>`).join('')});document.getElementById(id).innerHTML=body}
function draw(){const items=selected();document.getElementById('legend').innerHTML=items.map(s=>`<span><i style="background:${s.color}"></i>${s.label}</span>`).join('');quality();lines('qps-plot','qps');lines('latency-plot',document.getElementById('latency-metric').value)}
for(const id of ['quality-metric','latency-metric'])document.getElementById(id).addEventListener('change',draw);draw();
</script></main></body></html>'''


if __name__ == '__main__':
    cli = argparse.ArgumentParser(description=__doc__)
    cli.add_argument('--reports-root', type=Path, required=True)
    cli.add_argument('--es-report', required=True)
    args = cli.parse_args()
    body = render(args.reports_root, args.es_report)
    target = args.reports_root / 'datasets/t2ranking-es.html'
    temporary = None
    try:
        with tempfile.NamedTemporaryFile(mode='w', encoding='utf-8', dir=target.parent, prefix='.t2-es-', suffix='.html', delete=False) as stream:
            temporary = Path(stream.name)
            stream.write(body)
        os.replace(temporary, target)
    finally:
        if temporary is not None:
            temporary.unlink(missing_ok=True)
    print(f'Published {target}')
