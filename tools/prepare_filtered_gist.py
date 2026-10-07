#!/usr/bin/env python3
"""Offline GIST eligibility/exact-truth pack and GIST/T2 concurrent-SQL pack."""
import argparse
import copy
import json
import os
from pathlib import Path
import sys

from prepare_public_pack import sha256, ref, write_json, add_scenario


def exact_truth(base, queries, cutoffs, top_k=100, chunk_size=8192):
    import numpy as np
    from threadpoolctl import threadpool_limits
    q = np.asarray(queries, dtype=np.float64)
    qnorm = np.einsum('ij,ij->i', q, q)
    best = {cutoff: [(np.empty(0), np.empty(0, dtype=np.int64)) for _ in q] for cutoff in cutoffs}
    with threadpool_limits(limits=8):
        for start in range(0, len(base), chunk_size):
            block = np.asarray(base[start:start + chunk_size], dtype=np.float64)
            distances = np.maximum(0, np.einsum('ij,ij->i', block, block)[:, None] + qnorm[None, :] - 2 * block @ q.T)
            for cutoff in cutoffs:
                n = min(len(block), cutoff - start)
                if n <= 0:
                    continue
                for number in range(len(q)):
                    values = distances[:n, number]
                    # Include every boundary tie before sorting by (distance, id).
                    count = min(top_k, n)
                    boundary = np.partition(values, count - 1)[count - 1]
                    chosen = np.flatnonzero(values <= boundary)
                    old_values, old_ids = best[cutoff][number]
                    values = np.concatenate((old_values, values[chosen]))
                    ids = np.concatenate((old_ids, chosen + start))
                    order = np.lexsort((ids, values))[:top_k]
                    best[cutoff][number] = values[order], ids[order]
            print(f'exact truth: {min(start + len(block), len(base))}/{len(base)}', file=sys.stderr, flush=True)
    return {cutoff: [list(map(str, ids)) for _, ids in best[cutoff]] for cutoff in cutoffs}


