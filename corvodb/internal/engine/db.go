// Package engine ties the storage layer and the SQL parser together: it owns
// the schema, plans statements and runs them.
package engine

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"

	"github.com/juliocesar04-code/proj-1/corvodb/internal/sql"
	"github.com/juliocesar04-code/proj-1/corvodb/internal/storage"
	"github.com/juliocesar04-code/proj-1/corvodb/internal/types"
)

// Errors reported by the engine.
var (
	ErrNoTransaction     = errors.New("no transaction is open")
	ErrNestedTransaction = errors.New("a transaction is already open")
)

// Options configures a database.
type Options struct {
	// CacheSize is the number of pages held in the buffer pool.
	CacheSize int
	// NoSync trades durability for speed by skipping the fsync on commit.
	NoSync bool
	// ReadOnly opens the database without allowing writes.
	ReadOnly bool
}

// DB is an open database.
type DB struct {
	store   *storage.Store
	catalog atomic.Pointer[Catalog]
}

// Open opens or creates the database stored at path.
func Open(path string, opts Options) (*DB, error) {
	store, err := storage.Open(path, storage.Options{
		CacheSize: opts.CacheSize,
		NoSync:    opts.NoSync,
		ReadOnly:  opts.ReadOnly,
	})
	if err != nil {
		return nil, err
	}

	db := &DB{store: store}
	if err := store.View(func(tx *storage.Tx) error {
		catalog, err := loadCatalog(tx)
		if err != nil {
			return err
		}
		db.catalog.Store(catalog)
		return nil
	}); err != nil {
		store.Close()
		return nil, err
	}
	return db, nil
}

// Close flushes and closes the database.
func (db *DB) Close() error { return db.store.Close() }

// Path is the file backing the database.
func (db *DB) Path() string { return db.store.Path() }

// Catalog returns the current schema snapshot.
func (db *DB) Catalog() *Catalog { return db.catalog.Load() }

// Result is what a statement produced.
type Result struct {
	// Tag names the statement, for example "CREATE TABLE".
	Tag string
	// Columns and Rows are filled in by SELECT and EXPLAIN.
	Columns []string
	Rows    []Row
	// RowsAffected counts the rows an INSERT, UPDATE or DELETE touched.
	RowsAffected int64
}

// Session runs statements and owns the explicit transaction, if any.
type Session struct {
	db *DB
	tx *txContext
}

// Session opens a new session on the database.
func (db *DB) Session() *Session { return &Session{db: db} }

// Exec runs every statement in the query and returns one result per statement.
func (s *Session) Exec(query string) ([]Result, error) {
	stmts, err := sql.Parse(query)
	if err != nil {
		return nil, err
	}
	results := make([]Result, 0, len(stmts))
	for _, stmt := range stmts {
		result, err := s.ExecStatement(stmt)
		if err != nil {
			return results, err
		}
		results = append(results, result)
	}
	return results, nil
}

// InTransaction reports whether an explicit transaction is open.
func (s *Session) InTransaction() bool { return s.tx != nil }

// Close rolls back an open transaction.
func (s *Session) Close() error {
	if s.tx == nil {
		return nil
	}
	err := s.tx.rollback()
	s.tx = nil
	return err
}

// ExecStatement runs one parsed statement.
func (s *Session) ExecStatement(stmt sql.Statement) (Result, error) {
	switch stmt.(type) {
	case *sql.Begin:
		if s.tx != nil {
			return Result{}, ErrNestedTransaction
		}
		tx, err := s.db.begin(true)
		if err != nil {
			return Result{}, err
		}
		s.tx = tx
		return Result{Tag: "BEGIN"}, nil

	case *sql.Commit:
		if s.tx == nil {
			return Result{}, ErrNoTransaction
		}
		err := s.tx.commit()
		s.tx = nil
		return Result{Tag: "COMMIT"}, err

	case *sql.Rollback:
		if s.tx == nil {
			return Result{}, ErrNoTransaction
		}
		err := s.tx.rollback()
		s.tx = nil
		return Result{Tag: "ROLLBACK"}, err
	}

	if s.tx != nil {
		result, err := s.tx.run(stmt)
		if err != nil {
			// Leave the transaction open: the caller decides whether to
			// retry the statement or roll back.
			return result, err
		}
		return result, nil
	}

	tx, err := s.db.begin(needsWrite(stmt))
	if err != nil {
		return Result{}, err
	}
	result, err := tx.run(stmt)
	if err != nil {
		tx.rollback()
		return result, err
	}
	return result, tx.commit()
}

