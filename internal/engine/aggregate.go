package engine

import (
	"fmt"
	"io"
	"strings"

	"github.com/juliocesar04-code/proj-1/internal/sql"
	"github.com/juliocesar04-code/proj-1/internal/types"
)

// aggregateCall is one COUNT, SUM, AVG, MIN or MAX in the query.
type aggregateCall struct {
	call     *sql.FuncCall
	name     string
	arg      sql.Expr // nil for COUNT(*)
	distinct bool
}

// aggregateOp groups rows and folds each group into a single row. Groups are
// kept in a hash table and emitted in the order they were first seen, which
// keeps output stable without an ORDER BY.
type aggregateOp struct {
	child  Operator
	groups []sql.Expr
	calls  []aggregateCall
	schema Schema
	eval   *evaluator

	rows []Row
	pos  int
	done bool
}

func newAggregate(child Operator, groups []sql.Expr, calls []aggregateCall, schema Schema) *aggregateOp {
	return &aggregateOp{
		child:  child,
		groups: groups,
		calls:  calls,
		schema: schema,
		eval:   newEvaluator(child.Schema()),
	}
}

func (a *aggregateOp) Open() error {
	a.rows, a.pos, a.done = nil, 0, false
	return a.child.Open()
}

func (a *aggregateOp) Next() (Row, error) {
	if !a.done {
		if err := a.compute(); err != nil {
			return nil, err
		}
		a.done = true
	}
	if a.pos >= len(a.rows) {
		return nil, io.EOF
	}
	row := a.rows[a.pos]
	a.pos++
	return row, nil
}

type groupState struct {
	keys  []types.Value
	accs  []*accumulator
	order int
}

func (a *aggregateOp) compute() error {
	groups := map[string]*groupState{}
	var order []*groupState

	for {
		row, err := a.child.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		keys := make([]types.Value, len(a.groups))
		for i, expr := range a.groups {
			value, err := a.eval.eval(expr, row)
			if err != nil {
				return err
			}
			keys[i] = value
		}

		hash := string(types.EncodeKeys(keys...))
		state, ok := groups[hash]
		if !ok {
			state = &groupState{keys: keys, accs: make([]*accumulator, len(a.calls))}
			for i := range state.accs {
				state.accs[i] = newAccumulator(a.calls[i])
			}
			groups[hash] = state
			order = append(order, state)
		}

		for i, call := range a.calls {
			var value types.Value
			if call.arg != nil {
				value, err = a.eval.eval(call.arg, row)
				if err != nil {
					return err
				}
			}
			if err := state.accs[i].add(value, call.arg == nil); err != nil {
				return err
			}
		}
	}

	// An aggregate without GROUP BY always produces exactly one row.
	if len(order) == 0 && len(a.groups) == 0 {
		state := &groupState{accs: make([]*accumulator, len(a.calls))}
		for i := range state.accs {
			state.accs[i] = newAccumulator(a.calls[i])
		}
		order = append(order, state)
	}

	a.rows = make([]Row, len(order))
	for i, state := range order {
		row := make(Row, 0, len(state.keys)+len(state.accs))
		row = append(row, state.keys...)
		for _, acc := range state.accs {
			row = append(row, acc.result())
		}
		a.rows[i] = row
	}
	return nil
}

func (a *aggregateOp) Close() error         { a.rows = nil; return a.child.Close() }
func (a *aggregateOp) Schema() Schema       { return a.schema }
func (a *aggregateOp) Children() []Operator { return []Operator{a.child} }

func (a *aggregateOp) Explain() string {
	parts := make([]string, len(a.calls))
	for i, call := range a.calls {
		parts[i] = call.call.String()
	}
	if len(a.groups) == 0 {
		return "Aggregate: " + strings.Join(parts, ", ")
	}
	keys := make([]string, len(a.groups))
	for i, expr := range a.groups {
		keys[i] = expr.String()
	}
	return fmt.Sprintf("Hash Aggregate: %s group by %s",
		strings.Join(parts, ", "), strings.Join(keys, ", "))
}

// accumulator folds the values of one aggregate over one group.
type accumulator struct {
	name     string
	distinct bool
	seen     map[string]bool

	count    int64
	intSum   int64
	floatSum float64
	allInt   bool
	any      bool
	extreme  types.Value
}

func newAccumulator(call aggregateCall) *accumulator {
	acc := &accumulator{name: call.name, distinct: call.distinct, allInt: true}
	if call.distinct {
		acc.seen = map[string]bool{}
	}
	return acc
}

func (a *accumulator) add(value types.Value, star bool) error {
	if star {
		a.count++
		return nil
	}
	if value.IsNull() {
		return nil // aggregates ignore NULL, like standard SQL
	}
	if a.distinct {
		key := string(types.EncodeKey(value))
		if a.seen[key] {
			return nil
		}
		a.seen[key] = true
	}
	a.count++

	switch a.name {
	case "SUM", "AVG":
		number, ok := value.Float64()
		if !ok {
			return fmt.Errorf("%s() expects numbers, got %s", a.name, value.T)
		}
		if value.T == types.Integer {
			a.intSum += value.I
		} else {
			a.allInt = false
		}
		a.floatSum += number
	case "MIN", "MAX":
		if !a.any {
			a.extreme = value
			break
		}
		cmp, ok := types.Compare(value, a.extreme)
		if !ok {
			return fmt.Errorf("%s() cannot compare %s with %s", a.name, value.T, a.extreme.T)
		}
		if (a.name == "MIN" && cmp < 0) || (a.name == "MAX" && cmp > 0) {
			a.extreme = value
		}
	}
	a.any = true
	return nil
}

func (a *accumulator) result() types.Value {
	switch a.name {
	case "COUNT":
		return types.NewInt(a.count)
	case "SUM":
		if !a.any {
			return types.NullValue
		}
		if a.allInt {
			return types.NewInt(a.intSum)
		}
		return types.NewFloat(a.floatSum)
	case "AVG":
		if a.count == 0 {
			return types.NullValue
		}
		return types.NewFloat(a.floatSum / float64(a.count))
	case "MIN", "MAX":
		if !a.any {
			return types.NullValue
		}
		return a.extreme
	}
	return types.NullValue
}
