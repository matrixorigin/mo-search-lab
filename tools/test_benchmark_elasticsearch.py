"""Small, independent measurement and HTTP failure contracts; no ES needed."""
import math
import hashlib
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch, MagicMock

import benchmark_elasticsearch as bench
import render_es_reference as portal


class ElasticsearchTests(unittest.TestCase):
    def test_quality_known_answer_empty_and_grade_boundary(self):
        scores = bench.quality(['b', 'a', 'zero'], {'a': 3, 'b': 2, 'c': 1, 'zero': 0})
        self.assertAlmostEqual(scores['ndcg_at_10'], (2 + 3 / math.log2(3)) / (3 + 2 / math.log2(3) + .5))
        self.assertEqual(scores['recall'], 1)
        self.assertEqual(scores['mrr_at_10'], 1)
        self.assertEqual(bench.quality(['c', 'b'], {'a': 3, 'b': 2, 'c': 1})['mrr_at_10'], .5)
        self.assertEqual(bench.quality([], {'a': 3}), dict(ndcg=0, ndcg_at_10=0, recall=0, mrr_at_10=0))
        with self.assertRaisesRegex(ValueError, 'duplicate'):
            bench.quality(['a', 'a'], {'a': 3})
        self.assertEqual(bench.percentile([100, 3, 2, 1], .95), 100)
        with self.assertRaisesRegex(ValueError, 'no successful'):
            bench.percentile([], .95)

    def test_partial_search_and_bulk_never_count_as_success(self):
        for response in [{'timed_out': True}, {'_shards': {'failed': 1}}]:
            with self.assertRaisesRegex(RuntimeError, 'partial'):
                bench.extract_hits(response)
        with patch.object(bench, 'Client') as client:
            client.return_value.__enter__.return_value.request.return_value = {'errors': False, 'items': []}
            with self.assertRaisesRegex(RuntimeError, 'bulk incomplete'):
                bench.bulk_chunk('http://localhost:9200', 'index', b'', 1)

    def test_reuse_requires_actual_owner_scoring_and_topology(self):
        mapping = {'dynamic': 'strict', '_meta': {'owner': 'mo-retrieval-bench', 'corpus_sha256': 'frozen'},
                   'properties': {'id': {'type': 'long'}, 'body': {'type': 'text', 'analyzer': 'cjk', 'similarity': 'BM25'}}}
        settings = {'index.number_of_shards': '1', 'index.number_of_replicas': '0'}
        bench.verify_existing_index(mapping, settings, 'frozen')
        for failure in ['owner', 'scoring', 'shards']:
            bad_mapping = json.loads(json.dumps(mapping))
            bad_settings = settings.copy()
            if failure == 'owner':
                bad_mapping['_meta']['owner'] = 'another-tool'
            elif failure == 'scoring':
                bad_mapping['properties']['body']['similarity'] = 'boolean'
            else:
                bad_settings['index.number_of_shards'] = '2'
            with self.assertRaisesRegex(ValueError, 'one-shard topology'):
                bench.verify_existing_index(bad_mapping, bad_settings, 'frozen')

    def test_http_timeout_closes_connection_without_retry(self):
        with patch.object(bench.http.client, 'HTTPConnection') as connection:
            connection.return_value.request.side_effect = TimeoutError('deadline')
            client = bench.Client('http://localhost:9200')
            with self.assertRaises(TimeoutError):
                client.request('GET', '/')
            connection.return_value.close.assert_called_once()
            connection.return_value.request.assert_called_once()
        with self.assertRaisesRegex(ValueError, 'local'):
            bench.Client('http://example.com:9200')

    def test_profile_failed_samples_are_preserved_and_workers_close(self):
        instances = []
        class FakeClient:
            def __init__(self, *args):
                self.closed = False
                instances.append(self)
            def request(self, method, *args):
                return {} if method == 'GET' else {'timed_out': True}
            def close(self):
                self.closed = True
            def __enter__(self):
                return self
            def __exit__(self, *args):
                self.close()
        queries = [{'id': str(i), 'params': {'anli_text': '词'}, 'relevance': {'a': 3}} for i in range(3)]
        with patch.object(bench, 'Client', FakeClient):
            result = bench.profile('http://localhost', 'index', queries, 'anli_text', 2, warmup=0)
        self.assertEqual(result['executions'], 3)
        self.assertEqual(result['sql_failures'], 3)
        self.assertEqual(result['sql_successes'], 0)
        self.assertNotIn('p95_ms', result)
        self.assertEqual([r['id'] for r in result['results']], ['0', '1', '2'])
        self.assertTrue(all(client.closed for client in instances))

    def test_stability_distinguishes_same_set_from_changed_order(self):
        client = MagicMock()
        client.request.side_effect = [dict(took=1, hits={'hits': [{'_id': value} for value in ids]})
                                      for ids in [['a', 'b'], ['b', 'a'], ['a', 'b']]]
        with patch.object(bench, 'Client') as factory:
            factory.return_value.__enter__.return_value = client
            result = bench.stability('http://localhost', 'index', [{'id': 'q', 'params': {'raw_text': '词'}}], 'raw_text', 3)
        self.assertEqual(result['stability'][0]['distinct_results'], 1)
        self.assertEqual(result['stability'][0]['distinct_orders'], 2)
        self.assertEqual([r['pass'] for r in result['results']], [True, False, True])


