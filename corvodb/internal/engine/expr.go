package engine

import (
	"fmt"
	"math"
	"strings"

	"github.com/juliocesar04-code/proj-1/corvodb/internal/sql"
	"github.com/juliocesar04-code/proj-1/corvodb/internal/types"
)

// Row is one tuple flowing through the operator tree.
type Row []types.Value

// ColumnInfo describes one column of an operator's output.
type ColumnInfo struct {
	Table string
	Name  string
	Type  types.Type
}

// Schema is the shape of an operator's output.
type Schema []ColumnInfo

// Resolve finds the position of a column reference. An unqualified name that
// matches more than one table is rejected.
func (s Schema) Resolve(table, name string) (int, error) {
	found := -1
	for i, col := range s {
		if !strings.EqualFold(col.Name, name) {
			continue
		}
		if table != "" && !strings.EqualFold(col.Table, table) {
			continue
		}
		if found >= 0 {
			return -1, fmt.Errorf("column %q is ambiguous", name)
		}
		found = i
	}
	if found < 0 {
		if table != "" {
			return -1, fmt.Errorf("no such column: %s.%s", table, name)
		}
		return -1, fmt.Errorf("no such column: %s", name)
	}
	return found, nil
}

// evaluator turns expressions into values against a row.
//
// substitutions map expression nodes that were already computed by a lower
// operator (grouping keys and aggregates) onto their position in the row, so
// the projection above an aggregate does not evaluate them a second time.
type evaluator struct {
	schema        Schema
	substitutions map[sql.Expr]int
}

func newEvaluator(schema Schema) *evaluator { return &evaluator{schema: schema} }

func (e *evaluator) eval(expr sql.Expr, row Row) (types.Value, error) {
	if index, ok := e.substitutions[expr]; ok {
		return row[index], nil
	}

	switch node := expr.(type) {
	case *sql.Literal:
		return node.Value, nil

	case *sql.ColumnRef:
		index, err := e.schema.Resolve(node.Table, node.Name)
		if err != nil {
			return types.NullValue, err
		}
		return row[index], nil

	case *sql.UnaryExpr:
		operand, err := e.eval(node.Operand, row)
		if err != nil {
			return types.NullValue, err
		}
		return evalUnary(node.Op, operand)

	case *sql.BinaryExpr:
		return e.evalBinary(node, row)

	case *sql.IsNullExpr:
		operand, err := e.eval(node.Operand, row)
		if err != nil {
			return types.NullValue, err
		}
		return types.NewBool(operand.IsNull() != node.Negate), nil

	case *sql.InExpr:
		return e.evalIn(node, row)

	case *sql.BetweenExpr:
		return e.evalBetween(node, row)

	case *sql.FuncCall:
		if isAggregate(node.Name) {
			return types.NullValue, fmt.Errorf("%s() is only allowed in a select list, HAVING or ORDER BY", node.Name)
		}
		return e.evalFunc(node, row)
	}
	return types.NullValue, fmt.Errorf("cannot evaluate %s", expr)
}

func evalUnary(op string, operand types.Value) (types.Value, error) {
	switch op {
	case "NOT":
		if operand.IsNull() {
			return types.NullValue, nil
		}
		if operand.T != types.Boolean {
			return types.NullValue, fmt.Errorf("NOT expects a boolean, got %s", operand.T)
		}
		return types.NewBool(!operand.B), nil
	case "-":
		switch operand.T {
		case types.Null:
			return types.NullValue, nil
		case types.Integer:
			return types.NewInt(-operand.I), nil
		case types.Float:
			return types.NewFloat(-operand.F), nil
		}
		return types.NullValue, fmt.Errorf("cannot negate %s", operand.T)
	}
	return types.NullValue, fmt.Errorf("unknown unary operator %q", op)
}

func (e *evaluator) evalBinary(node *sql.BinaryExpr, row Row) (types.Value, error) {
	// AND and OR short circuit, and follow three valued logic.
	if node.Op == "AND" || node.Op == "OR" {
		left, err := e.eval(node.Left, row)
		if err != nil {
			return types.NullValue, err
		}
		if node.Op == "AND" && left.T == types.Boolean && !left.B {
			return types.NewBool(false), nil
		}
		if node.Op == "OR" && left.T == types.Boolean && left.B {
			return types.NewBool(true), nil
		}
		right, err := e.eval(node.Right, row)
		if err != nil {
			return types.NullValue, err
		}
		if left.IsNull() || right.IsNull() {
			return types.NullValue, nil
		}
		if left.T != types.Boolean || right.T != types.Boolean {
			return types.NullValue, fmt.Errorf("%s expects booleans", node.Op)
		}
		if node.Op == "AND" {
			return types.NewBool(left.B && right.B), nil
		}
		return types.NewBool(left.B || right.B), nil
	}

	left, err := e.eval(node.Left, row)
	if err != nil {
		return types.NullValue, err
	}
	right, err := e.eval(node.Right, row)
	if err != nil {
		return types.NullValue, err
	}
	return applyBinary(node.Op, left, right)
}

