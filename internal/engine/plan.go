package engine

import (
	"fmt"
	"io"
	"strings"

	"github.com/juliocesar04-code/proj-1/internal/sql"
	"github.com/juliocesar04-code/proj-1/internal/storage"
	"github.com/juliocesar04-code/proj-1/internal/types"
)

// planner turns a parsed SELECT into a tree of operators.
//
// The rules it applies are the classic ones: predicates are split on AND and
// pushed as close to the scans as possible, a scan becomes an index or primary
// key range whenever a predicate allows it, an equality join becomes a hash
// join, and an ORDER BY that matches the scan order drops the sort.
type planner struct {
	tx      *storage.Tx
	catalog *Catalog
}

// singleRow feeds one empty row, which is what a SELECT without FROM needs.
type singleRow struct{ done bool }

func (s *singleRow) Open() error { s.done = false; return nil }

func (s *singleRow) Next() (Row, error) {
	if s.done {
		return nil, io.EOF
	}
	s.done = true
	return Row{}, nil
}

func (s *singleRow) Close() error         { return nil }
func (s *singleRow) Schema() Schema       { return nil }
func (s *singleRow) Children() []Operator { return nil }
func (s *singleRow) Explain() string      { return "Result" }

// accessHint asks a scan to produce rows already ordered by a column.
type accessHint struct {
	active     bool
	column     string
	descending bool
}

func (p *planner) planSelect(stmt *sql.Select) (Operator, error) {
	if stmt.From == nil {
		return p.planWithoutFrom(stmt)
	}

	base, ok := p.catalog.Table(stmt.From.Name)
	if !ok {
		return nil, fmt.Errorf("no such table: %s", stmt.From.Name)
	}

	sources := []sql.TableRef{*stmt.From}
	tables := []*Table{base}
	full := tableSchema(base, stmt.From.Ident())
	for _, join := range stmt.Joins {
		joined, ok := p.catalog.Table(join.Table.Name)
		if !ok {
			return nil, fmt.Errorf("no such table: %s", join.Table.Name)
		}
		sources = append(sources, join.Table)
		tables = append(tables, joined)
		full = append(full, tableSchema(joined, join.Table.Ident())...)
	}

	if containsAggregate(stmt.Where) {
		return nil, fmt.Errorf("aggregate functions are not allowed in WHERE")
	}

	// Split the WHERE clause and route each conjunct to the deepest place it
	// can be evaluated.
	pushed := make(map[string][]sql.Expr)
	var residual []sql.Expr
	for _, conjunct := range splitConjuncts(stmt.Where) {
		referenced, err := referencedTables(conjunct, full)
		if err != nil {
			return nil, err
		}
		if len(referenced) == 1 {
			for ident := range referenced {
				pushed[ident] = append(pushed[ident], conjunct)
			}
			continue
		}
		residual = append(residual, conjunct)
	}

	hint := p.orderHint(stmt, full)

	op, err := p.planAccess(tables[0], sources[0], pushed[strings.ToLower(sources[0].Ident())], hint)
	if err != nil {
		return nil, err
	}
	ordered := hint.active && isOrderedScan(op, hint)

	for i, join := range stmt.Joins {
		right, err := p.planAccess(tables[i+1], sources[i+1], pushed[strings.ToLower(sources[i+1].Ident())], accessHint{})
		if err != nil {
			return nil, err
		}
		op = makeJoin(op, right, join.On, full)
		ordered = false
	}

	if len(residual) > 0 {
		op = newFilter(op, combineConjuncts(residual), nil)
	}
	return p.planProjection(stmt, op, full, ordered)
}

func (p *planner) planWithoutFrom(stmt *sql.Select) (Operator, error) {
	if stmt.Where != nil || len(stmt.Joins) > 0 || len(stmt.GroupBy) > 0 {
		return nil, fmt.Errorf("a query without FROM cannot have WHERE, JOIN or GROUP BY")
	}
	return p.planProjection(stmt, &singleRow{}, nil, false)
}

