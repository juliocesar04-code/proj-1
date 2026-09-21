package engine

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/juliocesar04-code/proj-1/internal/sql"
	"github.com/juliocesar04-code/proj-1/internal/storage"
	"github.com/juliocesar04-code/proj-1/internal/types"
)

// Operator is one node of the execution plan. The engine uses the iterator
// model: a plan is a tree of operators and rows are pulled one at a time from
// the root, so a query never materialises more than it has to.
//
// Next returns io.EOF once the stream is exhausted.
type Operator interface {
	Open() error
	Next() (Row, error)
	Close() error
	Schema() Schema
	Explain() string
	Children() []Operator
}

// rowSource is a scan that can also report the row id of the current row,
// which is what UPDATE and DELETE need to maintain indexes.
type rowSource interface {
	Operator
	RowID() int64
}

// --- sequential scan -------------------------------------------------------

type seqScan struct {
	tx     *storage.Tx
	table  *Table
	schema Schema

	cursor  *storage.Cursor
	started bool
	rowID   int64
}

func newSeqScan(tx *storage.Tx, table *Table, alias string) *seqScan {
	return &seqScan{tx: tx, table: table, schema: tableSchema(table, alias)}
}

func tableSchema(table *Table, alias string) Schema {
	if alias == "" {
		alias = table.Name
	}
	schema := make(Schema, len(table.Columns))
	for i, col := range table.Columns {
		schema[i] = ColumnInfo{Table: alias, Name: col.Name, Type: col.Type}
	}
	return schema
}

func (s *seqScan) Open() error {
	s.Close()
	s.cursor = s.tx.Tree(s.table.Root).Cursor()
	s.started = false
	return nil
}

func (s *seqScan) Next() (Row, error) {
	if s.cursor == nil {
		return nil, io.EOF
	}
	if s.started {
		s.cursor.Next()
	} else {
		s.cursor.First()
		s.started = true
	}
	if err := s.cursor.Err(); err != nil {
		return nil, err
	}
	if !s.cursor.Valid() {
		return nil, io.EOF
	}

	id, _, err := types.DecodeKey(s.cursor.Key())
	if err != nil {
		return nil, err
	}
	s.rowID = id.I

	raw, err := s.cursor.Value()
	if err != nil {
		return nil, err
	}
	row, err := types.DecodeRow(raw)
	if err != nil {
		return nil, err
	}
	return row, nil
}

func (s *seqScan) Close() error {
	if s.cursor != nil {
		s.cursor.Close()
		s.cursor = nil
	}
	return nil
}

func (s *seqScan) Schema() Schema       { return s.schema }
func (s *seqScan) RowID() int64         { return s.rowID }
func (s *seqScan) Children() []Operator { return nil }
func (s *seqScan) Explain() string      { return "Seq Scan on " + s.table.Name }

// --- index scan ------------------------------------------------------------

// scanRange is the interval an index scan walks.
type scanRange struct {
	hasLow, hasHigh       bool
	low, high             types.Value
	lowClosed, highClosed bool
}

func (r scanRange) describe(column string) string {
	switch {
	case r.hasLow && r.hasHigh && r.lowClosed && r.highClosed && r.low.Equal(r.high):
		return fmt.Sprintf("%s = %s", column, quoteValue(r.low))
	case r.hasLow && r.hasHigh:
		return fmt.Sprintf("%s %s %s and %s %s %s",
			column, gte(r.lowClosed), quoteValue(r.low),
			column, lte(r.highClosed), quoteValue(r.high))
	case r.hasLow:
		return fmt.Sprintf("%s %s %s", column, gte(r.lowClosed), quoteValue(r.low))
	case r.hasHigh:
		return fmt.Sprintf("%s %s %s", column, lte(r.highClosed), quoteValue(r.high))
	default:
		return "full index"
	}
}

func gte(closed bool) string {
	if closed {
		return ">="
	}
	return ">"
}

