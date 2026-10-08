#!/usr/bin/env python3
"""Add saved database-filter and concurrent-SQL charts to dataset portals."""
import argparse
import html
import json
import math
import os
from pathlib import Path
import re
import tempfile


CSS = '''.database-panel{border-top:3px solid #193b55;margin-top:30px}.database-panel>h2{margin:0 0 8px;font-size:24px}.database-controls{display:flex;gap:12px 24px;flex-wrap:wrap;margin:18px 0}.database-controls label{display:flex;align-items:center;gap:8px;font-size:13px}.database-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:24px}.database-plot{min-width:0;border-top:1px solid #e1e8ed;padding-top:14px}.database-plot h3{font-size:17px;margin:0 0 12px}.database-svg{display:block;width:100%;min-width:420px}.database-svg text{fill:#54657a;font:15px ui-monospace,Consolas,monospace}.database-svg .filter-bar-value{font-size:14px}.database-legend{display:flex;gap:20px;margin:12px 0;font-size:13px}.database-legend span{display:flex;align-items:center;gap:7px}.database-legend i{width:12px;height:3px}.database-panel details{margin:18px 0}.database-panel summary{cursor:pointer;font-weight:600}.database-panel pre{font-size:12px;max-height:280px;overflow:auto}.database-panel .chart-scroll{overflow-x:auto}.database-panel table{font-size:13px;white-space:nowrap}.database-links{display:flex;gap:12px 20px;flex-wrap:wrap;font-size:13px;margin:16px 0}@media(max-width:760px){.database-svg{min-width:0}.database-svg text{font-size:20px}.database-grid{grid-template-columns:1fr}.database-panel>h2{font-size:21px}.database-controls{display:block}.database-controls label{margin:10px 0}}
'''
CSS += '\n@media(max-width:760px){#database-filters .database-svg{min-width:440px}#database-filters .filter-bar-value{font-size:16px}}\n'


def saved(root: Path, folder: str):
    report = json.loads((root / folder / 'report.json').read_text())
    status = json.loads((root / folder / 'run-status.json').read_text())
    if not status.get('complete') or report['cleanup'] not in ['dropped', 'retained'] or any(s.get('error') for s in report['stages']):
        raise ValueError('measurement has not completed preparation/cleanup')
    for s in report['scenarios']:
        if s.get('error') or len(s['results']) != s['selected_queries'] * s['repetitions']:
            raise ValueError('incomplete scenario; inspect raw failed report')
        if s['oracle'] != 'stable_multiset' and not s['sql_successes']:
            raise ValueError('no successful SQL samples')
    return report


def point(scene):
    good = [r for r in scene['results'] if r['sql_succeeded']]
    values = {key: scene[key] for key in ['effective_concurrency','qps','p90_ms','p95_ms','p99_ms','mean_score','sql_successes','sql_failures']}
    values['mean_returned'] = sum(len(r.get('ids', [])) for r in good) / len(good)
    values['full_result_rate'] = sum(len(r.get('ids', [])) == scene['top_k'] for r in good) / len(good)
    if any(not math.isfinite(v) or v < 0 for v in values.values()):
        raise ValueError('invalid chart measurements')
    return values


def plots(prefix, names):
    return '<div class="database-grid">' + ''.join(f'<article class="database-plot"><h3>{html.escape(label)}</h3><div class="chart-scroll"><svg id="{prefix}-{key}" class="database-svg" viewBox="0 0 560 270" role="img" aria-label="{html.escape(label)}"></svg></div></article>' for key,label in names) + '</div>'


