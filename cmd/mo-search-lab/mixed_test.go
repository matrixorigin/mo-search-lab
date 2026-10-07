package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

type mixedTestState struct {
	started              chan string
	release              chan struct{}
	failRoute, failSetup string
	mu                   sync.Mutex
	opens, closes        int
}
type mixedTestConnector struct{ state *mixedTestState }

func (c mixedTestConnector) Connect(context.Context) (driver.Conn, error) {
	c.state.mu.Lock()
	c.state.opens++
	c.state.mu.Unlock()
	return &mixedTestConn{state: c.state}, nil
}
func (c mixedTestConnector) Driver() driver.Driver { return mixedTestDriver{c.state} }

type mixedTestDriver struct{ state *mixedTestState }

func (d mixedTestDriver) Open(string) (driver.Conn, error) {
	return mixedTestConnector{d.state}.Connect(context.Background())
}

type mixedTestConn struct {
	state   *mixedTestState
	setting string
}

func (c *mixedTestConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (c *mixedTestConn) Begin() (driver.Tx, error) { return nil, errors.New("unexpected transaction") }
func (c *mixedTestConn) Close() error {
	c.state.mu.Lock()
	c.state.closes++
	c.state.mu.Unlock()
	return nil
}
func (c *mixedTestConn) ExecContext(_ context.Context, s string, _ []driver.NamedValue) (driver.Result, error) {
	if s == c.state.failSetup {
		return nil, errors.New("setup rejected")
	}
	c.setting = s
	return driver.RowsAffected(0), nil
}
func (c *mixedTestConn) QueryContext(ctx context.Context, s string, _ []driver.NamedValue) (driver.Rows, error) {
	if strings.HasPrefix(s, "EXPLAIN ") {
		return &mixedTestRows{column: "plan", value: "tablefunction"}, nil
	}
	if c.setting != "SET "+s {
		return nil, errors.New("wrong session setting")
	}
	select {
	case c.state.started <- s:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case <-c.state.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if s == c.state.failRoute {
		return nil, errors.New("branch failed")
	}
	return &mixedTestRows{column: "id", value: "1"}, nil
}

type mixedTestRows struct {
	column, value string
	read          bool
}

func (r *mixedTestRows) Columns() []string { return []string{r.column} }
func (r *mixedTestRows) Close() error      { return nil }
func (r *mixedTestRows) Next(values []driver.Value) error {
	if r.read {
		return io.EOF
	}
	r.read = true
	values[0] = r.value
	return nil
}

func mixedTestPair() [2]scenarioRun {
	var pair [2]scenarioRun
	for i, id := range []string{"V", "T"} {
		pair[i] = scenarioRun{Scenario: loadedScenario{scenario: scenario{ID: id, Route: "sql", SQL: id, SessionSQL: []string{"SET " + id}, Oracle: "exact_ids", TopK: 1}, QueriesData: []query{{ID: id, ExactIDs: []string{"1"}}}}, Options: options{concurrency: 1, repeat: 1, timeout: time.Second}}
	}
	return pair
}

func TestMixedSQLOverlapFailureAndCancellation(t *testing.T) {
	for _, mode := range []string{"success", "branch_failure", "cancel", "setup_failure"} {
		t.Run(mode, func(t *testing.T) {
			state := &mixedTestState{started: make(chan string, 2), release: make(chan struct{})}
			if mode == "branch_failure" {
				state.failRoute = "T"
			}
			if mode == "setup_failure" {
				state.failSetup = "SET T"
			}
			db := sql.OpenDB(mixedTestConnector{state})
			db.SetMaxOpenConns(2)
			db.SetMaxIdleConns(0)
			defer db.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			done := make(chan [2]ScenarioReport, 1)
			go func() { done <- runMixedScenarios(ctx, db, mixedTestPair()) }()
			if mode != "setup_failure" {
				seen := map[string]bool{}
				for i := 0; i < 2; i++ {
					select {
					case id := <-state.started:
						seen[id] = true
					case <-ctx.Done():
						t.Fatal("both SQLs did not overlap")
					}
				}
				if len(seen) != 2 {
					t.Fatal("routes were not distinct")
				}
				if mode == "cancel" {
					cancel()
				} else {
					close(state.release)
				}
			}
			var result [2]ScenarioReport
			select {
			case result = <-done:
			case <-ctx.Done():
				if mode != "cancel" {
					t.Fatal("mixed workers failed to finish")
				}
				select {
				case result = <-done:
				case <-time.After(time.Second):
					t.Fatal("cancelled workers failed to join")
				}
			}
			if db.Stats().InUse != 0 {
				t.Fatal("session lease leaked")
			}
			state.mu.Lock()
			opens, closes := state.opens, state.closes
			state.mu.Unlock()
			if opens != closes {
				t.Fatalf("connections open=%d close=%d", opens, closes)
			}
			if mode == "setup_failure" {
				if result[0].Error == "" || result[1].Error == "" || result[0].Executions != 0 {
					t.Fatal("partial setup published measurements")
				}
				return
			}
			for i, r := range result {
				if r.ExecutionMode != "mixed" || r.BaseScenarioID == "" || r.Executions != 1 || r.MeasuredSeconds != result[0].MeasuredSeconds {
					t.Fatalf("missing branch identity or timing: %+v", r)
				}
				want := mode != "cancel" && (mode != "branch_failure" || i == 0)
				if (r.SQLSuccesses == 1) != want {
					t.Fatalf("branch %d success=%d want=%v", i, r.SQLSuccesses, want)
				}
			}
		})
	}
}

func TestMixedSelectionAndRetention(t *testing.T) {
	pair := mixedTestPair()
	scenes := []loadedScenario{pair[0].Scenario, pair[1].Scenario}
	o := options{repeat: 1, concurrency: 1, mixedScenarios: []string{"V", "T"}}
	runs, err := planScenarioRuns(scenes, o)
	if err != nil {
		t.Fatal(err)
	}
	if err = validateMixedSelection(scenes, o, runs); err != nil {
		t.Fatal(err)
	}
	for _, ids := range [][]string{{"V"}, {"V", "V"}, {"V", "missing"}} {
		o.mixedScenarios = ids
		if validateMixedSelection(scenes, o, runs) == nil {
			t.Fatalf("invalid selection accepted %v", ids)
		}
	}
	o.mixedScenarios = []string{"V", "T"}
	runs[0].Options.repeat = 100000
	runs[1].Options.repeat = 100000
	if validateMixedSelection(scenes, o, runs) == nil {
		t.Fatal("added mixed retention ignored")
	}
}

func TestFilteredObservationCannotWaiveEligibility(t *testing.T) {
	s := scenario{Oracle: "ann_recall", QualityMode: "observe", TopK: 2}
	q := query{ExactIDs: []string{"1", "2"}, AllowedIDRanges: [][2]int64{{0, 9}}}
	for _, ids := range [][]string{{"3"}, nil} {
		score, pass, _ := scoreQuery(s, q, ids)
		if !pass || score != 0 {
			t.Fatal("observation became invented quality gate")
		}
	}
	for _, ids := range [][]string{{"10"}, {"invalid"}} {
		_, pass, why := scoreQuery(s, q, ids)
		if pass || why == "" {
			t.Fatal("eligibility violation accepted")
		}
	}
	if validateEvaluationOptions(s, 2) == nil {
		t.Fatal("legacy schema accepted new observation")
	}
	s.NDCGGain = "linear"
	if validateEvaluationOptions(s, 3) == nil {
		t.Fatal("ANN accepted unrelated gain setting")
	}
	for _, bounds := range []string{"[[3,2]]", "[[0]]", "[[0,9,99]]", "[null]", "[[0,9],[9,10]]"} {
		bad := []byte(`{"id":"q","exact_ids":["1"],"allowed_id_ranges":` + bounds + "}\n")
		if _, err := parseQueries(bad, scenario{Oracle: "ann_recall"}); err == nil {
			t.Fatalf("invalid allowed range accepted: %s", bounds)
		}
	}
	if _, err := parseQueries([]byte(`{"id":"q","exact_ids":["1"],"allowed_id_ranges":[[0,9]],"unknown":true}`), scenario{Oracle: "ann_recall"}); err == nil {
		t.Fatal("unknown query field accepted")
	}
}