func lte(closed bool) string {
	if closed {
		return "<="
	}
	return "<"
}

func quoteValue(v types.Value) string {
	if v.T == types.Text {
		return "'" + strings.ReplaceAll(v.S, "'", "''") + "'"
	}
	return v.String()
}

// indexScan walks a secondary index and fetches the matching rows. Entries are
// keyed by (value, row id), so the scan decodes the value back out of the key
// to apply the interval bounds exactly.
type indexScan struct {
	tx         *storage.Tx
	table      *Table
	index      *Index
	schema     Schema
	bounds     scanRange
	descending bool

	cursor  *storage.Cursor
	rows    *storage.Tree
	started bool
	rowID   int64
}

func newIndexScan(tx *storage.Tx, table *Table, index *Index, alias string, bounds scanRange, descending bool) *indexScan {
	return &indexScan{
		tx: tx, table: table, index: index,
		schema: tableSchema(table, alias), bounds: bounds, descending: descending,
	}
}

func (s *indexScan) Open() error {
	s.Close()
	s.cursor = s.tx.Tree(s.index.Root).Cursor()
	s.rows = s.tx.Tree(s.table.Root)
	s.started = false
	return nil
}

// upperProbe is a key that sorts after every entry for the given value, which
// lets a reverse scan start on the last matching entry.
func upperProbe(v types.Value) []byte {
	return append(types.EncodeKey(v), 0xFF)
}

func (s *indexScan) seek() {
	if s.descending {
		if s.bounds.hasHigh {
			s.cursor.SeekReverse(upperProbe(s.bounds.high))
		} else {
			s.cursor.Last()
		}
		return
	}
	if s.bounds.hasLow {
		s.cursor.Seek(types.EncodeKey(s.bounds.low))
	} else {
		s.cursor.First()
	}
}

func (s *indexScan) Next() (Row, error) {
	if s.cursor == nil {
		return nil, io.EOF
	}
	for {
		switch {
		case !s.started:
			s.seek()
			s.started = true
		case s.descending:
			s.cursor.Prev()
		default:
			s.cursor.Next()
		}
		if err := s.cursor.Err(); err != nil {
			return nil, err
		}
		if !s.cursor.Valid() {
			return nil, io.EOF
		}

		indexed, rest, err := types.DecodeKey(s.cursor.Key())
		if err != nil {
			return nil, err
		}
		id, _, err := types.DecodeKey(rest)
		if err != nil {
			return nil, err
		}

		inside, done := s.bounds.contains(indexed, s.descending)
		if done {
			return nil, io.EOF
		}
		if !inside {
			continue
		}

		raw, err := s.rows.Get(types.EncodeKey(id))
		if err != nil {
			return nil, err
		}
		if raw == nil {
			return nil, fmt.Errorf("index %q points at a missing row %d", s.index.Name, id.I)
		}
		row, err := types.DecodeRow(raw)
		if err != nil {
			return nil, err
		}
		s.rowID = id.I
		return row, nil
	}
}

// contains reports whether a value is inside the range, and whether a scan in
// the given direction has walked past the end of it.
func (r scanRange) contains(value types.Value, descending bool) (inside, done bool) {
	if value.IsNull() {
		// A comparison against NULL is never true, so a bounded scan skips
		// NULL entries; an unbounded one keeps them.
		return !r.hasLow && !r.hasHigh, false
	}
	if r.hasLow {
		cmp, ok := types.Compare(value, r.low)
		if !ok {
			return false, false
		}
		if cmp < 0 {
			return false, descending
		}
		if cmp == 0 && !r.lowClosed {
			return false, false
		}
	}
	if r.hasHigh {
		cmp, ok := types.Compare(value, r.high)
		if !ok {
			return false, false
		}
		if cmp > 0 {
			return false, !descending
		}
		if cmp == 0 && !r.highClosed {
			return false, false
		}
	}
	return true, false
}

func (s *indexScan) Close() error {
	if s.cursor != nil {
		s.cursor.Close()
		s.cursor = nil
	}
	return nil
}

