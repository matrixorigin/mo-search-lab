"""Portal snapshots must stay independent from archived performance runs."""
import json
from pathlib import Path
import tempfile
import unittest

from render_report_tabs import publish, with_environment_tab


class ReportTabTests(unittest.TestCase):
    def fixture(self, root):
        inspection = root / "inspect"
        inspection.mkdir()
        record = {"run_kind": "environment_inspection", "environment": {"observed_at": "2026-10-08T09:30:58Z"}}
        (inspection / "report.json").write_text(json.dumps(record))
        (inspection / "environment.json").write_text(json.dumps(record["environment"]))
        (inspection / "report.html").write_text('''<style id="environment-style">.environment-panel{color:blue}</style>
<div id="environment-content"><div>prod snapshot <a href="environment.json">raw environment</a><a href="report.json">raw inspection</a></div></div>''')
        layout = root / "layout"
        layout.mkdir()
        (layout / "report.html").write_text('''<style id="report-tabs-style">.report-tabs{display:flex}</style>
<nav class="report-tabs" id="report-tabs-navigation">two tabs</nav>
<script id="report-tabs-controller">/* tab controls */</script>''')
        page = '<html><head><style>/* original chart style */</style></head><body><main><header>local performance</header><section id="single-concurrency">frozen chart</section><script>/* original chart controls */</script></main></body></html>'
        (root / "datasets").mkdir()
        for target in [root / "index.html", root / "datasets/gist1m.html", root / "datasets/t2ranking.html"]:
            target.write_text(page)
        return page

    def test_republication_keeps_chart_changes_and_raw_source_links(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            original = self.fixture(root)
            raw = {p: p.read_bytes() for p in (root / "inspect").glob("*.json")}
            rendered = with_environment_tab(original, root, "inspect", "layout")
            self.assertEqual(rendered, with_environment_tab(rendered, root, "inspect", "layout"))
            self.assertIn("独立环境检查", rendered)
            performance, environment = rendered.split('id="report-environment"', 1)
            self.assertIn("frozen chart", performance)
            self.assertNotIn("prod snapshot", performance)
            self.assertIn("prod snapshot", environment)
            rendered = rendered.replace("frozen chart", "updated chart")
            updated = with_environment_tab(rendered, root, "inspect", "layout")
            self.assertIn("updated chart", updated)
            publish(root, "inspect", "layout")
            self.assertIn('href="inspect/environment.json"', (root / "index.html").read_text())
            self.assertIn('href="../inspect/environment.json"', (root / "datasets/t2ranking.html").read_text())
            self.assertEqual(raw, {p: p.read_bytes() for p in raw})

    def test_invalid_inspection_does_not_replace_existing_pages(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.fixture(root)
            paths = [root / "index.html", root / "datasets/gist1m.html", root / "datasets/t2ranking.html"]
            original = {p: p.read_bytes() for p in paths}
            path = root / "inspect/report.json"
            record = json.loads(path.read_text())
            record["run_kind"] = "benchmark"
            path.write_text(json.dumps(record))
            with self.assertRaisesRegex(ValueError, "independent inspect"):
                publish(root, "inspect", "layout")
            self.assertEqual(original, {p: p.read_bytes() for p in original})


if __name__ == "__main__":
    unittest.main()
