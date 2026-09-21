package engine

import (
	"strings"

	"github.com/juliocesar04-code/proj-1/corvodb/internal/sql"
)

// walkExpr visits every node of an expression. When fn returns false the
// children of that node are skipped.
func walkExpr(expr sql.Expr, fn func(sql.Expr) bool) {
	if expr == nil || !fn(expr) {
		return
	}
	for _, child := range exprChildren(expr) {
		walkExpr(child, fn)
	}
}

func exprChildren(expr sql.Expr) []sql.Expr {
	switch node := expr.(type) {
	case *sql.UnaryExpr:
		return []sql.Expr{node.Operand}
	case *sql.BinaryExpr:
		return []sql.Expr{node.Left, node.Right}
	case *sql.IsNullExpr:
		return []sql.Expr{node.Operand}
	case *sql.InExpr:
		return append([]sql.Expr{node.Operand}, node.List...)
	case *sql.BetweenExpr:
		return []sql.Expr{node.Operand, node.Low, node.High}
	case *sql.FuncCall:
		return node.Args
	default:
		return nil
	}
}

// splitConjuncts breaks `a AND b AND c` into its parts so the planner can push
// each one down on its own.
func splitConjuncts(expr sql.Expr) []sql.Expr {
	if expr == nil {
		return nil
	}
	if binary, ok := expr.(*sql.BinaryExpr); ok && binary.Op == "AND" {
		return append(splitConjuncts(binary.Left), splitConjuncts(binary.Right)...)
	}
	return []sql.Expr{expr}
}

// combineConjuncts is the inverse of splitConjuncts.
func combineConjuncts(exprs []sql.Expr) sql.Expr {
	if len(exprs) == 0 {
		return nil
	}
	combined := exprs[0]
	for _, expr := range exprs[1:] {
		combined = &sql.BinaryExpr{Op: "AND", Left: combined, Right: expr}
	}
	return combined
}

// referencedTables reports which tables an expression reads, using the schema
// to resolve unqualified column names.
func referencedTables(expr sql.Expr, schema Schema) (map[string]bool, error) {
	tables := map[string]bool{}
	var failure error
	walkExpr(expr, func(node sql.Expr) bool {
		ref, ok := node.(*sql.ColumnRef)
		if !ok {
			return true
		}
		index, err := schema.Resolve(ref.Table, ref.Name)
		if err != nil {
			if failure == nil {
				failure = err
			}
			return false
		}
		tables[strings.ToLower(schema[index].Table)] = true
		return false
	})
	return tables, failure
}

// containsAggregate reports whether an expression calls an aggregate.
func containsAggregate(expr sql.Expr) bool {
	found := false
	walkExpr(expr, func(node sql.Expr) bool {
		if call, ok := node.(*sql.FuncCall); ok && isAggregate(call.Name) {
			found = true
			return false
		}
		return !found
	})
	return found
}

// collectAggregates gathers the distinct aggregate calls of a query.
func collectAggregates(exprs []sql.Expr, schema Schema) []aggregateCall {
	var calls []aggregateCall
	for _, expr := range exprs {
		walkExpr(expr, func(node sql.Expr) bool {
			call, ok := node.(*sql.FuncCall)
			if !ok || !isAggregate(call.Name) {
				return true
			}
			for _, existing := range calls {
				if exprEqual(existing.call, call, schema) {
					return false
				}
			}
			var arg sql.Expr
			if len(call.Args) > 0 {
				arg = call.Args[0]
			}
			calls = append(calls, aggregateCall{
				call: call, name: call.Name, arg: arg, distinct: call.Distinct,
			})
			return false
		})
	}
	return calls
}

// exprEqual compares two expressions structurally. Column references match
// when they resolve to the same column, so `name` and `u.name` are the same
// expression for grouping purposes.
func exprEqual(a, b sql.Expr, schema Schema) bool {
	switch left := a.(type) {
	case *sql.Literal:
		right, ok := b.(*sql.Literal)
		return ok && left.Value == right.Value

	case *sql.ColumnRef:
		right, ok := b.(*sql.ColumnRef)
		if !ok {
			return false
		}
		leftIndex, err := schema.Resolve(left.Table, left.Name)
		if err != nil {
			return false
		}
		rightIndex, err := schema.Resolve(right.Table, right.Name)
		return err == nil && leftIndex == rightIndex

	case *sql.UnaryExpr:
		right, ok := b.(*sql.UnaryExpr)
		return ok && left.Op == right.Op && exprEqual(left.Operand, right.Operand, schema)

	case *sql.BinaryExpr:
		right, ok := b.(*sql.BinaryExpr)
		return ok && left.Op == right.Op &&
			exprEqual(left.Left, right.Left, schema) &&
			exprEqual(left.Right, right.Right, schema)

	case *sql.IsNullExpr:
		right, ok := b.(*sql.IsNullExpr)
		return ok && left.Negate == right.Negate && exprEqual(left.Operand, right.Operand, schema)

	case *sql.InExpr:
		right, ok := b.(*sql.InExpr)
		if !ok || left.Negate != right.Negate || len(left.List) != len(right.List) {
			return false
		}
		if !exprEqual(left.Operand, right.Operand, schema) {
			return false
		}
		for i := range left.List {
			if !exprEqual(left.List[i], right.List[i], schema) {
				return false
			}
		}
		return true

	case *sql.BetweenExpr:
		right, ok := b.(*sql.BetweenExpr)
		return ok && left.Negate == right.Negate &&
			exprEqual(left.Operand, right.Operand, schema) &&
			exprEqual(left.Low, right.Low, schema) &&
			exprEqual(left.High, right.High, schema)

	case *sql.FuncCall:
		right, ok := b.(*sql.FuncCall)
		if !ok || left.Name != right.Name || left.Star != right.Star ||
			left.Distinct != right.Distinct || len(left.Args) != len(right.Args) {
			return false
		}
		for i := range left.Args {
			if !exprEqual(left.Args[i], right.Args[i], schema) {
				return false
			}
		}
		return true
	}
	return false
}