func needsWrite(stmt sql.Statement) bool {
	switch stmt.(type) {
	case *sql.Select, *sql.Explain:
		return false
	default:
		return true
	}
}

// txContext is one transaction: a storage transaction plus the private copy of
// the schema it may modify.
type txContext struct {
	db       *DB
	st       *storage.Tx
	catalog  *Catalog
	writable bool
	changed  map[string]bool
}

func (db *DB) begin(writable bool) (*txContext, error) {
	st, err := db.store.Begin(writable)
	if err != nil {
		return nil, err
	}
	catalog := db.catalog.Load()
	if writable {
		catalog = catalog.clone()
	}
	return &txContext{
		db: db, st: st, catalog: catalog,
		writable: writable, changed: map[string]bool{},
	}, nil
}

func (tx *txContext) commit() error {
	if tx.writable {
		for name := range tx.changed {
			table, ok := tx.catalog.Table(name)
			if !ok {
				continue
			}
			if err := storeTable(tx.st, table); err != nil {
				tx.st.Rollback()
				return err
			}
		}
	}
	if err := tx.st.Commit(); err != nil {
		return err
	}
	if tx.writable {
		tx.db.catalog.Store(tx.catalog)
	}
	return nil
}

func (tx *txContext) rollback() error { return tx.st.Rollback() }

func (tx *txContext) touch(table *Table) { tx.changed[strings.ToLower(table.Name)] = true }

func (tx *txContext) run(stmt sql.Statement) (Result, error) {
	switch node := stmt.(type) {
	case *sql.Select:
		return tx.runSelect(node)
	case *sql.Explain:
		return tx.runExplain(node)
	case *sql.CreateTable:
		return tx.createTable(node)
	case *sql.DropTable:
		return tx.dropTable(node)
	case *sql.CreateIndex:
		return tx.createIndex(node)
	case *sql.DropIndex:
		return tx.dropIndex(node)
	case *sql.Insert:
		return tx.insert(node)
	case *sql.Update:
		return tx.update(node)
	case *sql.Delete:
		return tx.delete(node)
	default:
		return Result{}, fmt.Errorf("unsupported statement %T", stmt)
	}
}

// --- queries ---------------------------------------------------------------

func (tx *txContext) plan(stmt *sql.Select) (Operator, error) {
	p := &planner{tx: tx.st, catalog: tx.catalog}
	return p.planSelect(stmt)
}

func (tx *txContext) runSelect(stmt *sql.Select) (Result, error) {
	op, err := tx.plan(stmt)
	if err != nil {
		return Result{}, err
	}
	if err := op.Open(); err != nil {
		return Result{}, err
	}
	defer op.Close()

	result := Result{Tag: "SELECT"}
	for _, column := range op.Schema() {
		result.Columns = append(result.Columns, column.Name)
	}
	for {
		row, err := op.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Result{}, err
		}
		result.Rows = append(result.Rows, row)
	}
	result.RowsAffected = int64(len(result.Rows))
	return result, nil
}

func (tx *txContext) runExplain(stmt *sql.Explain) (Result, error) {
	query, ok := stmt.Statement.(*sql.Select)
	if !ok {
		return Result{}, fmt.Errorf("EXPLAIN only supports SELECT")
	}
	op, err := tx.plan(query)
	if err != nil {
		return Result{}, err
	}
	result := Result{Tag: "EXPLAIN", Columns: []string{"plan"}}
	for _, line := range ExplainPlan(op) {
		result.Rows = append(result.Rows, Row{types.NewText(line)})
	}
	return result, nil
}