def script(prefix, payload, body, serial=False):
    data = json.dumps(payload, ensure_ascii=False).replace('<', '\\u003c').replace('&', '\\u0026')
    draw = r'''function draw(id,series,key,fixed){const maximum=fixed||Math.max(1,...series.flatMap(s=>s.points.map(p=>p[key])))*1.15;let svg=[0,.25,.5,.75,1].map(t=>`<line x1="75" x2="525" y1="${215-t*180}" y2="${215-t*180}" stroke="#e1e8ed"/><text x="65" y="${220-t*180}" text-anchor="end">${(t*maximum).toFixed(maximum>10?0:2)}</text>`).join('');svg+=[1,4,8].map((c,i)=>`<text x="${85+i*215}" y="242" text-anchor="middle">${c}</text>`).join('')+'<text x="300" y="264" text-anchor="middle">并发档位</text>';series.forEach((s,i)=>{const points=s.points.map((p,j)=>[85+j*215,215-p[key]/maximum*180,p]);svg+=`<polyline points="${points.map(p=>p.slice(0,2).join(',')).join(' ')}" fill="none" stroke="${colors[i]}" stroke-width="3"/>`;svg+=points.map(([x,y,p])=>`<circle cx="${x}" cy="${y}" r="5" fill="${colors[i]}" data-metric="${key}" data-value="${p[key]}"><title>${s.label} · 并发 ${p.effective_concurrency} · ${key} ${p[key].toFixed(4)} · 成功样本 ${p.sql_successes} · SQL 错误 ${p.sql_failures}</title></circle>`).join('')});document.getElementById(id).innerHTML=svg}'''
    if serial:
        draw = r'''function draw(id,series,key,fixed){const maximum=fixed||Math.max(1,...series.map(s=>s.points[0][key]))*1.15;let svg=[0,.25,.5,.75,1].map(t=>`<line x1="75" x2="525" y1="${215-t*180}" y2="${215-t*180}" stroke="#e1e8ed"/><text x="65" y="${220-t*180}" text-anchor="end">${(t*maximum).toFixed(maximum>10?0:2)}</text>`).join('');[100,10,1].forEach((percent,index)=>{const center=150+index*150;svg+=`<text x="${center}" y="242" text-anchor="middle">${percent}%</text>`;['pre','post'].forEach((mode,i)=>{const s=series.find(s=>s.visible_percent===percent&&s.mode===mode),p=s.points[0],value=p[key],height=value/maximum*180,x=center+(i===0?-45:5),label=key==='mean_score'?value.toFixed(3):value.toFixed(value>=100?0:1);svg+=`<g><title>${s.label} · 可见 ${percent}% · 串行 · ${key} ${value.toFixed(4)} · 成功样本 ${p.sql_successes} · SQL 错误 ${p.sql_failures}</title><rect x="${x}" y="${215-height}" width="40" height="${height}" rx="2" fill="${colors[i]}" data-mode="${mode}" data-percent="${percent}" data-metric="${key}" data-value="${value}"/><text class="filter-bar-value" x="${x+20}" y="${208-height}" text-anchor="middle">${label}</text></g>`})});svg+='<text x="300" y="264" text-anchor="middle">可见数据比例</text>';document.getElementById(id).innerHTML=svg}'''
    return (f'<script id="{prefix}-measurements" type="application/json">{data}</script><script>(()=>{{\n'
            f"const root=document.getElementById('{prefix}'),data=JSON.parse(document.getElementById('{prefix}-measurements').textContent),colors=['#087f8c','#b96a23'];\n"
            + draw + '\n' + body + '\n})();</script>')


def links(folder, label):
    return f'<div class="database-links"><a href="../{folder}/report.html">{label}完整报告</a><a href="../{folder}/report.json">逐查询原始记录</a><a href="../{folder}/source.json">数据与场景配置</a><a href="../{folder}/pack-manifest.json">数据包清单</a><a href="../{folder}/verification.json">独立复算记录</a><a href="../{folder}/environment.json">部署与测量环境</a></div>'