func (s *indexScan) Schema() Schema       { return s.schema }
func (s *indexScan) RowID() int64         { return s.rowID }
func (s *indexScan) Children() []Operator { return nil }

func (s *indexScan) Explain() string {
	direction := ""
	if s.descending {
		direction = " backward"
	}
	return fmt.Sprintf("Index Scan%s on %s using %s (%s)",
		direction, s.table.Name, s.index.Name, s.bounds.describe(s.index.Column))
}

// --- primary key scan ------------------------------------------------------

// pkScan walks the table tree itself over a range of row ids. It is what an
// INTEGER PRIMARY KEY lookup turns into, and it needs no second read.
type pkScan struct {
	tx         *storage.Tx
	table      *Table
	schema     Schema
	bounds     scanRange
	descending bool

	cursor  *storage.Cursor
	started bool
	rowID   int64
}

func newPKScan(tx *storage.Tx, table *Table, alias string, bounds scanRange, descending bool) *pkScan {
	return &pkScan{tx: tx, table: table, schema: tableSchema(table, alias), bounds: bounds, descending: descending}
}

func (s *pkScan) Open() error {
	s.Close()
	s.cursor = s.tx.Tree(s.table.Root).Cursor()
	s.started = false
	return nil
}

func (s *pkScan) Next() (Row, error) {
	if s.cursor == nil {
		return nil, io.EOF
	}
	for {
		switch {
		case !s.started:
			s.started = true
			switch {
			case s.descending && s.bounds.hasHigh:
				s.cursor.SeekReverse(upperProbe(s.bounds.high))
			case s.descending:
				s.cursor.Last()
			case s.bounds.hasLow:
				s.cursor.Seek(types.EncodeKey(s.bounds.low))
			default:
				s.cursor.First()
			}
		case s.descending:
			s.cursor.Prev()
		default:
			s.cursor.Next()
		}
		if err := s.cursor.Err(); err != nil {
			return nil, err
		}
		if !s.cursor.Valid() {
			return nil, io.EOF
		}

		id, _, err := types.DecodeKey(s.cursor.Key())
		if err != nil {
			return nil, err
		}
		inside, done := s.bounds.contains(id, s.descending)
		if done {
			return nil, io.EOF
		}
		if !inside {
			continue
		}

		raw, err := s.cursor.Value()
		if err != nil {
			return nil, err
		}
		row, err := types.DecodeRow(raw)
		if err != nil {
			return nil, err
		}
		s.rowID = id.I
		return row, nil
	}
}

func (s *pkScan) Close() error {
	if s.cursor != nil {
		s.cursor.Close()
		s.cursor = nil
	}
	return nil
}

func (s *pkScan) Schema() Schema       { return s.schema }
func (s *pkScan) RowID() int64         { return s.rowID }
func (s *pkScan) Children() []Operator { return nil }

func (s *pkScan) Explain() string {
	direction := ""
	if s.descending {
		direction = " backward"
	}
	column := s.table.Columns[s.table.RowIDColumn].Name
	return fmt.Sprintf("Primary Key Scan%s on %s (%s)", direction, s.table.Name, s.bounds.describe(column))
}

// --- filter ----------------------------------------------------------------

type filterOp struct {
	child     Operator
	predicate sql.Expr
	eval      *evaluator
}

func newFilter(child Operator, predicate sql.Expr, subs map[sql.Expr]int) *filterOp {
	eval := newEvaluator(child.Schema())
	eval.substitutions = subs
	return &filterOp{child: child, predicate: predicate, eval: eval}
}

func (f *filterOp) Open() error { return f.child.Open() }

func (f *filterOp) Next() (Row, error) {
	for {
		row, err := f.child.Next()
		if err != nil {
			return nil, err
		}
		keep, err := f.eval.eval(f.predicate, row)
		if err != nil {
			return nil, err
		}
		if keep.Truthy() {
			return row, nil
		}
	}
}

