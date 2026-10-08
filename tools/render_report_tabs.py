#!/usr/bin/env python3
"""Add an independently collected environment inspection tab to saved portals."""
import argparse
from datetime import datetime, timezone
import html
from html.parser import HTMLParser
import json
import os
from pathlib import Path
import tempfile


def fragment(page: str, start: str, end: str) -> str:
    if page.count(start) != 1 or page.count(end) != 1:
        raise ValueError(f"expected one rendered fragment: {start}")
    return page.split(start, 1)[1].split(end, 1)[0]


def element(page: str, tag: str, identifier: str, inner: bool = False) -> str:
    # Go's html/template removes source comments. Reuse actual rendered
    # elements by stable IDs, including nested environment tables/disclosures.
    class Extractor(HTMLParser):
        def __init__(self):
            super().__init__(convert_charrefs=False)
            self.offsets = [0]
            for line in page.splitlines(keepends=True):
                self.offsets.append(self.offsets[-1] + len(line))
            self.depth = 0
            self.start = None
            self.matches = []

        def position_offset(self):
            line, column = self.getpos()
            return self.offsets[line - 1] + column

        def handle_starttag(self, name, attrs):
            if name != tag:
                return
            if self.depth:
                self.depth += 1
            elif dict(attrs).get("id") == identifier:
                self.start = self.position_offset()
                self.depth = 1

        def handle_endtag(self, name):
            if name == tag and self.depth:
                self.depth -= 1
                if not self.depth:
                    self.matches.append(page[self.start:self.position_offset() + len(name) + 3])

    parser = Extractor()
    parser.feed(page)
    if len(parser.matches) != 1:
        raise ValueError(f"expected one rendered element: {identifier}")
    value = parser.matches[0]
    return value.split(">", 1)[1].rsplit("</", 1)[0] if inner else value


def with_environment_tab(page: str, root: Path, inspection: str, layout: str) -> str:
    # This is an off-site publisher. It reuses the binary's rendered view and
    # tab controls; it never invents evidence or attaches a snapshot to a run.
    directory = (root / inspection).resolve()
    relative = directory.relative_to(root.resolve()).as_posix()
    report = json.loads((directory / "report.json").read_text())
    if report.get("run_kind") != "environment_inspection" or not report.get("environment"):
        raise ValueError("environment tab requires an independent inspect report")
    inspection_page = (directory / "report.html").read_text()
    layout_page = (root / layout / "report.html").read_text()
    nav = element(layout_page, "nav", "report-tabs-navigation")
    script = element(layout_page, "script", "report-tabs-controller")
    css = element(layout_page, "style", "report-tabs-style", inner=True)
    css += element(inspection_page, "style", "environment-style", inner=True)
    environment = element(inspection_page, "div", "environment-content")
    for name in ("report.json", "environment.json"):
        environment = environment.replace(f'href="{name}"', f'href="../{html.escape(relative, quote=True)}/{name}"')
    observed = report["environment"].get("observed_at") or report["started_at"]
    date = datetime.fromisoformat(observed.replace("Z", "+00:00")).astimezone(timezone.utc)
    context = (f'<p class="environment-tab-context"><strong>独立环境检查</strong> · '
               f'{date:%Y-%m-%d %H:%M UTC}<br>'
               '此快照来自单独的 inspect 运行；性能图表的测量环境以各测试记录为准。</p>')
    # Managed boundaries keep repeated publication from nesting tabs or losing
    # chart changes made by the existing portal preparation scripts.
    if "<!-- portal-performance:start -->" in page:
        performance = fragment(page, "<!-- portal-performance:start -->", "<!-- portal-performance:end -->")
    else:
        performance = fragment(page, "</header>", "</main>")
    if page.count("<main>") != 1 or page.count("</header>") != 1 or page.count("</main>") != 1:
        raise ValueError("portal must contain one main region and header")
    head, rest = page.split("<main>", 1)
    header = rest.split("</header>", 1)[0] + "</header>"
    if "<!-- portal-tab-style:start -->" in head:
        previous = "<!-- portal-tab-style:start -->" + fragment(head, "<!-- portal-tab-style:start -->", "<!-- portal-tab-style:end -->") + "<!-- portal-tab-style:end -->"
        head = head.replace(previous, "")
    if head.count("</head>") != 1:
        raise ValueError("portal must contain one head region")
    head = head.replace("</head>", f'<!-- portal-tab-style:start --><style>{css}</style><!-- portal-tab-style:end --></head>')
    return (head + "<main>" + header + nav +
            '<section id="report-performance" role="tabpanel" aria-labelledby="tab-performance" data-report-panel>' +
            "<!-- portal-performance:start -->" + performance + "<!-- portal-performance:end --></section>" +
            '<section id="report-environment" role="tabpanel" aria-labelledby="tab-environment" data-report-panel>' +
            '<section class="environment-panel" id="environment" aria-labelledby="environment-tab-title">'
            '<h2 class="environment-tab-title" id="environment-tab-title">运行环境与检索配置</h2>' +
            context + environment + "</section></section>" + script + "</main></body></html>")


def publish(root: Path, inspection: str, layout: str) -> None:
    targets = [root / "index.html", root / "datasets/gist1m.html", root / "datasets/t2ranking.html"]
    # Prepare every page before replacing any page.
    pages = [(target, with_environment_tab(target.read_text(), root, inspection, layout)) for target in targets]
    for target, body in pages:
        # The index is one directory above the dataset pages.
        if target.parent == root:
            directory = (root / inspection).resolve().relative_to(root.resolve()).as_posix()
            for name in ("report.json", "environment.json"):
                body = body.replace(f'href="../{html.escape(directory, quote=True)}/{name}"',
                                    f'href="{html.escape(directory, quote=True)}/{name}"')
        temporary = None
        try:
            with tempfile.NamedTemporaryFile(mode="w", encoding="utf-8", dir=target.parent,
                                             prefix=".report-tabs-", suffix=".html", delete=False) as stream:
                temporary = Path(stream.name)
                stream.write(body)
            os.replace(temporary, target)
        finally:
            if temporary is not None:
                temporary.unlink(missing_ok=True)
        print(f"Updated {target}")


if __name__ == "__main__":
    cli = argparse.ArgumentParser(description=__doc__)
    cli.add_argument("--reports-root", type=Path, required=True)
    cli.add_argument("--environment-report", required=True)
    cli.add_argument("--layout-report", required=True, help="benchmark report rendered by the current binary")
    args = cli.parse_args()
    publish(args.reports_root, args.environment_report, args.layout_report)
