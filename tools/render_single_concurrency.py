#!/usr/bin/env python3
"""Render saved concurrency-1 latency; update only the existing GIST portals."""
import argparse
import html
import json
import math
import os
from pathlib import Path
import re
import tempfile


CHART_CSS = """
.single-concurrency{border-top:3px solid #193b55}.single-heading{display:flex;align-items:center;justify-content:space-between;gap:12px;flex-wrap:wrap}.single-heading h2{font-size:22px;margin:0 0 5px}.single-legend{display:flex;gap:16px;font-size:13px}.single-legend span{display:flex;align-items:center;gap:7px}.single-legend i{width:10px;height:10px;border-radius:2px;background:var(--percentile-color)}.single-row,.single-axis{display:grid;grid-template-columns:180px minmax(0,1fr);gap:20px;align-items:center}.single-row{padding:14px 0;border-bottom:1px solid #e4ebf1}.single-label{display:flex;flex-direction:column;gap:5px}.single-label strong{font-size:15px}.single-label small{font-size:12px}.single-scroll{overflow-x:auto;min-width:0}.single-svg{display:block;width:100%;min-width:440px}.single-svg text{font:14px ui-monospace,Consolas,monospace;fill:#54657a;font-variant-numeric:tabular-nums}.single-svg .single-value{fill:#213442}.single-grid{stroke:#e1e8ed;stroke-dasharray:3 4}.single-bar:hover{opacity:.8}.single-axis{padding-top:10px}.single-axis>span{font-size:12px;color:#5c6c7d}
@media(max-width:760px){.single-row,.single-axis{grid-template-columns:minmax(0,1fr);gap:8px}.single-heading h2{font-size:20px}.single-concurrency{padding:16px}.single-legend{gap:14px}}
"""
PERCENTILES = (("p90_ms", "P90", "#087f8c"),
               ("p95_ms", "P95", "#315eb4"),
               ("p99_ms", "P99", "#b96a23"))


def render_chart(configurations: list) -> str:
    if not configurations:
        raise ValueError("no concurrency-1 measurements")
    for item in configurations:
        profile = item["profile"]
        if profile["effective_concurrency"] != 1 or profile["sql_successes"] <= 0:
            raise ValueError("single-concurrency chart needs successful concurrency-1 samples")
        values = [profile[key] for key, _, _ in PERCENTILES]
        if any(not math.isfinite(value) or value < 0 for value in values) or values != sorted(values):
            raise ValueError("invalid measured latency quantiles")
    largest = max(item["profile"]["p99_ms"] for item in configurations)
    target = max(1, largest) * 1.08
    magnitude = 10 ** math.floor(math.log10(target))
    maximum = math.ceil(target / magnitude * 2) / 2 * magnitude
    legend = "".join(f'<span><i style="--percentile-color:{color}"></i>{name}</span>'
                     for _, name, color in PERCENTILES)
    pieces = [f'<section id="single-concurrency" class="card single-concurrency">'
              f'<div class="single-heading"><h2>单并发查询延迟</h2>'
              f'<div class="single-legend" aria-label="延迟分位数">{legend}</div></div>'
              '<p class="chart-note">客户端并发 1 · SQL 往返延迟 · 毫秒，越低越好。'
              '各配置使用同一刻度；悬停柱子查看分位数和成功样本数。</p>']
    grid = "".join(f'<line class="single-grid" x1="{i*164}" x2="{i*164}" y1="0" y2="84"/>'
                   for i in range(5))
    for item in configurations:
        profile = item["profile"]
        label = html.escape(item["label"])
        pieces.append(f'<div class="single-row"><div class="single-label"><strong>{label}</strong>'
                      f'<small>成功样本 {profile["sql_successes"]} · SQL 错误 {profile["sql_failures"]}</small></div>'
                      f'<div class="single-scroll"><svg class="single-svg" viewBox="0 0 800 84" '
                      f'role="img" aria-label="{label}，并发 1 的 P90、P95、P99 延迟"><title>{label} · 毫秒</title>{grid}')
        for index, (key, name, color) in enumerate(PERCENTILES):
            value = profile[key]
            width = value / maximum * 656
            y = 5 + index * 27
            note = f'{label} · 并发 1 · {name} {value:.3f} ms · 成功样本 {profile["sql_successes"]}'
            pieces.append(f'<g><title>{note}</title><rect class="single-bar" x="0" y="{y}" '
                          f'width="{width:.9f}" height="18" rx="2" fill="{color}" '
                          f'data-config="{label}" data-metric="{key}" data-value="{value:.12g}"/>'
                          f'<text class="single-value" x="{width+8:.9f}" y="{y+14}">{value:.1f} ms</text></g>')
        pieces.append('</svg></div></div>')
    ticks = "".join(f'<text x="{i*164}" y="18" text-anchor="{"start" if i==0 else "middle"}">{maximum*i/4:g}</text>'
                    for i in range(5))
    pieces.append(f'<div class="single-axis"><span>延迟 / 毫秒</span><div class="single-scroll">'
                  f'<svg class="single-svg" viewBox="0 0 800 26" aria-hidden="true" '
                  f'data-axis-max="{maximum:g}">{ticks}</svg></div></div></section>')
    return "".join(pieces)


def update_gist(root: Path, folder: str) -> None:
    report = json.loads((root / folder / "report.json").read_text())
    baseline = [scene for scene in report["scenarios"]
                if scene["oracle"] == "ann_recall" and scene["effective_concurrency"] == 1]
    labels = {"vector": "默认探测参数", "vector_nprobe_20": "20 probes", "vector_nprobe_100": "100 probes"}
    if len(baseline) != 3 or {scene["id"] for scene in baseline} != labels.keys():
        raise ValueError("GIST portal requires the measured default/20/100 profiles")
    if len({(scene["queries_sha256"], scene["selected_queries"], scene["top_k"]) for scene in baseline}) != 1:
        raise ValueError("GIST baselines use different query cohorts or Top-K")
    chart = render_chart([{"label": labels[scene["id"]], "profile": scene}
                          for scene in sorted(baseline, key=lambda scene: list(labels).index(scene["id"]))])
    for target in (root / "datasets/gist1m.html", root / "index.html"):
        page = target.read_text()
        if '<title>GIST1M' not in page or f'{folder}/report.json' not in page:
            raise ValueError(f"{target} does not identify the selected GIST measurement")
        page = re.sub(r'<section id="single-concurrency".*?</section>\n?', '', page, flags=re.S)
        page = re.sub(r'<style id="single-concurrency-style">.*?</style>', '', page, flags=re.S)
        anchor = '<section class="card overview" id="overview">'
        if page.count(anchor) != 1 or page.count('</head>') != 1:
            raise ValueError(f"{target} has no unique chart insertion position")
        page = page.replace('</head>', f'<style id="single-concurrency-style">{CHART_CSS}</style></head>')
        page = page.replace(anchor, chart + '\n' + anchor)
        temporary = None
        try:
            with tempfile.NamedTemporaryFile(mode="w", encoding="utf-8", dir=target.parent,
                                             prefix=".gist-single-", suffix=".html", delete=False) as stream:
                temporary = Path(stream.name)
                stream.write(page)
            os.replace(temporary, target)
        finally:
            if temporary is not None:
                temporary.unlink(missing_ok=True)
        print(f"Updated {target}")


if __name__ == "__main__":
    cli = argparse.ArgumentParser(description=__doc__)
    cli.add_argument("--reports-root", type=Path, required=True)
    cli.add_argument("--gist-report", required=True)
    args = cli.parse_args()
    update_gist(args.reports_root, args.gist_report)