// orderHint decides whether the scan can satisfy the ORDER BY on its own.
func (p *planner) orderHint(stmt *sql.Select, full Schema) accessHint {
	if len(stmt.Joins) > 0 || len(stmt.OrderBy) != 1 || len(stmt.GroupBy) > 0 || stmt.Distinct {
		return accessHint{}
	}
	for _, column := range stmt.Columns {
		if column.Expr != nil && containsAggregate(column.Expr) {
			return accessHint{}
		}
	}
	ref, ok := stmt.OrderBy[0].Expr.(*sql.ColumnRef)
	if !ok {
		return accessHint{}
	}
	index, err := full.Resolve(ref.Table, ref.Name)
	if err != nil || !strings.EqualFold(full[index].Table, stmt.From.Ident()) {
		return accessHint{}
	}
	return accessHint{active: true, column: full[index].Name, descending: stmt.OrderBy[0].Descending}
}

func isOrderedScan(op Operator, hint accessHint) bool {
	switch scan := op.(type) {
	case *pkScan:
		return strings.EqualFold(scan.table.Columns[scan.table.RowIDColumn].Name, hint.column) &&
			scan.descending == hint.descending
	case *indexScan:
		return strings.EqualFold(scan.index.Column, hint.column) && scan.descending == hint.descending
	case *filterOp:
		return isOrderedScan(scan.child, hint)
	}
	return false
}

// planAccess picks the access path for one table and wraps whatever predicate
// could not be turned into a range in a filter.
func (p *planner) planAccess(table *Table, ref sql.TableRef, conjuncts []sql.Expr, hint accessHint) (Operator, error) {
	scan, leftover := p.chooseScan(table, ref.Ident(), conjuncts, hint)
	if len(leftover) > 0 {
		return newFilter(scan, combineConjuncts(leftover), nil), nil
	}
	return scan, nil
}

// columnBounds accumulates the interval implied by the predicates on one
// column, together with the predicates that produced it.
type columnBounds struct {
	rng  scanRange
	used map[int]bool
}

func (b *columnBounds) tightenLow(value types.Value, closed bool, conjunct int) {
	if b.rng.hasLow {
		cmp, ok := types.Compare(value, b.rng.low)
		if !ok {
			return
		}
		if cmp < 0 || (cmp == 0 && closed) {
			b.used[conjunct] = true
			return
		}
	}
	b.rng.hasLow, b.rng.low, b.rng.lowClosed = true, value, closed
	b.used[conjunct] = true
}

func (b *columnBounds) tightenHigh(value types.Value, closed bool, conjunct int) {
	if b.rng.hasHigh {
		cmp, ok := types.Compare(value, b.rng.high)
		if !ok {
			return
		}
		if cmp > 0 || (cmp == 0 && closed) {
			b.used[conjunct] = true
			return
		}
	}
	b.rng.hasHigh, b.rng.high, b.rng.highClosed = true, value, closed
	b.used[conjunct] = true
}

func (b *columnBounds) score() int {
	switch {
	case b.rng.hasLow && b.rng.hasHigh && b.rng.lowClosed && b.rng.highClosed && b.rng.low.Equal(b.rng.high):
		return 3
	case b.rng.hasLow && b.rng.hasHigh:
		return 2
	case b.rng.hasLow || b.rng.hasHigh:
		return 1
	default:
		return 0
	}
}