// --- schema changes --------------------------------------------------------

func (tx *txContext) createTable(stmt *sql.CreateTable) (Result, error) {
	if _, exists := tx.catalog.Table(stmt.Name); exists {
		if stmt.IfNotExists {
			return Result{Tag: "CREATE TABLE"}, nil
		}
		return Result{}, fmt.Errorf("table %s already exists", stmt.Name)
	}

	table := &Table{Name: stmt.Name, NextRowID: 1, RowIDColumn: -1}
	primaryKeys := 0
	for _, def := range stmt.Columns {
		if table.ColumnIndex(def.Name) >= 0 {
			return Result{}, fmt.Errorf("duplicate column name %s", def.Name)
		}
		if def.PrimaryKey {
			primaryKeys++
			if primaryKeys > 1 {
				return Result{}, fmt.Errorf("table %s has more than one primary key", stmt.Name)
			}
			if def.Type == types.Integer {
				table.RowIDColumn = len(table.Columns)
			}
		}
		table.Columns = append(table.Columns, Column{
			Name: def.Name, Type: def.Type,
			NotNull: def.NotNull, Unique: def.Unique, PrimaryKey: def.PrimaryKey,
		})
	}

	tree, err := tx.st.CreateTree()
	if err != nil {
		return Result{}, err
	}
	table.Root = tree.Root()

	// A column that must stay unique gets an index, unless it is already the
	// row id, which is unique by construction.
	for i, column := range table.Columns {
		needsIndex := column.Unique || (column.PrimaryKey && i != table.RowIDColumn)
		if !needsIndex {
			continue
		}
		index, err := tx.buildIndex(table, fmt.Sprintf("%s_%s_key", table.Name, column.Name), column.Name, true)
		if err != nil {
			return Result{}, err
		}
		table.Indexes = append(table.Indexes, *index)
	}

	tx.catalog.put(table)
	tx.touch(table)
	return Result{Tag: "CREATE TABLE"}, nil
}

func (tx *txContext) dropTable(stmt *sql.DropTable) (Result, error) {
	table, ok := tx.catalog.Table(stmt.Name)
	if !ok {
		if stmt.IfExists {
			return Result{Tag: "DROP TABLE"}, nil
		}
		return Result{}, fmt.Errorf("no such table: %s", stmt.Name)
	}

	for _, index := range table.Indexes {
		if err := tx.st.Tree(index.Root).Drop(); err != nil {
			return Result{}, err
		}
	}
	if err := tx.st.Tree(table.Root).Drop(); err != nil {
		return Result{}, err
	}
	if err := removeTable(tx.st, table.Name); err != nil {
		return Result{}, err
	}
	tx.catalog.remove(table.Name)
	delete(tx.changed, strings.ToLower(table.Name))
	return Result{Tag: "DROP TABLE"}, nil
}

func (tx *txContext) createIndex(stmt *sql.CreateIndex) (Result, error) {
	if existing, _ := tx.catalog.FindIndex(stmt.Name); existing != nil {
		if stmt.IfNotExists {
			return Result{Tag: "CREATE INDEX"}, nil
		}
		return Result{}, fmt.Errorf("index %s already exists", stmt.Name)
	}
	table, ok := tx.catalog.Table(stmt.Table)
	if !ok {
		return Result{}, fmt.Errorf("no such table: %s", stmt.Table)
	}
	if table.ColumnIndex(stmt.Column) < 0 {
		return Result{}, fmt.Errorf("no such column: %s.%s", stmt.Table, stmt.Column)
	}

	index, err := tx.buildIndex(table, stmt.Name, stmt.Column, stmt.Unique)
	if err != nil {
		return Result{}, err
	}
	table.Indexes = append(table.Indexes, *index)
	tx.touch(table)
	return Result{Tag: "CREATE INDEX"}, nil
}

