# Field retrieval benchmark v5: anli-shaped T2Ranking comparison

## Scope and design decision (2026-10-06)

The user approved an anli-shaped fulltext scenario and requested an explanation
for the existing T2Ranking result. Work stays in the isolated
`codex/mo-retrieval-bench` worktree at base
`1f6b6d17c0e7c2ff7981be9a2f85fa9ec3a83650`. Existing local runner/report changes
and all measured JSON files remain intact. No MatrixOne kernel, wire format,
runner scheduler, oracle or lifecycle change is needed.

The public corpus supplies documents and judgments, not a required SQL search
expression. Our earlier first-100k/ten-query pilot submitted whole questions to
ngram BOOLEAN mode. The application snapshot instead preprocesses a question
with jieba/spacy, its custom dictionary and stopwords, joins the words with
spaces, and submits BOOLEAN searches. MatrixOne 4.2.1's GenTextSql lowers each
Boolean TEXT fragment to position-constrained matching, so these are materially
different workloads. Confirm the difference experimentally; do not infer an
engine bug or customer quality from a subset score.

## Contract and ownership

An off-site preparer owns the new immutable pack. It verifies the original pack
references before copying data, preserves query IDs and subset judgments, and
freezes both original text and application-preprocessed text in query inputs.
Use the snapshot's Chinese/English segmentation rules, pinned jieba 0.42.1 and
spacy 3.8.7, zh_core_web_sm and resource digests. Preparation dependencies never
enter the customer binary or runtime. Reject unusable/empty tokenized inputs
before copying the large CSV. Output creation is exclusive; source files are
never modified, and failure cleans only the newly owned output directory.

Four ordinary scenarios share one corpus/index and top_k=100:

1. Original sentence, canonical WHERE/MATCH/ORDER/LIMIT, pinned TF-IDF.
2. Application tokens, otherwise identical SQL and TF-IDF: isolate input.
3. Application tokens with anli's nested candidate LIMIT (100x), permission and
   deletion filter, score/time/ID ordering, final LIMIT and post-limit score
   cutoff (>0.01), pinned TF-IDF: isolate SQL shape.
4. Same application SQL and tokens, pinned BM25: isolate scoring choice.

Keep the first ten historical query IDs, corpus and subset relevance labels for
the first controlled run. Use clients 1/4/8, ten repetitions and warmup; label
the repeated ten-query sample rather than imply a broad official benchmark.
The two anli SQL variants also get independent five-query/30-repeat multiset
checks. Variable hit counts are permitted (maximum 100); quality/empty-result
metrics remain separate so consistently empty output is never presented as a
healthy retrieval result. The existing runner owns bounds, fresh session SETs,
SQL timeouts and unique-database cleanup.

Only T2Ranking passage text is indexed. Permissions are synthetic OPEN and the
business timestamp is constant, projected in the SQL: this validates the
application SQL shape without claiming customer data, permissions selectivity,
data distribution, two-SQL detail fetching, hybrid fusion or reranking coverage.
The snapshot does not establish the customer's actual index parser or scoring
variable. Keep ngram as the controlled baseline and display both scoring modes.

## Alternatives and diagnosis

Running the application backend would add models, Redis and customer services
and break the independent-client goal. Changing the database parser or several
parameters at once would confound input diagnosis. Freezing preprocessed inputs
and making paired SQL/score scenarios fits the existing pack contract and keeps
the fewest moving parts.

An independent tiny public SQL probe owns its own database and closes it on all
paths. Use competing two-row Chinese passages to distinguish whole-fragment
position matching, spaced Boolean OR, mandatory words and prefix behavior.
For the real run, verify official subset scores independently, empty/hit counts,
measured repetitions, SQL errors, physical index use and cleanup. Explain qrels
coverage and subset bias separately from matching behavior.

## Change/risk and validation map

- R2 off-site pack producer: input identity, resource/version provenance,
  preflight validation, immutable old files, session pins, SQL/Top-K consistency,
  separate quality/stability oracles, and atomic output ownership. Use a tiny
  fixture with an injected tokenizer for pack invariants; use the real pinned
  tokenizer for the actual controlled pack.
- R1 portal consumer: preserve dataset routes and native switching, reuse
  current SVG overview/stability components, add a paired empty-result/quality
  view and explicitly scoped metadata. Validate desktop/mobile and raw hashes.