func applyBinary(op string, left, right types.Value) (types.Value, error) {
	switch op {
	case "=", "!=", "<", "<=", ">", ">=":
		if left.IsNull() || right.IsNull() {
			return types.NullValue, nil
		}
		result, ok := types.Compare(left, right)
		if !ok {
			return types.NullValue, nil
		}
		switch op {
		case "=":
			return types.NewBool(result == 0), nil
		case "!=":
			return types.NewBool(result != 0), nil
		case "<":
			return types.NewBool(result < 0), nil
		case "<=":
			return types.NewBool(result <= 0), nil
		case ">":
			return types.NewBool(result > 0), nil
		default:
			return types.NewBool(result >= 0), nil
		}

	case "LIKE", "NOT LIKE":
		if left.IsNull() || right.IsNull() {
			return types.NullValue, nil
		}
		if left.T != types.Text || right.T != types.Text {
			return types.NullValue, fmt.Errorf("LIKE expects text operands")
		}
		return types.NewBool(likeMatch(right.S, left.S) == (op == "LIKE")), nil

	case "||":
		if left.IsNull() || right.IsNull() {
			return types.NullValue, nil
		}
		return types.NewText(left.String() + right.String()), nil

	case "+", "-", "*", "/", "%":
		return arithmetic(op, left, right)
	}
	return types.NullValue, fmt.Errorf("unknown operator %q", op)
}

func arithmetic(op string, left, right types.Value) (types.Value, error) {
	if left.IsNull() || right.IsNull() {
		return types.NullValue, nil
	}
	leftNum, leftOK := left.Float64()
	rightNum, rightOK := right.Float64()
	if !leftOK || !rightOK {
		return types.NullValue, fmt.Errorf("cannot apply %q to %s and %s", op, left.T, right.T)
	}

	if left.T == types.Integer && right.T == types.Integer {
		a, b := left.I, right.I
		switch op {
		case "+":
			return types.NewInt(a + b), nil
		case "-":
			return types.NewInt(a - b), nil
		case "*":
			return types.NewInt(a * b), nil
		case "/":
			if b == 0 {
				return types.NullValue, nil
			}
			return types.NewInt(a / b), nil
		case "%":
			if b == 0 {
				return types.NullValue, nil
			}
			return types.NewInt(a % b), nil
		}
	}

	switch op {
	case "+":
		return types.NewFloat(leftNum + rightNum), nil
	case "-":
		return types.NewFloat(leftNum - rightNum), nil
	case "*":
		return types.NewFloat(leftNum * rightNum), nil
	case "/":
		if rightNum == 0 {
			return types.NullValue, nil
		}
		return types.NewFloat(leftNum / rightNum), nil
	case "%":
		if rightNum == 0 {
			return types.NullValue, nil
		}
		return types.NewFloat(math.Mod(leftNum, rightNum)), nil
	}
	return types.NullValue, fmt.Errorf("unknown operator %q", op)
}

func (e *evaluator) evalIn(node *sql.InExpr, row Row) (types.Value, error) {
	operand, err := e.eval(node.Operand, row)
	if err != nil {
		return types.NullValue, err
	}
	if operand.IsNull() {
		return types.NullValue, nil
	}

	sawNull := false
	for _, item := range node.List {
		candidate, err := e.eval(item, row)
		if err != nil {
			return types.NullValue, err
		}
		if candidate.IsNull() {
			sawNull = true
			continue
		}
		if operand.Equal(candidate) {
			return types.NewBool(!node.Negate), nil
		}
	}
	if sawNull {
		return types.NullValue, nil
	}
	return types.NewBool(node.Negate), nil
}

func (e *evaluator) evalBetween(node *sql.BetweenExpr, row Row) (types.Value, error) {
	operand, err := e.eval(node.Operand, row)
	if err != nil {
		return types.NullValue, err
	}
	low, err := e.eval(node.Low, row)
	if err != nil {
		return types.NullValue, err
	}
	high, err := e.eval(node.High, row)
	if err != nil {
		return types.NullValue, err
	}

	atLeast, err := applyBinary(">=", operand, low)
	if err != nil {
		return types.NullValue, err
	}
	atMost, err := applyBinary("<=", operand, high)
	if err != nil {
		return types.NullValue, err
	}
	if atLeast.IsNull() || atMost.IsNull() {
		return types.NullValue, nil
	}
	inside := atLeast.B && atMost.B
	return types.NewBool(inside != node.Negate), nil
}

