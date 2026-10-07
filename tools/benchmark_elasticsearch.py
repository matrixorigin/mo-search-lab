#!/usr/bin/env python3
"""Off-site, standard-library ES comparison. Does not change the field binary."""
import argparse
from collections import Counter
from concurrent.futures import FIRST_COMPLETED, ThreadPoolExecutor, wait
import csv
from datetime import datetime, timezone
import hashlib
import http.client
import json
import math
from pathlib import Path
import queue
import re
import shutil
import sys
import threading
import time
from urllib.parse import urlsplit
import uuid


def digest(path):
    h = hashlib.sha256()
    with Path(path).open('rb') as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b''):
            h.update(chunk)
    return h.hexdigest()


def utc():
    return datetime.now(timezone.utc).isoformat()


def save(path, value):
    temp = path.with_suffix(path.suffix + '.tmp')
    temp.write_text(json.dumps(value, ensure_ascii=False, indent=2) + '\n')
    temp.replace(path)


class Client:
    def __init__(self, endpoint, timeout=180):
        url = urlsplit(endpoint)
        if url.scheme != 'http' or url.hostname not in ('127.0.0.1', 'localhost') or url.path not in ('', '/') or url.username or url.password:
            raise ValueError('This experimental runner accepts only a local, unauthenticated HTTP fixture')
        self.connection = http.client.HTTPConnection(url.hostname, url.port or 9200, timeout=timeout)

    def request(self, method, path, value=None, raw=None):
        body = raw if raw is not None else (json.dumps(value, ensure_ascii=False).encode() if value is not None else None)
        content = 'application/x-ndjson' if raw is not None else 'application/json'
        try:
            self.connection.request(method, path, body=body, headers={'Content-Type': content})
            response = self.connection.getresponse()
            data = response.read()
            parsed = json.loads(data)
            if response.status >= 300:
                raise RuntimeError(f'HTTP {response.status}: {str(parsed)[:1000]}')
            return parsed
        except BaseException:
            self.close()
            raise

    def close(self):
        self.connection.close()

    def __enter__(self):
        return self

    def __exit__(self, *args):
        self.close()


def quality(ids, relevance):
    if len(ids) != len(set(ids)):
        raise ValueError('duplicate returned document IDs')
    grades = {str(k): int(v) for k, v in relevance.items()}
    ideal = sorted(grades.values(), reverse=True)
    def ndcg(k):
        actual = sum(grades.get(v, 0) / math.log2(i + 2) for i, v in enumerate(ids[:k]))
        maximum = sum(v / math.log2(i + 2) for i, v in enumerate(ideal[:k]))
        return actual / maximum if maximum else 0.0
    positives = {k for k, v in grades.items() if v >= 2}
    hits = set(ids[:100]) & positives
    return {'ndcg': ndcg(100), 'ndcg_at_10': ndcg(10),
            'recall': len(hits) / len(positives) if positives else 0.0,
            'mrr_at_10': next((1 / (i + 1) for i, v in enumerate(ids[:10]) if v in positives), 0.0)}


def percentile(values, fraction):
    if not values:
        raise ValueError('no successful latency samples')
    return sorted(values)[math.ceil(len(values) * fraction) - 1]


def search_body(text):
    return {'size': 100, '_source': False, 'track_total_hits': False,
            'min_score': 0.01, 'query': {'match': {'body': {'query': text, 'operator': 'or'}}},
            'sort': [{'_score': 'desc'}, {'id': 'asc'}]}


def extract_hits(response):
    if response.get('timed_out') or response.get('_shards', {}).get('failed', 0):
        raise RuntimeError(f'partial/timed-out search: {str(response)[:500]}')
    ids = [str(hit['_id']) for hit in response['hits']['hits']]
    if len(ids) > 100 or len(ids) != len(set(ids)):
        raise ValueError('invalid Top-100 ID result')
    return ids


def verify_existing_index(mapping, settings, corpus_hash):
    expected = {'id': {'type': 'long'}, 'body': {'type': 'text', 'analyzer': 'cjk', 'similarity': 'BM25'}}
    metadata = mapping.get('_meta', {})
    if (metadata.get('owner') != 'mo-retrieval-bench' or metadata.get('corpus_sha256') != corpus_hash
            or mapping.get('properties') != expected or mapping.get('dynamic') != 'strict'
            or mapping.get('_source', {}).get('enabled', True) is not True
            or settings.get('index.number_of_shards') != '1' or settings.get('index.number_of_replicas') != '0'):
        raise ValueError('existing index must match the owned corpus, CJK/BM25 mapping and one-shard topology')