class ReferenceTests(unittest.TestCase):
    def test_publication_is_independent_of_mo_and_requires_complete_measured_profiles(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            es_dir = root / 'es'
            es_dir.mkdir()
            query_bytes = ''.join(json.dumps({'id': str(i)}) + '\n' for i in range(500)).encode()
            query_hash = hashlib.sha256(query_bytes).hexdigest()
            (es_dir / 'query-inputs.jsonl').write_bytes(query_bytes)
            (es_dir / 'environment.json').write_text(json.dumps({'cpu_limit': 8, 'memory_limit_bytes': 16*1024**3, 'node_count': 1}))
            query_ids = [str(i) for i in range(500)]
            results = [{'id': q, 'sql_succeeded': True, 'ids': ['a'], 'quality': dict(ndcg=1, ndcg_at_10=1, recall=1, mrr_at_10=1)} for q in query_ids]
            definition = {'settings': {'number_of_shards': 1, 'number_of_replicas': 0}, 'mappings': {'properties': {'body': {'type': 'text', 'analyzer': 'cjk', 'similarity': 'BM25'}}}}
            report = {'status': 'passed', 'indexed_rows': 2303643, 'inputs': [{'sha256': 'corpus', 'rows': 2303643}], 'queries_sha256': query_hash, 'query_ids': query_ids, 'index_definition': definition, 'profile': {'repeat': 1}, 'scenarios': []}
            for field in ['anli_text', 'raw_text']:
                for concurrency in [1, 4, 8]:
                    report['scenarios'].append({'id': f'es_cjk_{field}', 'effective_concurrency': concurrency, 'sql_failures': 0, 'sql_successes': 500, 'top_k': 100, 'ndcg_gain': 'linear', 'relevant_grade': 2, 'quality_mode': 'observe', 'qps': 1, 'p90_ms': 1, 'p95_ms': 1, 'p99_ms': 1, 'results': results})
                report['scenarios'].append({'id': f'es_cjk_{field}_stability', 'executions': 150, 'sql_failures': 0, 'check_order': True, 'allow_empty': True, 'results': [{}]*150})
            (es_dir / 'report.json').write_text(json.dumps(report))
            items, _, _ = portal.measured_series(root, 'es')
            self.assertEqual(len(items), 2)
            self.assertTrue(all(item['engine'] == 'ES' for item in items))
            self.assertEqual([p.name for p in root.iterdir()], ['es'])
            for failure in ['digest', 'profile', 'status', 'rows', 'cohort']:
                changed = json.loads(json.dumps(report))
                if failure == 'digest':
                    changed['queries_sha256'] = 'wrong'
                elif failure == 'profile':
                    changed['scenarios'].pop(0)
                elif failure == 'rows':
                    changed['indexed_rows'] = 2
                elif failure == 'cohort':
                    changed['query_ids'] = list(reversed(changed['query_ids']))
                else:
                    changed['status'] = 'failed'
                (es_dir / 'report.json').write_text(json.dumps(changed))
                with self.assertRaises(ValueError):
                    portal.measured_series(root, 'es')


if __name__ == '__main__':
    unittest.main()