def filter_panel(root: Path, folder: str):
    report = saved(root, folder)
    source = json.loads((root / folder / 'source.json').read_text())
    cases = []
    rows, details = [], []
    if {(c['mode'],c['visible_percent']) for c in source['cases']} != {(mode,p) for mode in ['pre','post'] for p in [100,10,1]} or len(source['cases']) != 6:
        raise ValueError('filtered chart requires exactly the six PRE/POST visibility cases')
    for meta in source['cases']:
        scenes = [s for s in report['scenarios'] if s['id'] == meta['id'] and s['effective_concurrency'] == 1]
        if len(scenes) != 1 or any(s['top_k'] != 100 or s.get('quality_mode') != 'observe' or not s.get('physical_plans') or s['selected_queries'] != source['query_count'] or s['repetitions'] != 1 for s in scenes):
            raise ValueError('filtered chart needs one complete serial measurement/plan per case')
        first = scenes[0]
        if any('ivf_search' not in s['plan'] or s['sql'] != first['sql'] for s in scenes):
            raise ValueError('vector index evidence is absent')
        cases.append({**meta,'label':meta['mode'].upper(),'points':[point(s) for s in scenes]})
        for s in scenes:
            p = point(s)
            rows.append(f'<tr><td>{meta["mode"].upper()}</td><td>{meta["visible_percent"]}%</td><td>{p["mean_score"]:.4f}</td><td>{p["mean_returned"]:.1f}</td><td>{p["qps"]:.2f}</td><td>{p["p90_ms"]:.2f}</td><td>{p["p95_ms"]:.2f}</td><td>{p["p99_ms"]:.2f}</td></tr>')
        details.append(f'<details><summary>{meta["mode"].upper()} · 可见 {meta["visible_percent"]}%：SQL 与执行计划</summary><p><a href="../{folder}/{meta["id"]}.jsonl">固定查询与过滤真值</a></p><pre>{html.escape(first["sql"])}</pre><pre>{html.escape(first["plan"])}</pre><pre>{html.escape(next(iter(first["physical_plans"].values())))}</pre></details>')
    stability = [s for s in report['scenarios'] if s['oracle']=='stable_multiset']
    checked = sum(s['executions'] for s in stability)
    failed = sum(s['failures'] for s in stability)
    tie_note = ''
    tie_file = root / folder / 'stability-tie-analysis.json'
    if tie_file.is_file():
        analysis = json.loads(tie_file.read_text())
        if analysis['order_changed_executions'] == failed and analysis['all_result_sets_unchanged'] and analysis['all_order_changes_between_identical_vectors']:
            tie_note = f'<p class="chart-note">这 {failed} 次顺序变化全部发生在完全相同的向量之间，结果集合未变。SQL 只按距离排序，同距结果没有指定 ID 顺序；严格顺序检查保留为未通过。<a href="../{folder}/stability-tie-analysis.json">查看逐次同距核对记录</a>。</p>'
    fragment = re.search(r'<section id="stability".*?</section>', (root / folder / 'report.html').read_text(), re.S)
    stable_html = fragment.group().replace('id="stability"','id="filtered-stability"').replace('href="report.json"',f'href="../{folder}/report.json"') if fragment else ''
    filter_script = script('database-filters', cases, "function update(){draw('filters-recall',data,'mean_score',1);draw('filters-returned',data,'mean_returned',100);draw('filters-qps',data,'qps');draw('filters-latency',data,root.querySelector('#filter-latency').value)}root.querySelector('#filter-latency').addEventListener('change',update);update();", serial=True)
    return f'''<section id="database-filters" class="card database-panel"><h2>过滤条件下的向量检索</h2><p class="chart-note">GIST1M · 100 万向量 · 同一批 {source['query_count']} 条查询 · Top-100 · 1,000 lists · 100 probes · 单并发串行。可见数据为固定前缀分区，权限字段是合成数据；真值在各自可见范围内重新精确计算。</p><div class="database-controls"><label>延迟分位数 <select id="filter-latency"><option value="p90_ms">P90</option><option value="p95_ms" selected>P95</option><option value="p99_ms">P99</option></select></label></div><div class="database-legend"><span><i style="background:#087f8c"></i>PRE</span><span><i style="background:#b96a23"></i>POST</span></div><p class="mobile-scroll-note">图表可左右滑动查看。</p>{plots('filters',[('recall','Recall@100 · 越高越好'),('returned','平均返回数量 · 最多 100 条'),('qps','串行吞吐 · SQL / 秒'),('latency','SQL 往返延迟 · 毫秒')])}<p class="chart-note">返回数量、召回和延迟需要一起看；POST 在有限候选中执行过滤，可能返回不足 100 条。重复检查 {checked} 次，未通过 {failed} 次；集合和顺序均检查，稳定不代表召回充分。浮点精度及同距边界可能影响精确 ID 集合，本次真值口径见配置。</p>{tie_note}<details><summary>完整数值</summary><div class="chart-scroll"><table><tr><th>模式</th><th>可见比例</th><th>Recall@100</th><th>平均返回</th><th>QPS</th><th>P90 ms</th><th>P95 ms</th><th>P99 ms</th></tr>{''.join(rows)}</table></div></details><details><summary>SQL 与正常／物理执行计划</summary>{''.join(details)}</details><details><summary>过滤场景重复稳定性图表</summary>{stable_html}</details>{links(folder,'过滤测试')}{filter_script}</section>'''