func (p *planner) chooseScan(table *Table, alias string, conjuncts []sql.Expr, hint accessHint) (Operator, []sql.Expr) {
	schema := tableSchema(table, alias)
	bounds := map[string]*columnBounds{}

	for i, conjunct := range conjuncts {
		column, op, value, ok := rangePredicate(conjunct, schema, table)
		if !ok {
			continue
		}
		key := strings.ToLower(column)
		entry, exists := bounds[key]
		if !exists {
			entry = &columnBounds{used: map[int]bool{}}
			bounds[key] = entry
		}
		switch op {
		case "=":
			entry.tightenLow(value, true, i)
			entry.tightenHigh(value, true, i)
		case ">":
			entry.tightenLow(value, false, i)
		case ">=":
			entry.tightenLow(value, true, i)
		case "<":
			entry.tightenHigh(value, false, i)
		case "<=":
			entry.tightenHigh(value, true, i)
		}
	}

	best := -1
	var bestColumn string
	var bestEntry *columnBounds
	var bestIndex *Index
	usePK := false

	for column, entry := range bounds {
		score := entry.score()
		if score == 0 {
			continue
		}
		isPK := table.RowIDColumn >= 0 && strings.EqualFold(table.Columns[table.RowIDColumn].Name, column)
		index := table.IndexOn(column)
		if !isPK && index == nil {
			continue
		}
		if isPK {
			score++ // reading the table directly beats a second lookup
		}
		if score > best {
			best, bestColumn, bestEntry, bestIndex, usePK = score, column, entry, index, isPK
		}
	}

	if best < 0 && hint.active {
		// No predicate to narrow the scan, but the ORDER BY can ride the
		// index and save a sort.
		if table.RowIDColumn >= 0 && strings.EqualFold(table.Columns[table.RowIDColumn].Name, hint.column) {
			return newPKScan(p.tx, table, alias, scanRange{}, hint.descending), conjuncts
		}
		if index := table.IndexOn(hint.column); index != nil {
			return newIndexScan(p.tx, table, index, alias, scanRange{}, hint.descending), conjuncts
		}
	}

	if best < 0 {
		return newSeqScan(p.tx, table, alias), conjuncts
	}

	descending := hint.active && strings.EqualFold(hint.column, bestColumn) && hint.descending
	leftover := make([]sql.Expr, 0, len(conjuncts))
	for i, conjunct := range conjuncts {
		if !bestEntry.used[i] {
			leftover = append(leftover, conjunct)
		}
	}

	if usePK {
		return newPKScan(p.tx, table, alias, bestEntry.rng, descending), leftover
	}
	return newIndexScan(p.tx, table, bestIndex, alias, bestEntry.rng, descending), leftover
}

// rangePredicate recognises `column <op> constant` in either order and returns
// the bound, already cast to the declared type of the column so that the
// encoded key lands in the right place.
func rangePredicate(expr sql.Expr, schema Schema, table *Table) (column, op string, value types.Value, ok bool) {
	binary, isBinary := expr.(*sql.BinaryExpr)
	if !isBinary {
		return "", "", types.NullValue, false
	}
	switch binary.Op {
	case "=", "<", "<=", ">", ">=":
	default:
		return "", "", types.NullValue, false
	}

	ref, isRef := binary.Left.(*sql.ColumnRef)
	other := binary.Right
	operator := binary.Op
	if !isRef {
		ref, isRef = binary.Right.(*sql.ColumnRef)
		other = binary.Left
		operator = mirrorOperator(binary.Op)
	}
	if !isRef {
		return "", "", types.NullValue, false
	}
	if _, err := schema.Resolve(ref.Table, ref.Name); err != nil {
		return "", "", types.NullValue, false
	}

	constant, isConstant := constantValue(other)
	if !isConstant || constant.IsNull() {
		return "", "", types.NullValue, false
	}
	position := table.ColumnIndex(ref.Name)
	if position < 0 {
		return "", "", types.NullValue, false
	}
	casted, err := types.Cast(constant, table.Columns[position].Type)
	if err != nil {
		return "", "", types.NullValue, false
	}
	return table.Columns[position].Name, operator, casted, true
}

func mirrorOperator(op string) string {
	switch op {
	case "<":
		return ">"
	case "<=":
		return ">="
	case ">":
		return "<"
	case ">=":
		return "<="
	default:
		return op
	}
}

// constantValue folds an expression that does not read any column.
func constantValue(expr sql.Expr) (types.Value, bool) {
	constant := true
	walkExpr(expr, func(node sql.Expr) bool {
		switch node.(type) {
		case *sql.ColumnRef:
			constant = false
		case *sql.FuncCall:
			constant = false
		}
		return constant
	})
	if !constant {
		return types.NullValue, false
	}
	value, err := newEvaluator(nil).eval(expr, nil)
	if err != nil {
		return types.NullValue, false
	}
	return value, true
}

// makeJoin prefers a hash join whenever the ON clause has an equality that
// spans the two sides.
func makeJoin(left, right Operator, on sql.Expr, full Schema) Operator {
	conjuncts := splitConjuncts(on)
	leftSchema, rightSchema := left.Schema(), right.Schema()

	for i, conjunct := range conjuncts {
		binary, ok := conjunct.(*sql.BinaryExpr)
		if !ok || binary.Op != "=" {
			continue
		}
		leftKey, rightKey, ok := orientJoinKeys(binary, leftSchema, rightSchema)
		if !ok {
			continue
		}
		rest := make([]sql.Expr, 0, len(conjuncts)-1)
		rest = append(rest, conjuncts[:i]...)
		rest = append(rest, conjuncts[i+1:]...)
		return newHashJoin(left, right, leftKey, rightKey, combineConjuncts(rest))
	}
	return newNestedLoopJoin(left, right, on)
}