// buildIndex creates the index tree and fills it from the rows already stored.
func (tx *txContext) buildIndex(table *Table, name, column string, unique bool) (*Index, error) {
	tree, err := tx.st.CreateTree()
	if err != nil {
		return nil, err
	}
	index := &Index{Name: name, Column: column, Unique: unique, Root: tree.Root()}
	position := table.ColumnIndex(column)

	cursor := tx.st.Tree(table.Root).Cursor()
	defer cursor.Close()

	type entry struct {
		value types.Value
		rowID int64
	}
	var entries []entry
	for cursor.First(); cursor.Valid(); cursor.Next() {
		id, _, err := types.DecodeKey(cursor.Key())
		if err != nil {
			return nil, err
		}
		raw, err := cursor.Value()
		if err != nil {
			return nil, err
		}
		row, err := types.DecodeRow(raw)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry{value: row[position], rowID: id.I})
	}
	if err := cursor.Err(); err != nil {
		return nil, err
	}

	for _, e := range entries {
		if err := tx.addIndexEntry(index, e.value, e.rowID); err != nil {
			return nil, err
		}
	}
	return index, nil
}

func (tx *txContext) dropIndex(stmt *sql.DropIndex) (Result, error) {
	table, index := tx.catalog.FindIndex(stmt.Name)
	if index == nil {
		if stmt.IfExists {
			return Result{Tag: "DROP INDEX"}, nil
		}
		return Result{}, fmt.Errorf("no such index: %s", stmt.Name)
	}
	if err := tx.st.Tree(index.Root).Drop(); err != nil {
		return Result{}, err
	}
	for i := range table.Indexes {
		if strings.EqualFold(table.Indexes[i].Name, stmt.Name) {
			table.Indexes = append(table.Indexes[:i], table.Indexes[i+1:]...)
			break
		}
	}
	tx.touch(table)
	return Result{Tag: "DROP INDEX"}, nil
}

// --- index maintenance -----------------------------------------------------

func indexEntryKey(value types.Value, rowID int64) []byte {
	return types.EncodeKeys(value, types.NewInt(rowID))
}

func (tx *txContext) addIndexEntry(index *Index, value types.Value, rowID int64) error {
	if index.Unique && !value.IsNull() {
		conflict, err := tx.uniqueConflict(index, value, rowID)
		if err != nil {
			return err
		}
		if conflict {
			return fmt.Errorf("duplicate value %s violates unique index %s", quoteValue(value), index.Name)
		}
	}
	tree := tx.st.Tree(index.Root)
	if err := tree.Put(indexEntryKey(value, rowID), nil); err != nil {
		return err
	}
	index.Root = tree.Root()
	return nil
}

func (tx *txContext) removeIndexEntry(index *Index, value types.Value, rowID int64) error {
	tree := tx.st.Tree(index.Root)
	if _, err := tree.Delete(indexEntryKey(value, rowID)); err != nil {
		return err
	}
	index.Root = tree.Root()
	return nil
}

// uniqueConflict reports whether another row already holds the value.
func (tx *txContext) uniqueConflict(index *Index, value types.Value, rowID int64) (bool, error) {
	prefix := types.EncodeKey(value)
	cursor := tx.st.Tree(index.Root).Cursor()
	defer cursor.Close()

	for cursor.Seek(prefix); cursor.Valid(); cursor.Next() {
		key := cursor.Key()
		if !strings.HasPrefix(string(key), string(prefix)) {
			break
		}
		existing, _, err := types.DecodeKey(key[len(prefix):])
		if err != nil {
			return false, err
		}
		if existing.I != rowID {
			return true, nil
		}
	}
	return false, cursor.Err()
}

// --- rows ------------------------------------------------------------------