func (e *evaluator) evalFunc(node *sql.FuncCall, row Row) (types.Value, error) {
	args := make([]types.Value, len(node.Args))
	for i, arg := range node.Args {
		value, err := e.eval(arg, row)
		if err != nil {
			return types.NullValue, err
		}
		args[i] = value
	}
	return callScalar(node.Name, args)
}

func callScalar(name string, args []types.Value) (types.Value, error) {
	arity := func(want int) error {
		if len(args) != want {
			return fmt.Errorf("%s() takes %d argument(s), got %d", name, want, len(args))
		}
		return nil
	}

	switch name {
	case "LOWER", "UPPER", "LENGTH", "TRIM":
		if err := arity(1); err != nil {
			return types.NullValue, err
		}
		if args[0].IsNull() {
			return types.NullValue, nil
		}
		text := args[0].String()
		switch name {
		case "LOWER":
			return types.NewText(strings.ToLower(text)), nil
		case "UPPER":
			return types.NewText(strings.ToUpper(text)), nil
		case "TRIM":
			return types.NewText(strings.TrimSpace(text)), nil
		default:
			return types.NewInt(int64(len([]rune(text)))), nil
		}

	case "ABS":
		if err := arity(1); err != nil {
			return types.NullValue, err
		}
		switch args[0].T {
		case types.Null:
			return types.NullValue, nil
		case types.Integer:
			if args[0].I < 0 {
				return types.NewInt(-args[0].I), nil
			}
			return args[0], nil
		case types.Float:
			return types.NewFloat(math.Abs(args[0].F)), nil
		}
		return types.NullValue, fmt.Errorf("ABS() expects a number")

	case "ROUND":
		if len(args) != 1 && len(args) != 2 {
			return types.NullValue, fmt.Errorf("ROUND() takes 1 or 2 arguments")
		}
		if args[0].IsNull() {
			return types.NullValue, nil
		}
		number, ok := args[0].Float64()
		if !ok {
			return types.NullValue, fmt.Errorf("ROUND() expects a number")
		}
		digits := int64(0)
		if len(args) == 2 {
			if args[1].T != types.Integer {
				return types.NullValue, fmt.Errorf("ROUND() expects an integer precision")
			}
			digits = args[1].I
		}
		scale := math.Pow(10, float64(digits))
		return types.NewFloat(math.Round(number*scale) / scale), nil

	case "COALESCE":
		if len(args) == 0 {
			return types.NullValue, fmt.Errorf("COALESCE() needs at least one argument")
		}
		for _, arg := range args {
			if !arg.IsNull() {
				return arg, nil
			}
		}
		return types.NullValue, nil

	case "IFNULL":
		if err := arity(2); err != nil {
			return types.NullValue, err
		}
		if args[0].IsNull() {
			return args[1], nil
		}
		return args[0], nil

	case "SUBSTR":
		if len(args) != 2 && len(args) != 3 {
			return types.NullValue, fmt.Errorf("SUBSTR() takes 2 or 3 arguments")
		}
		if args[0].IsNull() {
			return types.NullValue, nil
		}
		runes := []rune(args[0].String())
		if args[1].T != types.Integer {
			return types.NullValue, fmt.Errorf("SUBSTR() expects an integer offset")
		}
		start := int(args[1].I)
		if start > 0 {
			start--
		} else if start < 0 {
			start = max(len(runes)+start, 0)
		}
		if start >= len(runes) {
			return types.NewText(""), nil
		}
		end := len(runes)
		if len(args) == 3 {
			if args[2].T != types.Integer {
				return types.NullValue, fmt.Errorf("SUBSTR() expects an integer length")
			}
			end = min(start+int(args[2].I), len(runes))
			if end < start {
				end = start
			}
		}
		return types.NewText(string(runes[start:end])), nil

	case "TYPEOF":
		if err := arity(1); err != nil {
			return types.NullValue, err
		}
		return types.NewText(strings.ToLower(args[0].T.String())), nil
	}
	return types.NullValue, fmt.Errorf("unknown function %s()", name)
}

// likeMatch implements SQL LIKE: % matches any run of characters, _ matches
// exactly one. Matching is case sensitive.
func likeMatch(pattern, text string) bool {
	p, t := []rune(pattern), []rune(text)
	pi, ti := 0, 0
	star, mark := -1, 0

	for ti < len(t) {
		switch {
		case pi < len(p) && (p[pi] == '_' || p[pi] == t[ti]):
			pi++
			ti++
		case pi < len(p) && p[pi] == '%':
			star = pi
			mark = ti
			pi++
		case star >= 0:
			pi = star + 1
			mark++
			ti = mark
		default:
			return false
		}
	}
	for pi < len(p) && p[pi] == '%' {
		pi++
	}
	return pi == len(p)
}

func isAggregate(name string) bool {
	switch name {
	case "COUNT", "SUM", "AVG", "MIN", "MAX":
		return true
	}
	return false
}
