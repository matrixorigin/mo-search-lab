# Performance measurement

## Contract

Ordinary vector/fulltext and paired SQL performance workloads default to one
30-second round at client concurrency 1, preceded by 10 seconds of warmup.
`--rounds 3` repeats warmup and measurement on the same data and indexes, with
3 seconds of cooldown between rounds. `--measure-duration 0` retains the
fixed-query-count mode. Filter checks and stability checks remain fixed-count,
serial workloads with their own budgets.

Vectors/parameters and persistent worker sessions are prepared before timing.
Each worker issues a new request only while the measurement window is open.
Requests already in flight are drained. QPS uses successful SQL requests divided
by actual wall time through the last completion. Paired workloads wait for both
routes and record separate route latencies over the same wall time. These are
closed-loop client measurements: serialization by the SQL driver, network,
result reading and lightweight recording/dispatch overhead remain included.
They do not claim server-only latency or open-loop arrival-rate behavior.

No quality scoring, percentile aggregation, chart generation or progress logging
runs inside the measured worker phase. All successful latency samples participate
in pooled nearest-rank P50/P90/P95/P99. Each round also has its own statistics.
Pooled QPS uses the sum of successes / sum of actual measurement times; QPS sample
standard deviation and coefficient of variation describe individual round QPS.
Warmup, cooldown and quality-processing time are excluded from that denominator.

## Quality evidence and bounded retention

During duration workloads, only the first request for each selected query per
round retains result IDs. Quality is computed from these samples after timing;
other requests retain latency, success/error, query identity and round, and do
not count as zero-quality results. Reports state the quality sample count.
Stability checking remains independent and retains all of its results.

The planner reserves fixed-count checks and splits the remaining 100,000-request
and 1,000,000-ID report budget across duration profiles, including paired routes.
If a request budget is reached, the workload stops and explicitly reports that
the target duration was not completed. Reduced quality coverage is also explicit.
This prevents unbounded heap/report growth without silently claiming full-duration
results. A report with fewer than 1,000 successful latency samples warns that P99
has limited tail evidence. Old reports without these fields retain their original
semantics when rendered or viewed in the CLI.

## Evidence required

Check in-flight overrun accounting, cancellation and worker cleanup, sample caps,
quality exclusion, pooled percentile/quality denominators, shared paired timing,
serial filter admission, CLI option round trips and old/new report rendering.