- R2 report projection: add optional `render --scenario-ids` alongside the
  existing concurrency selector so the retrieval overview does not combine
  primary-key import checks with fulltext workloads. Preserve the original
  report JSON, verdict and independently repeated stability scenes. Reject
  invalid, duplicate or absent ordinary IDs before publishing HTML. Identify
  the selected scope visibly. Prefix chart labels with scenario IDs when a
  session parameter alone would conflate different SQL/input scenarios.
  Extend the existing atomic saved-report test and owning renderer tests; run
  owning normal tests, vet and incremental lint. No runner or kernel contract
  changes; no kernel BVT, race, topology, restart or upgrade claim is added.

Design review decision: PASS for the user-approved pack/portal extension.
No blocking architectural question; actual matching and quality remain measured
outcomes rather than prerequisites for a successful tool execution.


## Terminal evidence and review (2026-10-06)

- Two deterministic producer tests pass in 0.005s. Evidence covers paired query
  IDs/text/judgments, digest references, algorithm pins, SQL limits/filter/cutoff,
  independent variable-cardinality stability, unchanged source files, invalid
  cardinalities/empty tokenization/corrupt digest, existing-output refusal and
  cleanup after an injected partial write failure.
- The real pinned tokenizer matches the original snapshot implementation for
  all ten measured questions and five nearby Chinese/English/numeric/symbol/
  truncation controls. Dependency/resource versions and hashes are frozen.
- The two-row SQL fixture validates the new pack through the static client and
  4.2.1, including both scoring modes and cleanup: zero SQL errors; the original
  sentence negative control fails quality as expected. A separate minimal SQL
  probe proves whole-sentence vs spaced-OR vs mandatory-word matching and shows
  that changing the original sentence to Natural mode still has no hit.
- The first-100k controlled trial completes in 67.579s (import 0.556s, index
  43.214s), with 1,650 successful measured SQL executions, no SQL errors and
  database dropped. Every ordinary EXPLAIN contains fulltext_index_scan.
- Independent qrels computation reproduces every ordinary score and both
  Top-10/Top-100 means. The original-sentence/TF-IDF mean nDCG@10 is 0.100 and
  nine inputs are empty; changing only input to application tokens makes all
  ten nonempty and mean nDCG@10 0.571871 (nDCG@100 0.592281). The nested anli
  TF-IDF SQL has the same judged means; BM25 gives 0.422449 / 0.490032.
- A real health finding is retained: nested anli TF-IDF query 4 has 17 distinct
  hundred-ID multisets over thirty repeats; twenty-four differ from the first,
  minimum overlap 0.96, maximum four replaced IDs. All five BM25 repeated checks
  have one multiset. Independent Counter-based checks reproduce the stability
  values. This single-CN finding is not attributed to a multi-CN sort fix.
- Two focused renderer tests pass; the owning pure-Go package passes with
  -count=1, vet and incremental lint pass (zero issues). Selected scene IDs
  preserve raw input/status, keep independent stability, isolate axes, and
  reject invalid/absent IDs without replacing prior HTML. Unfiltered rendering
  remains compatible. Labels distinguish identical session parameters.
- Chromium passes at 1280x1000 and 390x844: separate dataset pages, paired
  nonempty/nDCG@10 bars matching saved comparison values, four fulltext lines
  with 1/4/8, every switched percentile matching raw observations, 300 stability
  cells with 24 visible failures, native switching/reload/back/forward, report
  links, no overflow, browser errors or external requests. Visual inspection
  confirms desktop/mobile layout. Primary-key load probes remain in raw JSON.

Pack manifest SHA-256:
`b552c6e25785220eac44657022f4fe876a4674e91e7d2713a3b29c4fd9e10115`.
Measured JSON SHA-256:
`b531ff63c63e82616b89d1943d49de31a2b6fd10e296492d10fa7eb38f2f560f`.
Measurement uses preserved v0.5.3; final v0.5.4 renderer SHA-256:
`86923bfc5edd629df70da397dba1ce778d2f264a4b345e159edba590273a4161`.

Implementation review decision: PASS for the scoped pack/render/portal work.
The benchmark verdict is deliberately FAILED for observed quality/stability
checks; a complete implementation does not imply a healthy measured workload.
No unresolved delivery blocker. Kernel cause of the observed nested-SQL
instability, broader query coverage, realistic permission selectivity and
customer parser/scoring configuration remain separate investigations.