def profile(endpoint, index, queries, field, concurrency, warmup=5, repeat=1):
    path = f'/{index}/_search?request_cache=false&allow_partial_search_results=false'
    admitted = queries * repeat
    results = [None] * len(admitted)
    jobs = queue.Queue()
    for i, query in enumerate(admitted):
        jobs.put((i, query, search_body(query['params'][field])))
    with Client(endpoint) as client:
        for query in queries[:warmup]:
            extract_hits(client.request('POST', path, search_body(query['params'][field])))
    clients = []
    workers = []
    cancelled = threading.Event()
    ready = threading.Barrier(concurrency + 1)
    def worker(client):
        try:
            ready.wait()
        except threading.BrokenBarrierError:
            return
        while not cancelled.is_set():
            try:
                i, query, body = jobs.get_nowait()
            except queue.Empty:
                return
            row = {'id': query['id'], 'iteration': i // len(queries)}
            begin = time.perf_counter()
            try:
                response = client.request('POST', path, body)
                ids = extract_hits(response)
                row.update(ids=ids, sql_succeeded=True, es_took_ms=response['took'])
            except Exception as error:
                row.update(ids=[], sql_succeeded=False, error=f'{type(error).__name__}: {error}')
            row['latency_ms'] = (time.perf_counter() - begin) * 1000
            results[i] = row
    try:
        for _ in range(concurrency):
            client = Client(endpoint)
            clients.append(client)
            client.request('GET', '/')  # Connection setup outside the measured profile.
        for client in clients:
            thread = threading.Thread(target=worker, args=(client,))
            thread.start()
            workers.append(thread)
        begin = time.perf_counter()
        ready.wait()
        for thread in workers:
            thread.join()
        elapsed = time.perf_counter() - begin
    finally:
        cancelled.set()
        ready.abort()
        for client in clients:
            client.close()
        for thread in workers:
            thread.join()
    if any(result is None for result in results):
        raise RuntimeError('worker exited before completing admitted queries')
    for query, result in zip(admitted, results):
        if result['sql_succeeded']:
            result['quality'] = quality(result['ids'], query['relevance'])
    successes = [r for r in results if r['sql_succeeded']]
    scene = {'id': f'es_cjk_{field}', 'route': 'elasticsearch', 'oracle': 'qrels',
             'effective_concurrency': concurrency, 'selected_queries': len(queries),
             'executions': len(results), 'sql_successes': len(successes),
             'sql_failures': len(results) - len(successes), 'measured_seconds': elapsed,
             'qps': len(successes) / elapsed, 'results': results,
             'quality_mode': 'observe', 'ndcg_gain': 'linear', 'relevant_grade': 2,
             'top_k': 100, 'query_field': field, 'warmup': warmup, 'repetitions': repeat}
    if successes:
        scene.update({f'p{percent}_ms': percentile([r['latency_ms'] for r in successes], percent / 100)
                      for percent in (50, 90, 95, 99)})
    return scene


def stability(endpoint, index, queries, field, repetitions=30):
    results = []
    cells = []
    path = f'/{index}/_search?request_cache=false&allow_partial_search_results=false'
    with Client(endpoint) as client:
        for query in queries[:5]:
            sets = set()
            orders = set()
            baseline = None
            failures = 0
            for iteration in range(repetitions):
                row = {'id': query['id'], 'iteration': iteration}
                begin = time.perf_counter()
                try:
                    response = client.request('POST', path, search_body(query['params'][field]))
                    ids = extract_hits(response)
                    row['latency_ms'] = (time.perf_counter() - begin) * 1000
                    signature = tuple(sorted(Counter(ids).items()))
                    order = tuple(ids)
                    sets.add(signature)
                    orders.add(order)
                    if baseline is None:
                        baseline = (signature, order)
                    row.update(ids=ids, sql_succeeded=True, es_took_ms=response['took'],
                               **{'pass': baseline == (signature, order)})
                except Exception as error:
                    row.update(ids=[], sql_succeeded=False, error=f'{type(error).__name__}: {error}', **{'pass': False})
                    row['latency_ms'] = (time.perf_counter() - begin) * 1000
                failures += not row['pass']
                results.append(row)
            cells.append({'query_id': query['id'], 'executions': repetitions, 'failures': failures,
                          'distinct_results': len(sets), 'distinct_orders': len(orders)})
    return {'id': f'es_cjk_{field}_stability', 'oracle': 'stable_multiset',
            'query_field': field, 'selected_queries': min(5, len(queries)),
            'repetitions': repetitions, 'executions': len(results),
            'sql_successes': sum(r['sql_succeeded'] for r in results),
            'sql_failures': sum(not r['sql_succeeded'] for r in results),
            'check_order': True, 'allow_empty': True, 'stability': cells, 'results': results}


def chunks(path, max_rows=2000, max_bytes=8 * 1024 * 1024):
    csv.field_size_limit(64 * 1024 * 1024)
    data = bytearray()
    rows = 0
    with path.open(newline='') as stream:
        for item in csv.reader(stream):
            if len(item) != 2:
                raise ValueError('documents.csv requires id,body columns')
            doc_id, body = item
            action = json.dumps({'create': {'_id': doc_id}}, separators=(',', ':')).encode()
            document = json.dumps({'id': int(doc_id), 'body': body}, ensure_ascii=False, separators=(',', ':')).encode()
            data.extend(action + b'\n' + document + b'\n')
            rows += 1
            if rows >= max_rows or len(data) >= max_bytes:
                yield bytes(data), rows
                data.clear()
                rows = 0
        if rows:
            yield bytes(data), rows


def bulk_chunk(endpoint, index, raw, expected_rows):
    with Client(endpoint) as client:
        response = client.request('POST', f'/{index}/_bulk?refresh=false', raw=raw)
    items = response.get('items', [])
    if response.get('errors') or len(items) != expected_rows or any(
            item.get('create', {}).get('status') != 201 for item in items):
        errors = [item for item in items if item.get('create', {}).get('status') != 201]
        raise RuntimeError(f'bulk incomplete: expected {expected_rows}, got {len(items)}: {str(errors[:2])[:1000]}')
    return expected_rows


def import_documents(endpoint, index, path):
    imported = 0
    last = time.monotonic()
    with ThreadPoolExecutor(max_workers=4) as pool:
        pending = set()
        for raw, rows in chunks(path):
            pending.add(pool.submit(bulk_chunk, endpoint, index, raw, rows))
            if len(pending) == 4:
                done, pending = wait(pending, return_when=FIRST_COMPLETED)
                for future in done:
                    imported += future.result()
            if time.monotonic() - last > 20:
                print(f'imported {imported:,} rows', flush=True)
                last = time.monotonic()
        for future in pending:
            imported += future.result()
    return imported


def run(args):
    if args.output.exists():
        raise ValueError('output already exists; preserve measured runs')
    if not 1 <= args.repeat <= 20:
        raise ValueError('repeat must be between 1 and 20')
    manifest = json.loads((args.pack / 'manifest.json').read_text())
    corpus = args.pack / manifest['loads'][0]['file']['path']
    query_path = args.pack / 'anli_sql_bm25.jsonl'
    query_ref = json.loads((args.pack / 'anli_sql_bm25.json').read_text())['queries']
    corpus_hash = digest(corpus)
    query_hash = digest(query_path)
    if corpus_hash != manifest['loads'][0]['file']['sha256'] or query_hash != query_ref['sha256']:
        raise ValueError('pack inputs failed SHA-256 validation')
    queries = [json.loads(line) for line in query_path.read_text().splitlines()]
    if not queries or len({q['id'] for q in queries}) != len(queries):
        raise ValueError('queries must be nonempty and unique')
    index = args.index or f'mo_retrieval_bench_es_{uuid.uuid4().hex[:12]}'
    if not re.fullmatch(r'mo_retrieval_bench_es_[a-z0-9_]+', index):
        raise ValueError('index must use the owned mo_retrieval_bench_es_ namespace')
    definition = {'settings': {'number_of_shards': 1, 'number_of_replicas': 0, 'refresh_interval': '-1'},
                  'mappings': {'dynamic': 'strict', '_meta': {'owner': 'mo-retrieval-bench', 'corpus_sha256': corpus_hash},
                               'properties': {'id': {'type': 'long'}, 'body': {'type': 'text', 'analyzer': 'cjk', 'similarity': 'BM25'}}}}
    report = {'schema_version': 1, 'engine': 'Elasticsearch', 'started_at': utc(),
              'status': 'running', 'endpoint': args.endpoint, 'index': index,
              'inputs': [{'path': str(corpus), 'sha256': corpus_hash, 'rows': manifest['loads'][0]['rows']}],
              'queries_sha256': query_hash, 'query_ids': [q['id'] for q in queries],
              'runner_sha256': digest(Path(__file__)), 'index_definition': definition,
              'search_template': search_body('<bound query text>'),
              'profile': {'concurrency_levels': [1, 4, 8], 'repeat': args.repeat, 'warmup': 5,
                          'stability_queries': 5, 'stability_repeat': 30, 'timeout_seconds': 180},
              'scenarios': [], 'stages': [], 'index_retained': False}
    args.output.mkdir(parents=True)
    shutil.copyfile(query_path, args.output / 'query-inputs.jsonl')
    save(args.output / 'report.json', report)
    try:
        with Client(args.endpoint) as client:
            report['server'] = client.request('GET', '/')
            report['nodes_info'] = client.request('GET', '/_nodes/os,jvm,settings')
            if args.index:
                mapping = client.request('GET', f'/{index}/_mapping')[index]['mappings']
                settings = client.request('GET', f'/{index}/_settings?flat_settings=true')[index]['settings']
                verify_existing_index(mapping, settings, corpus_hash)
                report['actual_index'] = {'mapping': mapping, 'settings': settings}
            else:
                client.request('PUT', f'/{index}', definition)
                report['index_retained'] = True
                start = time.perf_counter()
                rows = import_documents(args.endpoint, index, corpus)
                report['stages'].append({'name': 'bulk_import_and_index', 'rows': rows, 'seconds': time.perf_counter() - start})
                client.request('PUT', f'/{index}/_settings', {'index': {'refresh_interval': '1s'}})
            client.request('POST', f'/{index}/_refresh')
            count = client.request('GET', f'/{index}/_count')['count']
            report['indexed_rows'] = count
            report['index_retained'] = True
            if count != manifest['loads'][0]['rows']:
                raise ValueError(f'indexed count {count} differs from corpus rows')
            deadline = time.monotonic() + 180
            while True:
                stats = client.request('GET', f'/{index}/_stats/merge,store,segments')
                if stats['_all']['primaries']['merges']['current'] == 0:
                    break
                if time.monotonic() > deadline:
                    raise TimeoutError('index merges did not settle before measurement')
                time.sleep(1)
            report['index_before'] = stats
            report['resources_before'] = client.request('GET', '/_nodes/stats/os,process,jvm,thread_pool')
            save(args.output / 'report.json', report)
            for field in ('anli_text', 'raw_text'):
                for concurrency in (1, 4, 8):
                    print(f'measuring {field}, concurrency {concurrency}', flush=True)
                    scene = profile(args.endpoint, index, queries, field, concurrency, repeat=args.repeat)
                    report['scenarios'].append(scene)
                    save(args.output / 'report.json', report)
                    print(f"  QPS {scene['qps']:.2f}; P95 {scene.get('p95_ms', 0):.2f} ms; errors {scene['sql_failures']}", flush=True)
                    if scene['sql_failures']:
                        raise RuntimeError('search failures; preserve partial evidence')
                print(f'measuring {field} repeat stability', flush=True)
                report['scenarios'].append(stability(args.endpoint, index, queries, field))
                save(args.output / 'report.json', report)
            report['resources_after'] = client.request('GET', '/_nodes/stats/os,process,jvm,thread_pool')
            report['index_after'] = client.request('GET', f'/{index}/_stats/merge,store,segments')
        report['status'] = 'passed' if all(not s['sql_failures'] for s in report['scenarios']) else 'failed'
    except BaseException as error:
        report.update(status='failed', error=f'{type(error).__name__}: {error}')
        raise
    finally:
        report['finished_at'] = utc()
        save(args.output / 'report.json', report)
    print(f"Saved {args.output / 'report.json'}", flush=True)
    return report


if __name__ == '__main__':
    cli = argparse.ArgumentParser(description=__doc__)
    cli.add_argument('--endpoint', default='http://127.0.0.1:19200')
    cli.add_argument('--pack', required=True, type=Path)
    cli.add_argument('--output', required=True, type=Path)
    cli.add_argument('--index', help='Reuse an already complete, owned index with verified corpus digest')
    cli.add_argument('--repeat', type=int, default=1, help='Loops through the same frozen cohort, 1..20')
    try:
        run(cli.parse_args())
    except Exception as error:
        print(f'{type(error).__name__}: {error}', file=sys.stderr)
        sys.exit(1)