func (f *filterOp) Close() error         { return f.child.Close() }
func (f *filterOp) Schema() Schema       { return f.child.Schema() }
func (f *filterOp) Children() []Operator { return []Operator{f.child} }
func (f *filterOp) Explain() string      { return "Filter: " + f.predicate.String() }

// RowID forwards the row id when the filter sits directly on a scan, so DML
// statements keep working through a pushed down predicate.
func (f *filterOp) RowID() int64 {
	if source, ok := f.child.(rowSource); ok {
		return source.RowID()
	}
	return 0
}

// --- joins -----------------------------------------------------------------

type nestedLoopJoin struct {
	left, right Operator
	on          sql.Expr
	schema      Schema
	eval        *evaluator

	leftRow   Row
	rightOpen bool
}

func newNestedLoopJoin(left, right Operator, on sql.Expr) *nestedLoopJoin {
	schema := append(append(Schema{}, left.Schema()...), right.Schema()...)
	return &nestedLoopJoin{left: left, right: right, on: on, schema: schema, eval: newEvaluator(schema)}
}

func (j *nestedLoopJoin) Open() error {
	j.leftRow = nil
	j.rightOpen = false
	return j.left.Open()
}

func (j *nestedLoopJoin) Next() (Row, error) {
	for {
		if j.leftRow == nil {
			row, err := j.left.Next()
			if err != nil {
				return nil, err
			}
			j.leftRow = row
			if err := j.right.Open(); err != nil {
				return nil, err
			}
			j.rightOpen = true
		}

		rightRow, err := j.right.Next()
		if err == io.EOF {
			j.right.Close()
			j.rightOpen = false
			j.leftRow = nil
			continue
		}
		if err != nil {
			return nil, err
		}

		joined := make(Row, 0, len(j.leftRow)+len(rightRow))
		joined = append(append(joined, j.leftRow...), rightRow...)
		if j.on == nil {
			return joined, nil
		}
		keep, err := j.eval.eval(j.on, joined)
		if err != nil {
			return nil, err
		}
		if keep.Truthy() {
			return joined, nil
		}
	}
}

func (j *nestedLoopJoin) Close() error {
	if j.rightOpen {
		j.right.Close()
		j.rightOpen = false
	}
	return j.left.Close()
}

func (j *nestedLoopJoin) Schema() Schema       { return j.schema }
func (j *nestedLoopJoin) Children() []Operator { return []Operator{j.left, j.right} }

func (j *nestedLoopJoin) Explain() string {
	if j.on == nil {
		return "Nested Loop Join"
	}
	return "Nested Loop Join: " + j.on.String()
}

// hashJoin handles the common case of an equality predicate: the right side is
// loaded into a hash table once and every left row probes it.
type hashJoin struct {
	left, right Operator
	leftKey     sql.Expr
	rightKey    sql.Expr
	residual    sql.Expr
	schema      Schema

	leftEval  *evaluator
	rightEval *evaluator
	joinEval  *evaluator

	buckets map[string][]Row
	leftRow Row
	matches []Row
	pos     int
}

func newHashJoin(left, right Operator, leftKey, rightKey, residual sql.Expr) *hashJoin {
	schema := append(append(Schema{}, left.Schema()...), right.Schema()...)
	return &hashJoin{
		left: left, right: right,
		leftKey: leftKey, rightKey: rightKey, residual: residual,
		schema:    schema,
		leftEval:  newEvaluator(left.Schema()),
		rightEval: newEvaluator(right.Schema()),
		joinEval:  newEvaluator(schema),
	}
}

func (j *hashJoin) Open() error {
	if err := j.left.Open(); err != nil {
		return err
	}
	if err := j.right.Open(); err != nil {
		return err
	}
	defer j.right.Close()

	j.buckets = make(map[string][]Row)
	j.leftRow, j.matches, j.pos = nil, nil, 0

	for {
		row, err := j.right.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		key, err := j.rightEval.eval(j.rightKey, row)
		if err != nil {
			return err
		}
		if key.IsNull() {
			continue // NULL never matches in an equi join
		}
		hash := string(types.EncodeKey(key))
		j.buckets[hash] = append(j.buckets[hash], row)
	}
}