// orientJoinKeys puts the side that reads the left input first.
func orientJoinKeys(binary *sql.BinaryExpr, leftSchema, rightSchema Schema) (sql.Expr, sql.Expr, bool) {
	if resolvesAgainst(binary.Left, leftSchema) && resolvesAgainst(binary.Right, rightSchema) {
		return binary.Left, binary.Right, true
	}
	if resolvesAgainst(binary.Right, leftSchema) && resolvesAgainst(binary.Left, rightSchema) {
		return binary.Right, binary.Left, true
	}
	return nil, nil, false
}

// resolvesAgainst reports whether every column of an expression belongs to the
// given schema.
func resolvesAgainst(expr sql.Expr, schema Schema) bool {
	sawColumn := false
	resolves := true
	walkExpr(expr, func(node sql.Expr) bool {
		ref, ok := node.(*sql.ColumnRef)
		if !ok {
			return resolves
		}
		sawColumn = true
		if _, err := schema.Resolve(ref.Table, ref.Name); err != nil {
			resolves = false
		}
		return false
	})
	return sawColumn && resolves
}

// planProjection builds everything above the joins: grouping, HAVING, sorting,
// the select list itself, DISTINCT and LIMIT.
func (p *planner) planProjection(stmt *sql.Select, input Operator, full Schema, ordered bool) (Operator, error) {
	outputs, names, err := expandSelectList(stmt.Columns, input.Schema())
	if err != nil {
		return nil, err
	}

	orderTerms := rewriteOrderBy(stmt.OrderBy, outputs, names)

	postAggregate := append(append([]sql.Expr{}, outputs...), stmt.Having)
	for _, term := range orderTerms {
		postAggregate = append(postAggregate, term.Expr)
	}

	op := input
	var substitutions map[sql.Expr]int
	calls := collectAggregates(postAggregate, input.Schema())

	if len(calls) > 0 || len(stmt.GroupBy) > 0 {
		schema := aggregateSchema(stmt.GroupBy, calls, input.Schema())
		substitutions = buildSubstitutions(postAggregate, stmt.GroupBy, calls, input.Schema())
		if err := checkGrouped(postAggregate, substitutions); err != nil {
			return nil, err
		}
		op = newAggregate(op, stmt.GroupBy, calls, schema)
		ordered = false
	} else if stmt.Having != nil {
		return nil, fmt.Errorf("HAVING requires GROUP BY or an aggregate function")
	}

	if stmt.Having != nil {
		op = newFilter(op, stmt.Having, substitutions)
	}
	if len(orderTerms) > 0 && !ordered {
		op = newSort(op, orderTerms, substitutions)
	}

	op = newProjection(op, outputs, projectionSchema(outputs, names, op.Schema()), substitutions)
	if stmt.Distinct {
		op = newDistinct(op)
	}

	limit, offset, err := limitValues(stmt)
	if err != nil {
		return nil, err
	}
	if limit >= 0 || offset > 0 {
		op = newLimit(op, limit, offset)
	}
	return op, nil
}

func expandSelectList(columns []sql.ResultColumn, schema Schema) ([]sql.Expr, []string, error) {
	var exprs []sql.Expr
	var names []string

	for _, column := range columns {
		if !column.Star {
			exprs = append(exprs, column.Expr)
			names = append(names, outputName(column))
			continue
		}
		matched := false
		for _, info := range schema {
			if column.StarTable != "" && !strings.EqualFold(info.Table, column.StarTable) {
				continue
			}
			matched = true
			exprs = append(exprs, &sql.ColumnRef{Table: info.Table, Name: info.Name})
			names = append(names, info.Name)
		}
		if !matched {
			return nil, nil, fmt.Errorf("no such table in this query: %s", column.StarTable)
		}
	}
	if len(exprs) == 0 {
		return nil, nil, fmt.Errorf("the select list is empty")
	}
	return exprs, names, nil
}

