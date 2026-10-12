"""Public display aliases must preserve historical lookup and evidence links."""
import unittest

from presentation import neutral_application_page


class NeutralApplicationDisplayTests(unittest.TestCase):
    def test_names_change_without_breaking_urls_scripts_or_escaping(self):
        original = ('<p>ANLI &lt;script&gt; anli_sql_bm25</p>'
                    '<a href="../t2ranking-anli/report.json" title="anli 结果">记录</a>'
                    '<script>const id="anli_sql_bm25";if(1<2){}</script>'
                    '<style>.anli{color:red}</style>')
        page = neutral_application_page(original)
        for expected in ['<p>app &lt;script&gt; app_sql_bm25</p>',
                         'title="app 结果"', 'href="../t2ranking-anli/report.json"',
                         'const id="anli_sql_bm25"', '.anli{color:red}']:
            self.assertIn(expected, page)
        self.assertEqual(page, neutral_application_page(page))


if __name__ == "__main__":
    unittest.main()
