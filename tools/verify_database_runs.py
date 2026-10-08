#!/usr/bin/env python3
"""Independently recompute saved SQL metrics, quality and repeated stability."""
import argparse
from collections import Counter
import hashlib
import json
import math
from pathlib import Path
import shutil


def digest(path):
    h = hashlib.sha256()
    with path.open('rb') as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b''):
            h.update(block)
    return h.hexdigest()


def near(actual, expected):
    assert math.isclose(actual, expected, rel_tol=1e-12, abs_tol=1e-12), (actual, expected)


def verify(folder: Path, pack: Path, binary: Path):
    raw = folder / 'report.json'
    before = digest(raw)
    report = json.loads(raw.read_text())
    manifest = json.loads((pack / 'manifest.json').read_text())
    status = json.loads((folder / 'run-status.json').read_text())
    assert status['complete'] and report['cleanup'] in ['dropped', 'retained']
    assert report['binary_sha256'] == digest(binary)
    assert report['manifest_sha256'] == digest(pack / 'manifest.json')
    assert report['index_sql'] == manifest['indexes']
    assert not any(stage.get('error') for stage in report['stages'])
    assert len(report['inputs']) == len(manifest['loads'])
    for actual, expected in zip(report['inputs'], manifest['loads']):
        assert actual['sha256'] == expected['file']['sha256'] and actual['rows'] == expected['rows']
        stage = next(s for s in report['stages'] if s['name'] == 'load_' + expected['table'])
        assert stage['rows'] == expected['rows']
    scenes, query_sets, scenario_digests = {}, {}, {}
    for ref in manifest['scenarios']:
        path = pack / ref['path']
        assert digest(path) == ref['sha256']
        scene = json.loads(path.read_text())
        qpath = pack / scene['queries']['path']
        assert digest(qpath) == scene['queries']['sha256']
        scenes[scene['id']] = scene
        scenario_digests[scene['id']] = ref['sha256']
        query_sets[scene['id']] = [json.loads(line) for line in qpath.read_text().splitlines()]
        shutil.copy2(path, folder / path.name)
        shutil.copy2(qpath, folder / qpath.name)
    summaries = []
    verified_quality = verified_stability = 0
    for scene in report['scenarios']:
        base = scene.get('base_scenario_id', scene['id'])
        config = scenes[base]
        inputs = query_sets[base][:scene['selected_queries']]
        queries = {q['id']:q for q in inputs}
        assert not scene.get('error') and not scene.get('plan_error'), scene['id']
        assert scene['queries_sha256'] == config['queries']['sha256']
        assert scene['scenario_sha256'] == scenario_digests[base]
        assert scene['sql'] == config['sql'] and scene.get('session_sql') == config.get('session_sql')
        assert scene['top_k'] == 100 and scene.get('physical_plans')
        results = scene['results']
        assert len(results) == scene['executions'] == len(inputs) * scene['repetitions']
        assert [r['id'] for r in results] == [q['id'] for q in inputs] * scene['repetitions']
        assert [r['iteration'] for r in results] == [i for i in range(scene['repetitions']) for _ in inputs]
        good = [r for r in results if r['sql_succeeded']]
        assert len(good) == scene['sql_successes']
        assert len(results) - len(good) == scene['sql_failures'] == 0
        assert sum(not r['pass'] for r in results) == scene['failures'] == scene['assertion_failures']
        near(scene['qps'], len(good) / scene['measured_seconds'])
        latencies = sorted(r['latency_ms'] for r in good)
        for percent in [50,90,95,99]:
            near(scene[f'p{percent}_ms'], latencies[math.ceil(percent / 100 * len(latencies)) - 1])
        for r in good:
            q, ids = queries[r['id']], r.get('ids',[])
            assert len(ids) <= 100
            if q.get('allowed_id_ranges'):
                assert all(any(lo <= int(id) <= hi for lo,hi in q['allowed_id_ranges']) for id in ids)
            if scene['oracle'] == 'ann_recall':
                assert len(set(ids)) == len(ids)
                expected = len(set(ids) & set(q['exact_ids'][:100])) / min(100,len(q['exact_ids']))
                near(r['score'], expected)
                assert r['pass'] and scene['quality_mode'] == 'observe' and scene['min_score'] == 0
                verified_quality += 1
            elif scene['oracle'] == 'qrels':
                assert len(set(ids)) == len(ids)
                grades = q['relevance']
                def ndcg(k):
                    ideal = sorted(grades.values(), reverse=True)[:k]
                    denominator = sum(grade / math.log2(i + 2) for i,grade in enumerate(ideal))
                    numerator = sum(grades.get(id,0) / math.log2(i + 2) for i,id in enumerate(ids[:k]))
                    return numerator / denominator if denominator else 0
                positive = {id for id,grade in grades.items() if grade >= 2}
                expected = {'ndcg':ndcg(100), 'ndcg_at_10':ndcg(10),
                            'recall':len(set(ids) & positive) / len(positive),
                            'mrr_at_10':next((1 / (i + 1) for i,id in enumerate(ids[:10]) if id in positive),0)}
                assert scene['ndcg_gain'] == 'linear' and scene['relevant_grade'] == 2
                for key,value in expected.items():
                    near(r['quality'][key],value)
                near(r['score'],expected['ndcg'])
                assert r['pass']
                verified_quality += 1
        if scene['oracle'] == 'stable_multiset':
            assert scene['check_order'] and scene['allow_empty'] and scene['selected_queries'] == 5 and scene['repetitions'] == 30
            for stats in scene['stability']:
                observations = [r for r in good if r['id'] == stats['query_id']]
                first = tuple(observations[0].get('ids',[]))
                failures = reorders = max_changed = 0
                worst = 1
                for r in observations:
                    ids = tuple(r.get('ids',[]))
                    common = sum((Counter(first) & Counter(ids)).values())
                    denominator = max(len(first),len(ids))
                    overlap = common / denominator if denominator else 1
                    near(r['score'],overlap)
                    assert r['pass'] == (ids == first)
                    failures += not r['pass']
                    reorders += overlap == 1 and ids != first
                    max_changed = max(max_changed,denominator - common)
                    worst = min(worst,overlap)
                assert stats['distinct_results'] == len({tuple(sorted(r.get('ids',[]))) for r in observations})
                assert stats['distinct_orders'] == len({tuple(r.get('ids',[])) for r in observations})
                assert stats['failures'] == failures and stats.get('reordered_executions',0) == reorders
                assert stats['max_changed_ids'] == max_changed
                near(stats['worst_overlap'],worst)
                verified_stability += len(observations)
        near(scene['mean_score'],sum(r['score'] for r in good) / len(good))
        summaries.append({'id':scene['id'],'base_scenario_id':base,'concurrency':scene['effective_concurrency'],
                          'oracle':scene['oracle'],'executions':len(results),'sql_failures':scene['sql_failures'],
                          'assertion_failures':scene['assertion_failures'],
                          'mean_returned':sum(len(r.get('ids',[])) for r in good) / len(good),
                          'full_result_rate':sum(len(r.get('ids',[])) == 100 for r in good) / len(good),
                          **{key:scene[key] for key in ['mean_score','qps','p90_ms','p95_ms','p99_ms','measured_seconds']}})
    if report['profile'].get('mixed_scenarios'):
        assert len(report['scenarios']) == 12 and verified_quality == 1200
        for c in [1,4,8]:
            mixed = [s for s in report['scenarios'] if s['effective_concurrency'] == c and s.get('execution_mode') == 'mixed']
            assert len(mixed) == 2 and mixed[0]['measured_seconds'] == mixed[1]['measured_seconds']
            for s in mixed:
                original = next(a for a in report['scenarios'] if a['id'] == s['base_scenario_id'] and a['effective_concurrency'] == c)
                assert s['sql'] == original['sql'] and s['queries_sha256'] == original['queries_sha256'] and s['scenario_sha256'] == original['scenario_sha256']
    else:
        levels = report['profile'].get('concurrency_levels') or [report['profile']['concurrency']]
        assert levels in ([1], [1,4,8])  # serial filter profile and frozen legacy sweep
        quality_ids = [id for id,config in scenes.items() if config['oracle'] == 'ann_recall']
        stability_ids = [id for id,config in scenes.items() if config['oracle'] == 'stable_multiset']
        assert len(quality_ids) == len(stability_ids) == 6
        assert len(report['scenarios']) == len(quality_ids)*len(levels)+len(stability_ids)
        assert verified_quality == sum(len(query_sets[id]) for id in quality_ids)*len(levels)
        assert verified_stability == len(stability_ids)*5*30
        for id, config in scenes.items():
            if config['oracle'] != 'ann_recall':
                continue
            profiles = [s for s in report['scenarios'] if s['id'] == id]
            assert sorted(s['effective_concurrency'] for s in profiles) == levels
            assert all('ivf_search' in s['plan'] for s in profiles)
            assert all(q['allowed_id_ranges'] == [[0,q['params']['cutoff'] - 1]] for q in query_sets[id])
    shutil.copy2(pack / 'manifest.json',folder / 'pack-manifest.json')
    shutil.copy2(pack / 'source.json',folder / 'source.json')
    proof = {'raw_sha256':before,'binary_sha256':report['binary_sha256'],
             'method':'Independent Python set/Counter/ordered tuple, linear qrels, nearest-rank percentiles and successful SQL / batch wall time; all returned filtered IDs checked.',
             'verified_quality_executions':verified_quality,'verified_stability_executions':verified_stability,
             'sql_failures':sum(s['sql_failures'] for s in report['scenarios']),
             'assertion_failures':sum(s['assertion_failures'] for s in report['scenarios']),
             'summary':summaries}
    (folder / 'verification.json').write_text(json.dumps(proof,ensure_ascii=False,indent=2)+'\n')
    assert digest(raw) == before
    return proof


if __name__ == '__main__':
    cli = argparse.ArgumentParser(description=__doc__)
    cli.add_argument('--report',type=Path,required=True)
    cli.add_argument('--pack',type=Path,required=True)
    cli.add_argument('--binary',type=Path,required=True)
    args = cli.parse_args()
    result = verify(args.report,args.pack,args.binary)
    print(json.dumps({key:value for key,value in result.items() if key != 'summary'},ensure_ascii=False))