def mixed_panel(root: Path, folder: str):
    report = saved(root, folder)
    series, plans = [], []
    for id,label in [('vector','向量 SQL'),('fulltext','全文 SQL')]:
        for mixed in [False,True]:
            scenes = sorted([s for s in report['scenarios'] if s['id']==id+('_mixed' if mixed else '')],key=lambda s:s['effective_concurrency'])
            if [s['effective_concurrency'] for s in scenes] != [1,4,8] or any(s['top_k']!=100 or (mixed and s.get('execution_mode')!='mixed') for s in scenes):
                raise ValueError('mixed chart needs isolated/concurrent measured profiles')
            series.append({'route':id,'label':'两路同时运行' if mixed else '单路独立运行','points':[point(s) for s in scenes]})
            if not mixed:
                first=scenes[0]
                plans.append(f'<details><summary>{label}：SQL、固定输入与执行计划</summary><p><a href="../{folder}/{id}.jsonl">固定查询与真值／人工标注</a></p><pre>{html.escape(first["sql"])}</pre><pre>{html.escape(first["plan"])}</pre><pre>{html.escape(next(iter(first["physical_plans"].values())))}</pre></details>')
            if mixed:
                baseline=[s for s in report['scenarios'] if s['id']==id]
                if any(s['sql']!=baseline[0]['sql'] or s['queries_sha256']!=baseline[0]['queries_sha256'] or s.get('session_sql')!=baseline[0].get('session_sql') for s in scenes):
                    raise ValueError('mixed/isolated query identity differs')
    return f'''<section id="database-workloads" class="card database-panel"><h2>向量与全文 SQL 同时运行</h2><p class="chart-note">GIST1M 向量表＋完整 T2Ranking 文本表 · 每路 {report['scenarios'][0]['selected_queries']} 条固定查询 · Top-100。分别单跑，再在同一 MO 服务上同时运行；两路结果独立保存。此处测通用资源竞争，使用两张表。</p><div class="database-controls"><label>延迟分位数 <select id="workload-latency"><option value="p90_ms">P90</option><option value="p95_ms" selected>P95</option><option value="p99_ms">P99</option></select></label></div><div class="database-legend"><span><i style="background:#087f8c"></i>单路独立运行</span><span><i style="background:#b96a23"></i>两路同时运行</span></div>{plots('workloads',[('vector-latency','向量 SQL · 延迟 / 毫秒'),('fulltext-latency','全文 SQL · 延迟 / 毫秒'),('vector-qps','向量 SQL · 完成数 / 秒'),('fulltext-qps','全文 SQL · 完成数 / 秒')])}<p class="chart-note">并发 C 时，单跑最多 C 条 SQL；同时运行最多 C 对、2C 条 SQL。每个作业等待两路返回后才进入下一对，同时运行的各路 QPS 使用同一批次时长，表示配对负载吞吐。质量计算在计时之后；预热与执行计划不计入。未执行客户端融合或后过滤。</p><details><summary>两路数据库 SQL 与执行计划</summary>{''.join(plans)}</details>{links(folder,'两路 SQL 测试')}{script('database-workloads',series,"function update(){for(const route of ['vector','fulltext']){const s=data.filter(c=>c.route===route);draw('workloads-'+route+'-latency',s,root.querySelector('#workload-latency').value);draw('workloads-'+route+'-qps',s,'qps')}}root.querySelector('#workload-latency').addEventListener('change',update);update();")}</section>'''


def publish_page(target, name, fragment):
    page = target.read_text()
    start,end = f'<!-- {name}-start -->',f'<!-- {name}-end -->'
    page = re.sub(re.escape(start)+'.*?'+re.escape(end)+'\n?', '', page, flags=re.S)
    style = '<style id="database-chart-style">'+CSS+'</style>'
    page = re.sub(r'<style id="database-chart-style">.*?</style>','',page,flags=re.S)
    if name=='database-filters':
        anchor = '<div class="summary-alert">'
    else:
        anchor = '<details id="quality-metric-help"'
    if anchor not in page:
        raise ValueError('portal insertion position is missing')
    page = page.replace('</head>',style+'</head>')
    page = page.replace(anchor,start+fragment+end+'\n'+anchor,1)
    temporary = None
    try:
        with tempfile.NamedTemporaryFile(mode='w',encoding='utf-8',dir=target.parent,delete=False) as stream:
            temporary=Path(stream.name);stream.write(page)
        os.replace(temporary,target)
    finally:
        if temporary is not None:temporary.unlink(missing_ok=True)


if __name__=='__main__':
    cli=argparse.ArgumentParser(description=__doc__)
    cli.add_argument('--reports-root',type=Path,required=True)
    cli.add_argument('--filtered-report')
    cli.add_argument('--mixed-report')
    args=cli.parse_args()
    if args.filtered_report:
        panel=filter_panel(args.reports_root,args.filtered_report)
        for name in ['datasets/gist1m.html','index.html']:
            value=panel if name.startswith('datasets/') else panel.replace('href="../','href="')
            publish_page(args.reports_root/name,'database-filters',value)
    if args.mixed_report:
        panel=mixed_panel(args.reports_root,args.mixed_report)
        (args.reports_root/'datasets/database-workloads-fragment.html').write_text('<style id="database-chart-style">'+CSS+'</style><!-- database-workloads-start -->'+panel+'<!-- database-workloads-end -->')
        publish_page(args.reports_root/'datasets/t2ranking.html','database-workloads',panel)
    print('Published saved database SQL measurement charts')