func (j *hashJoin) Next() (Row, error) {
	for {
		if j.leftRow != nil && j.pos < len(j.matches) {
			rightRow := j.matches[j.pos]
			j.pos++

			joined := make(Row, 0, len(j.leftRow)+len(rightRow))
			joined = append(append(joined, j.leftRow...), rightRow...)
			if j.residual == nil {
				return joined, nil
			}
			keep, err := j.joinEval.eval(j.residual, joined)
			if err != nil {
				return nil, err
			}
			if keep.Truthy() {
				return joined, nil
			}
			continue
		}

		row, err := j.left.Next()
		if err != nil {
			return nil, err
		}
		key, err := j.leftEval.eval(j.leftKey, row)
		if err != nil {
			return nil, err
		}
		j.leftRow, j.pos = row, 0
		if key.IsNull() {
			j.matches = nil
			continue
		}
		j.matches = j.buckets[string(types.EncodeKey(key))]
	}
}

func (j *hashJoin) Close() error {
	j.buckets = nil
	return j.left.Close()
}

func (j *hashJoin) Schema() Schema       { return j.schema }
func (j *hashJoin) Children() []Operator { return []Operator{j.left, j.right} }

func (j *hashJoin) Explain() string {
	text := fmt.Sprintf("Hash Join: %s = %s", j.leftKey, j.rightKey)
	if j.residual != nil {
		text += " and " + j.residual.String()
	}
	return text
}

// --- projection ------------------------------------------------------------

type projection struct {
	child  Operator
	exprs  []sql.Expr
	schema Schema
	eval   *evaluator
}

func newProjection(child Operator, exprs []sql.Expr, schema Schema, subs map[sql.Expr]int) *projection {
	eval := newEvaluator(child.Schema())
	eval.substitutions = subs
	return &projection{child: child, exprs: exprs, schema: schema, eval: eval}
}

func (p *projection) Open() error { return p.child.Open() }

func (p *projection) Next() (Row, error) {
	row, err := p.child.Next()
	if err != nil {
		return nil, err
	}
	out := make(Row, len(p.exprs))
	for i, expr := range p.exprs {
		value, err := p.eval.eval(expr, row)
		if err != nil {
			return nil, err
		}
		out[i] = value
	}
	return out, nil
}

func (p *projection) Close() error         { return p.child.Close() }
func (p *projection) Schema() Schema       { return p.schema }
func (p *projection) Children() []Operator { return []Operator{p.child} }

func (p *projection) Explain() string {
	names := make([]string, len(p.schema))
	for i, col := range p.schema {
		names[i] = col.Name
	}
	return "Projection: " + strings.Join(names, ", ")
}

// --- sort ------------------------------------------------------------------

type sortOp struct {
	child Operator
	terms []sql.OrderTerm
	eval  *evaluator

	rows []Row
	pos  int
	done bool
}

func newSort(child Operator, terms []sql.OrderTerm, subs map[sql.Expr]int) *sortOp {
	eval := newEvaluator(child.Schema())
	eval.substitutions = subs
	return &sortOp{child: child, terms: terms, eval: eval}
}

func (s *sortOp) Open() error {
	s.rows, s.pos, s.done = nil, 0, false
	return s.child.Open()
}

func (s *sortOp) Next() (Row, error) {
	if !s.done {
		if err := s.materialise(); err != nil {
			return nil, err
		}
		s.done = true
	}
	if s.pos >= len(s.rows) {
		return nil, io.EOF
	}
	row := s.rows[s.pos]
	s.pos++
	return row, nil
}