func outputName(column sql.ResultColumn) string {
	if column.Alias != "" {
		return column.Alias
	}
	if ref, ok := column.Expr.(*sql.ColumnRef); ok {
		return ref.Name
	}
	return column.Expr.String()
}

// rewriteOrderBy lets ORDER BY refer to a select list alias.
func rewriteOrderBy(terms []sql.OrderTerm, outputs []sql.Expr, names []string) []sql.OrderTerm {
	rewritten := make([]sql.OrderTerm, len(terms))
	copy(rewritten, terms)
	for i, term := range rewritten {
		ref, ok := term.Expr.(*sql.ColumnRef)
		if !ok || ref.Table != "" {
			continue
		}
		for j, name := range names {
			if strings.EqualFold(name, ref.Name) {
				rewritten[i].Expr = outputs[j]
				break
			}
		}
	}
	return rewritten
}

func aggregateSchema(groups []sql.Expr, calls []aggregateCall, input Schema) Schema {
	schema := make(Schema, 0, len(groups)+len(calls))
	for _, group := range groups {
		if ref, ok := group.(*sql.ColumnRef); ok {
			if index, err := input.Resolve(ref.Table, ref.Name); err == nil {
				schema = append(schema, input[index])
				continue
			}
		}
		schema = append(schema, ColumnInfo{Name: group.String()})
	}
	for _, call := range calls {
		schema = append(schema, ColumnInfo{Name: call.call.String()})
	}
	return schema
}

// buildSubstitutions maps the expressions already computed by the aggregate
// onto their position in its output row.
func buildSubstitutions(exprs []sql.Expr, groups []sql.Expr, calls []aggregateCall, input Schema) map[sql.Expr]int {
	substitutions := map[sql.Expr]int{}
	for _, expr := range exprs {
		walkExpr(expr, func(node sql.Expr) bool {
			for i, group := range groups {
				if exprEqual(node, group, input) {
					substitutions[node] = i
					return false
				}
			}
			for i, call := range calls {
				if exprEqual(node, call.call, input) {
					substitutions[node] = len(groups) + i
					return false
				}
			}
			return true
		})
	}
	return substitutions
}

// checkGrouped rejects a column that is neither grouped nor aggregated, which
// would otherwise pick an arbitrary row of the group.
func checkGrouped(exprs []sql.Expr, substitutions map[sql.Expr]int) error {
	var failure error
	for _, expr := range exprs {
		walkExpr(expr, func(node sql.Expr) bool {
			if _, ok := substitutions[node]; ok {
				return false
			}
			if ref, ok := node.(*sql.ColumnRef); ok && failure == nil {
				failure = fmt.Errorf("column %s must appear in GROUP BY or be used in an aggregate function", ref)
			}
			return failure == nil
		})
	}
	return failure
}

func projectionSchema(exprs []sql.Expr, names []string, input Schema) Schema {
	schema := make(Schema, len(exprs))
	for i, expr := range exprs {
		schema[i] = ColumnInfo{Name: names[i]}
		if ref, ok := expr.(*sql.ColumnRef); ok {
			if index, err := input.Resolve(ref.Table, ref.Name); err == nil {
				schema[i].Table = input[index].Table
				schema[i].Type = input[index].Type
			}
		}
	}
	return schema
}

func limitValues(stmt *sql.Select) (limit, offset int64, err error) {
	limit = -1
	if stmt.Limit != nil {
		value, ok := constantValue(stmt.Limit)
		if !ok || value.T != types.Integer || value.I < 0 {
			return 0, 0, fmt.Errorf("LIMIT expects a non negative integer")
		}
		limit = value.I
	}
	if stmt.Offset != nil {
		value, ok := constantValue(stmt.Offset)
		if !ok || value.T != types.Integer || value.I < 0 {
			return 0, 0, fmt.Errorf("OFFSET expects a non negative integer")
		}
		offset = value.I
	}
	return limit, offset, nil
}

// ExplainPlan renders an operator tree.
func ExplainPlan(op Operator) []string {
	var lines []string
	var walk func(Operator, int)
	walk = func(node Operator, depth int) {
		lines = append(lines, strings.Repeat("  ", depth)+node.Explain())
		for _, child := range node.Children() {
			walk(child, depth+1)
		}
	}
	walk(op, 0)
	return lines
}