def prepare(source: Path, original_pack: Path, text_pack: Path, output: Path, mixed: Path, query_count: int):
    import numpy as np
    output.mkdir(parents=True, exist_ok=False)
    mixed.mkdir(parents=True, exist_ok=False)
    manifest = json.loads((original_pack / 'manifest.json').read_text())
    count = manifest['loads'][0]['rows']
    width = 960
    record_bytes = (width + 1) * 4
    if count != 1_000_000 or not 5 <= query_count <= 100:
        raise ValueError('full GIST1M and 5..100 queries are required')
    arrays = []
    for file, rows in [('gist_base.fvecs', count), ('gist_query.fvecs', query_count)]:
        path = source / file
        if path.stat().st_size < rows * record_bytes or path.stat().st_size % record_bytes:
            raise ValueError(f'invalid {file} size')
        mapped = np.memmap(path, dtype='<f4', mode='r').reshape(-1, width + 1)[:rows]
        if not np.all(mapped[:, 0].view('<i4') == width) or not np.isfinite(mapped[:, 1:]).all():
            raise ValueError(f'invalid {file} vectors')
        arrays.append(mapped[:, 1:])
    cutoffs = [count, count // 10, count // 100]
    truth = exact_truth(arrays[0], arrays[1], cutoffs)
    original_queries = [json.loads(line) for line in (original_pack / 'vector_nprobe_100.jsonl').read_text().splitlines()][:query_count]
    if len(original_queries) != query_count:
        raise ValueError('not enough frozen query vectors')
    csv = output / 'vectors.csv'
    with (original_pack / 'documents.csv').open() as src, csv.open('w') as dst:
        written = 0
        for line in src:
            identifier, vector = line.split(',', 1)
            if int(identifier) != written:
                raise ValueError('GIST IDs must be contiguous and zero based')
            dst.write(f'{identifier},{identifier},{vector}')
            written += 1
    if written != count:
        raise ValueError('incomplete GIST CSV')
    vector_ddl = 'CREATE TABLE vectors (id bigint PRIMARY KEY, visibility_rank bigint, embedding vecf32(960))'
    index = "CREATE INDEX idx_vec USING ivfflat ON vectors(embedding) lists=1000 op_type 'vector_l2_ops'"
    pack = {'schema_version': 3, 'dataset': 'gist1m_filtered_v7', 'ddl': [vector_ddl],
            'loads': [{'file': ref(csv), 'table': 'vectors', 'rows': count}], 'indexes': [index], 'scenarios': []}
    metadata = []
    for cutoff, percent in zip(cutoffs, [100, 10, 1]):
        queries = []
        for number, original in enumerate(original_queries):
            item = copy.deepcopy(original)
            item['params']['cutoff'] = cutoff
            item['exact_ids'] = truth[cutoff][number]
            item['allowed_id_ranges'] = [[0, cutoff - 1]]
            queries.append(item)
        for mode in ['pre', 'post']:
            name = f'vector_{mode}_{percent}'
            sql = ("SELECT id FROM vectors WHERE visibility_rank < ? ORDER BY l2_distance(embedding, ?) "
                   f"LIMIT 100 by rank with option 'mode={mode}'")
            scene = {'id': name, 'route': 'sql', 'sql': sql, 'args': ['cutoff', 'vector'],
                     'session_sql': ['SET probe_limit=100'], 'oracle': 'ann_recall', 'quality_mode': 'observe', 'top_k': 100}
            add_scenario(output, pack, name, scene, queries)
            stable_queries = copy.deepcopy(queries[:5])
            for item in stable_queries:
                item.pop('exact_ids')
            stable = {**scene, 'id': name + '_stability', 'oracle': 'stable_multiset', 'id_column': 'id',
                      'quality_mode': '', 'check_order': True, 'allow_empty': True, 'min_repetitions': 30,
                      'plan_must_contain': {'tablefunction': 1}}
            add_scenario(output, pack, stable['id'], stable, stable_queries)
            metadata.append({'id': name, 'mode': mode, 'visible_percent': percent, 'eligible_rows': cutoff})
    write_json(output / 'manifest.json', pack)
    write_json(output / 'source.json', {'dataset': 'gist1m_filtered_v7', 'cases': metadata,
               'rows': count, 'dimensions': width, 'query_count': query_count, 'top_k': 100,
               'visibility': 'visibility_rank=id; fixed prefix partitions, synthetic access metadata',
               'truth': 'float64 squared L2 from original float32 vectors; eligible population first; ties by numeric id',
               'numpy_version': np.__version__, 'chunk_rows': 8192, 'blas_threads': 8,
               'sources': {file: sha256(source / file) for file in ['gist_base.fvecs','gist_query.fvecs','gist_groundtruth.ivecs']},
               'preparer_sha256': sha256(Path(__file__))})
    # The CSV files are immutable hardlinks; no data expansion is needed for mixed load.
    os.link(csv, mixed / 'vectors.csv')
    os.link(text_pack / 'documents.csv', mixed / 'texts.csv')
    text_manifest = json.loads((text_pack / 'manifest.json').read_text())
    text_scene = json.loads((text_pack / 'anli_sql_bm25.json').read_text())
    sql = text_scene['sql'].replace('documents', 'texts')
    prefix, suffix = 'SELECT id FROM (', ') chosen WHERE vec_dist > 0.01 ORDER BY vec_dist DESC, id'
    if not sql.startswith(prefix) or not sql.endswith(suffix):
        raise ValueError('unexpected frozen anli SQL wrapper')
    text_scene['sql'] = sql[len(prefix):-len(suffix)]
    text_scene['id'] = 'fulltext'
    text_scene['id_column'] = 'id'
    vector_scene = json.loads((output / 'vector_pre_10.json').read_text())
    vector_scene['id'] = 'vector'
    mixed_pack = {'schema_version': 3, 'dataset': 'gist_t2_sql_workload_v7',
                  'ddl': [vector_ddl, 'CREATE TABLE texts (id bigint PRIMARY KEY, body text)'],
                  'loads': [{'file': ref(mixed / 'vectors.csv'), 'table': 'vectors', 'rows': count},
                            {'file': ref(mixed / 'texts.csv'), 'table': 'texts', 'rows': text_manifest['loads'][0]['rows']}],
                  'indexes': [index, 'CREATE FULLTEXT INDEX idx_body ON texts(body) WITH PARSER ngram'], 'scenarios': []}
    add_scenario(mixed, mixed_pack, 'vector', vector_scene,
                 [json.loads(line) for line in (output / 'vector_pre_10.jsonl').read_text().splitlines()])
    add_scenario(mixed, mixed_pack, 'fulltext', text_scene,
                 [json.loads(line) for line in (text_pack / 'anli_sql_bm25.jsonl').read_text().splitlines()][:query_count])
    write_json(mixed / 'manifest.json', mixed_pack)
    write_json(mixed / 'source.json', {'dataset': 'gist_t2_sql_workload_v7', 'vector_pack_sha256': sha256(output / 'manifest.json'),
               'text_pack_sha256': sha256(text_pack / 'manifest.json'), 'query_count': query_count,
               'scope': 'Two independent datasets/tables, paired concurrent MO SQLs; no RRF, model calls or client post-filter. Not an anli same-table reproduction.',
               'fulltext': 'Frozen anli tokens, BM25/ngram; SQL returns id and score; original client 0.01 cutoff removed.'})
    print(f'Prepared {output} and {mixed}', flush=True)


if __name__ == '__main__':
    cli = argparse.ArgumentParser(description=__doc__)
    cli.add_argument('--source', type=Path, required=True)
    cli.add_argument('--gist-pack', type=Path, required=True)
    cli.add_argument('--text-pack', type=Path, required=True)
    cli.add_argument('--out', type=Path, required=True)
    cli.add_argument('--mixed-out', type=Path, required=True)
    cli.add_argument('--queries', type=int, default=100)
    args = cli.parse_args()
    prepare(args.source, args.gist_pack, args.text_pack, args.out, args.mixed_out, args.queries)