func (s *sortOp) materialise() error {
	type entry struct {
		row  Row
		keys []types.Value
	}

	var entries []entry
	for {
		row, err := s.child.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		keys := make([]types.Value, len(s.terms))
		for i, term := range s.terms {
			value, err := s.eval.eval(term.Expr, row)
			if err != nil {
				return err
			}
			keys[i] = value
		}
		entries = append(entries, entry{row: row, keys: keys})
	}

	sort.SliceStable(entries, func(a, b int) bool {
		for i, term := range s.terms {
			left, right := entries[a].keys[i], entries[b].keys[i]
			result := compareForSort(left, right)
			if result == 0 {
				continue
			}
			if term.Descending {
				return result > 0
			}
			return result < 0
		}
		return false
	})

	s.rows = make([]Row, len(entries))
	for i, e := range entries {
		s.rows[i] = e.row
	}
	return nil
}

// compareForSort orders values for ORDER BY, placing NULL first.
func compareForSort(a, b types.Value) int {
	switch {
	case a.IsNull() && b.IsNull():
		return 0
	case a.IsNull():
		return -1
	case b.IsNull():
		return 1
	}
	if result, ok := types.Compare(a, b); ok {
		return result
	}
	return strings.Compare(a.String(), b.String())
}

func (s *sortOp) Close() error         { return s.child.Close() }
func (s *sortOp) Schema() Schema       { return s.child.Schema() }
func (s *sortOp) Children() []Operator { return []Operator{s.child} }

func (s *sortOp) Explain() string {
	parts := make([]string, len(s.terms))
	for i, term := range s.terms {
		parts[i] = term.Expr.String()
		if term.Descending {
			parts[i] += " DESC"
		}
	}
	return "Sort: " + strings.Join(parts, ", ")
}

// --- distinct --------------------------------------------------------------

type distinctOp struct {
	child Operator
	seen  map[string]bool
}

func newDistinct(child Operator) *distinctOp { return &distinctOp{child: child} }

func (d *distinctOp) Open() error {
	d.seen = make(map[string]bool)
	return d.child.Open()
}

func (d *distinctOp) Next() (Row, error) {
	for {
		row, err := d.child.Next()
		if err != nil {
			return nil, err
		}
		key := string(types.EncodeKeys(row...))
		if d.seen[key] {
			continue
		}
		d.seen[key] = true
		return row, nil
	}
}

func (d *distinctOp) Close() error         { d.seen = nil; return d.child.Close() }
func (d *distinctOp) Schema() Schema       { return d.child.Schema() }
func (d *distinctOp) Children() []Operator { return []Operator{d.child} }
func (d *distinctOp) Explain() string      { return "Distinct" }

// --- limit -----------------------------------------------------------------

type limitOp struct {
	child   Operator
	limit   int64
	offset  int64
	emitted int64
	skipped int64
}

func newLimit(child Operator, limit, offset int64) *limitOp {
	return &limitOp{child: child, limit: limit, offset: offset}
}

func (l *limitOp) Open() error {
	l.emitted, l.skipped = 0, 0
	return l.child.Open()
}

func (l *limitOp) Next() (Row, error) {
	for l.skipped < l.offset {
		if _, err := l.child.Next(); err != nil {
			return nil, err
		}
		l.skipped++
	}
	if l.limit >= 0 && l.emitted >= l.limit {
		return nil, io.EOF
	}
	row, err := l.child.Next()
	if err != nil {
		return nil, err
	}
	l.emitted++
	return row, nil
}

func (l *limitOp) Close() error         { return l.child.Close() }
func (l *limitOp) Schema() Schema       { return l.child.Schema() }
func (l *limitOp) Children() []Operator { return []Operator{l.child} }

func (l *limitOp) Explain() string {
	if l.limit < 0 {
		return fmt.Sprintf("Offset %d", l.offset)
	}
	if l.offset > 0 {
		return fmt.Sprintf("Limit %d offset %d", l.limit, l.offset)
	}
	return fmt.Sprintf("Limit %d", l.limit)
}