func (tx *txContext) insert(stmt *sql.Insert) (Result, error) {
	table, ok := tx.catalog.Table(stmt.Table)
	if !ok {
		return Result{}, fmt.Errorf("no such table: %s", stmt.Table)
	}

	positions := make([]int, 0, len(stmt.Columns))
	for _, name := range stmt.Columns {
		index := table.ColumnIndex(name)
		if index < 0 {
			return Result{}, fmt.Errorf("no such column: %s.%s", table.Name, name)
		}
		positions = append(positions, index)
	}
	if len(positions) == 0 {
		for i := range table.Columns {
			positions = append(positions, i)
		}
	}

	eval := newEvaluator(nil)
	var inserted int64

	for _, values := range stmt.Rows {
		if len(values) != len(positions) {
			return Result{}, fmt.Errorf("expected %d values, got %d", len(positions), len(values))
		}
		row := make(Row, len(table.Columns))
		for i := range row {
			row[i] = types.NullValue
		}
		for i, expr := range values {
			if containsAggregate(expr) {
				return Result{}, fmt.Errorf("aggregate functions are not allowed in VALUES")
			}
			value, err := eval.eval(expr, nil)
			if err != nil {
				return Result{}, err
			}
			row[positions[i]] = value
		}
		if err := tx.writeRow(table, row); err != nil {
			return Result{}, err
		}
		inserted++
	}

	tx.touch(table)
	return Result{Tag: "INSERT", RowsAffected: inserted}, nil
}

// writeRow validates a row and stores it together with its index entries.
func (tx *txContext) writeRow(table *Table, row Row) error {
	rowID := int64(0)
	if table.RowIDColumn >= 0 && !row[table.RowIDColumn].IsNull() {
		value, err := types.Cast(row[table.RowIDColumn], types.Integer)
		if err != nil {
			return fmt.Errorf("column %s: %w", table.Columns[table.RowIDColumn].Name, err)
		}
		rowID = value.I
	} else {
		rowID = table.NextRowID
		if table.RowIDColumn >= 0 {
			row[table.RowIDColumn] = types.NewInt(rowID)
		}
	}
	if rowID >= table.NextRowID {
		table.NextRowID = rowID + 1
	}

	if err := validateRow(table, row); err != nil {
		return err
	}

	tree := tx.st.Tree(table.Root)
	key := types.EncodeKey(types.NewInt(rowID))
	if table.RowIDColumn >= 0 {
		exists, err := tree.Has(key)
		if err != nil {
			return err
		}
		if exists {
			return fmt.Errorf("duplicate primary key %d in %s", rowID, table.Name)
		}
	}

	for i := range table.Indexes {
		position := table.ColumnIndex(table.Indexes[i].Column)
		if err := tx.addIndexEntry(&table.Indexes[i], row[position], rowID); err != nil {
			return err
		}
	}

	if err := tree.Put(key, types.EncodeRow(row)); err != nil {
		return err
	}
	table.Root = tree.Root()
	return nil
}

func validateRow(table *Table, row Row) error {
	for i, column := range table.Columns {
		if row[i].IsNull() {
			if column.NotNull {
				return fmt.Errorf("column %s cannot be NULL", column.Name)
			}
			continue
		}
		value, err := types.Cast(row[i], column.Type)
		if err != nil {
			return fmt.Errorf("column %s: %w", column.Name, err)
		}
		row[i] = value
	}
	return nil
}

// match collects the rows a WHERE clause selects. The rows are read into
// memory first because modifying a tree invalidates open cursors.
func (tx *txContext) match(table *Table, where sql.Expr) ([]int64, []Row, error) {
	if containsAggregate(where) {
		return nil, nil, fmt.Errorf("aggregate functions are not allowed in WHERE")
	}
	p := &planner{tx: tx.st, catalog: tx.catalog}
	op, err := p.planAccess(table, sql.TableRef{Name: table.Name}, splitConjuncts(where), accessHint{})
	if err != nil {
		return nil, nil, err
	}
	source, ok := op.(rowSource)
	if !ok {
		return nil, nil, fmt.Errorf("internal error: %T cannot report row ids", op)
	}
	if err := op.Open(); err != nil {
		return nil, nil, err
	}
	defer op.Close()

	var ids []int64
	var rows []Row
	for {
		row, err := op.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, err
		}
		ids = append(ids, source.RowID())
		rows = append(rows, row)
	}
	return ids, rows, nil
}

