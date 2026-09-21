package engine

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/juliocesar04-code/proj-1/internal/storage"
	"github.com/juliocesar04-code/proj-1/internal/types"
)

// The schema lives in its own B+Tree, whose root page is recorded in the
// database metadata. Every entry is one table definition serialised as JSON,
// which keeps the format readable and easy to extend.
const catalogPrefix = "table:"

// Column is one column of a table.
type Column struct {
	Name       string     `json:"name"`
	Type       types.Type `json:"type"`
	NotNull    bool       `json:"not_null,omitempty"`
	Unique     bool       `json:"unique,omitempty"`
	PrimaryKey bool       `json:"primary_key,omitempty"`
}

// Index is a secondary index over a single column. Entries are keyed by the
// indexed value followed by the row id, so duplicates stay distinct and a
// prefix scan finds every row for a value.
type Index struct {
	Name   string `json:"name"`
	Column string `json:"column"`
	Unique bool   `json:"unique,omitempty"`
	Root   uint64 `json:"root"`
}

// Table is a stored relation.
type Table struct {
	Name    string   `json:"name"`
	Columns []Column `json:"columns"`
	Root    uint64   `json:"root"`
	// NextRowID is the id handed to the next row without an explicit key.
	NextRowID int64 `json:"next_row_id"`
	// RowIDColumn is the position of the INTEGER PRIMARY KEY column, or -1.
	// Such a column is stored as the row key itself instead of being
	// duplicated in an index.
	RowIDColumn int     `json:"rowid_column"`
	Indexes     []Index `json:"indexes"`
}

// ColumnIndex returns the position of a column, or -1.
func (t *Table) ColumnIndex(name string) int {
	for i := range t.Columns {
		if strings.EqualFold(t.Columns[i].Name, name) {
			return i
		}
	}
	return -1
}

// IndexOn returns an index covering the column, preferring a unique one.
func (t *Table) IndexOn(column string) *Index {
	var found *Index
	for i := range t.Indexes {
		if !strings.EqualFold(t.Indexes[i].Column, column) {
			continue
		}
		if t.Indexes[i].Unique {
			return &t.Indexes[i]
		}
		if found == nil {
			found = &t.Indexes[i]
		}
	}
	return found
}

// IndexByName returns the index with the given name.
func (t *Table) IndexByName(name string) *Index {
	for i := range t.Indexes {
		if strings.EqualFold(t.Indexes[i].Name, name) {
			return &t.Indexes[i]
		}
	}
	return nil
}

func (t *Table) clone() *Table {
	copied := *t
	copied.Columns = append([]Column(nil), t.Columns...)
	copied.Indexes = append([]Index(nil), t.Indexes...)
	return &copied
}

// Catalog is an in memory snapshot of the schema. A write transaction works
// on a private copy that is published only when the transaction commits.
type Catalog struct {
	tables map[string]*Table
}

func newCatalog() *Catalog { return &Catalog{tables: map[string]*Table{}} }

// Table looks a table up, ignoring case.
func (c *Catalog) Table(name string) (*Table, bool) {
	t, ok := c.tables[strings.ToLower(name)]
	return t, ok
}

// TableNames lists every table in the database, sorted.
func (c *Catalog) TableNames() []string {
	names := make([]string, 0, len(c.tables))
	for _, t := range c.tables {
		names = append(names, t.Name)
	}
	slices.SortFunc(names, func(a, b string) int {
		return strings.Compare(strings.ToLower(a), strings.ToLower(b))
	})
	return names
}

// FindIndex locates an index by name across every table.
func (c *Catalog) FindIndex(name string) (*Table, *Index) {
	for _, t := range c.tables {
		if idx := t.IndexByName(name); idx != nil {
			return t, idx
		}
	}
	return nil, nil
}

func (c *Catalog) put(t *Table)       { c.tables[strings.ToLower(t.Name)] = t }
func (c *Catalog) remove(name string) { delete(c.tables, strings.ToLower(name)) }

func (c *Catalog) clone() *Catalog {
	copied := newCatalog()
	for key, t := range c.tables {
		copied.tables[key] = t.clone()
	}
	return copied
}

// loadCatalog reads every table definition from the schema tree.
func loadCatalog(tx *storage.Tx) (*Catalog, error) {
	catalog := newCatalog()
	root := tx.CatalogRoot()
	if root == 0 {
		return catalog, nil
	}

	cursor := tx.Tree(root).Cursor()
	defer cursor.Close()
	for cursor.Seek([]byte(catalogPrefix)); cursor.Valid(); cursor.Next() {
		if !strings.HasPrefix(string(cursor.Key()), catalogPrefix) {
			break
		}
		raw, err := cursor.Value()
		if err != nil {
			return nil, err
		}
		var table Table
		if err := json.Unmarshal(raw, &table); err != nil {
			return nil, fmt.Errorf("catalog entry %q: %w", cursor.Key(), err)
		}
		catalog.put(&table)
	}
	return catalog, cursor.Err()
}

// catalogTree returns the schema tree, creating it on first use.
func catalogTree(tx *storage.Tx) (*storage.Tree, error) {
	if root := tx.CatalogRoot(); root != 0 {
		return tx.Tree(root), nil
	}
	tree, err := tx.CreateTree()
	if err != nil {
		return nil, err
	}
	tx.SetCatalogRoot(tree.Root())
	return tree, nil
}

func storeTable(tx *storage.Tx, table *Table) error {
	tree, err := catalogTree(tx)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(table)
	if err != nil {
		return err
	}
	if err := tree.Put([]byte(catalogPrefix+strings.ToLower(table.Name)), raw); err != nil {
		return err
	}
	tx.SetCatalogRoot(tree.Root())
	return nil
}

func removeTable(tx *storage.Tx, name string) error {
	tree, err := catalogTree(tx)
	if err != nil {
		return err
	}
	if _, err := tree.Delete([]byte(catalogPrefix + strings.ToLower(name))); err != nil {
		return err
	}
	tx.SetCatalogRoot(tree.Root())
	return nil
}