func (tx *txContext) update(stmt *sql.Update) (Result, error) {
	table, ok := tx.catalog.Table(stmt.Table)
	if !ok {
		return Result{}, fmt.Errorf("no such table: %s", stmt.Table)
	}

	assignments := make([]int, len(stmt.Assignments))
	for i, assignment := range stmt.Assignments {
		position := table.ColumnIndex(assignment.Column)
		if position < 0 {
			return Result{}, fmt.Errorf("no such column: %s.%s", table.Name, assignment.Column)
		}
		if containsAggregate(assignment.Value) {
			return Result{}, fmt.Errorf("aggregate functions are not allowed in SET")
		}
		assignments[i] = position
	}

	ids, rows, err := tx.match(table, stmt.Where)
	if err != nil {
		return Result{}, err
	}

	eval := newEvaluator(tableSchema(table, table.Name))
	var updated int64

	for i, row := range rows {
		next := append(Row(nil), row...)
		for j, assignment := range stmt.Assignments {
			value, err := eval.eval(assignment.Value, row)
			if err != nil {
				return Result{}, err
			}
			next[assignments[j]] = value
		}
		if err := tx.replaceRow(table, ids[i], row, next); err != nil {
			return Result{}, err
		}
		updated++
	}

	tx.touch(table)
	return Result{Tag: "UPDATE", RowsAffected: updated}, nil
}

func (tx *txContext) replaceRow(table *Table, rowID int64, old, next Row) error {
	if err := validateRow(table, next); err != nil {
		return err
	}

	newID := rowID
	if table.RowIDColumn >= 0 {
		if next[table.RowIDColumn].IsNull() {
			return fmt.Errorf("column %s cannot be NULL", table.Columns[table.RowIDColumn].Name)
		}
		newID = next[table.RowIDColumn].I
	}

	if err := tx.deleteRow(table, rowID, old); err != nil {
		return err
	}
	if newID >= table.NextRowID {
		table.NextRowID = newID + 1
	}

	tree := tx.st.Tree(table.Root)
	key := types.EncodeKey(types.NewInt(newID))
	if newID != rowID {
		exists, err := tree.Has(key)
		if err != nil {
			return err
		}
		if exists {
			return fmt.Errorf("duplicate primary key %d in %s", newID, table.Name)
		}
	}

	for i := range table.Indexes {
		position := table.ColumnIndex(table.Indexes[i].Column)
		if err := tx.addIndexEntry(&table.Indexes[i], next[position], newID); err != nil {
			return err
		}
	}
	if err := tree.Put(key, types.EncodeRow(next)); err != nil {
		return err
	}
	table.Root = tree.Root()
	return nil
}

func (tx *txContext) deleteRow(table *Table, rowID int64, row Row) error {
	for i := range table.Indexes {
		position := table.ColumnIndex(table.Indexes[i].Column)
		if err := tx.removeIndexEntry(&table.Indexes[i], row[position], rowID); err != nil {
			return err
		}
	}
	tree := tx.st.Tree(table.Root)
	if _, err := tree.Delete(types.EncodeKey(types.NewInt(rowID))); err != nil {
		return err
	}
	table.Root = tree.Root()
	return nil
}

func (tx *txContext) delete(stmt *sql.Delete) (Result, error) {
	table, ok := tx.catalog.Table(stmt.Table)
	if !ok {
		return Result{}, fmt.Errorf("no such table: %s", stmt.Table)
	}

	ids, rows, err := tx.match(table, stmt.Where)
	if err != nil {
		return Result{}, err
	}
	for i, row := range rows {
		if err := tx.deleteRow(table, ids[i], row); err != nil {
			return Result{}, err
		}
	}

	tx.touch(table)
	return Result{Tag: "DELETE", RowsAffected: int64(len(ids))}, nil
}
