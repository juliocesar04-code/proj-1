package sql

import (
	"strings"

	"github.com/juliocesar04-code/proj-1/internal/types"
)

// Statement is any top level SQL command.
type Statement interface{ statementNode() }

// Expr is a scalar expression.
type Expr interface {
	String() string
	exprNode()
}

// --- expressions -----------------------------------------------------------

// Literal is a constant.
type Literal struct{ Value types.Value }

// ColumnRef names a column, optionally qualified by a table or alias.
type ColumnRef struct {
	Table string
	Name  string
}

// UnaryExpr is a prefix operator: NOT or unary minus.
type UnaryExpr struct {
	Op      string
	Operand Expr
}

// BinaryExpr covers arithmetic, comparison, logic, LIKE and concatenation.
type BinaryExpr struct {
	Op    string
	Left  Expr
	Right Expr
}

// IsNullExpr is `x IS NULL` or `x IS NOT NULL`.
type IsNullExpr struct {
	Operand Expr
	Negate  bool
}

// InExpr is `x IN (a, b, c)`.
type InExpr struct {
	Operand Expr
	List    []Expr
	Negate  bool
}

// BetweenExpr is `x BETWEEN low AND high`.
type BetweenExpr struct {
	Operand Expr
	Low     Expr
	High    Expr
	Negate  bool
}

// FuncCall is a scalar or aggregate function call.
type FuncCall struct {
	Name     string
	Args     []Expr
	Star     bool // COUNT(*)
	Distinct bool
}

func (*Literal) exprNode()     {}
func (*ColumnRef) exprNode()   {}
func (*UnaryExpr) exprNode()   {}
func (*BinaryExpr) exprNode()  {}
func (*IsNullExpr) exprNode()  {}
func (*InExpr) exprNode()      {}
func (*BetweenExpr) exprNode() {}
func (*FuncCall) exprNode()    {}

func (e *Literal) String() string {
	if e.Value.T == types.Text {
		return "'" + strings.ReplaceAll(e.Value.S, "'", "''") + "'"
	}
	return e.Value.String()
}

func (e *ColumnRef) String() string {
	if e.Table != "" {
		return e.Table + "." + e.Name
	}
	return e.Name
}

func (e *UnaryExpr) String() string {
	if e.Op == "NOT" {
		return "NOT " + e.Operand.String()
	}
	return e.Op + e.Operand.String()
}

func (e *BinaryExpr) String() string {
	return "(" + e.Left.String() + " " + e.Op + " " + e.Right.String() + ")"
}

func (e *IsNullExpr) String() string {
	if e.Negate {
		return e.Operand.String() + " IS NOT NULL"
	}
	return e.Operand.String() + " IS NULL"
}

func (e *InExpr) String() string {
	parts := make([]string, len(e.List))
	for i, item := range e.List {
		parts[i] = item.String()
	}
	op := " IN ("
	if e.Negate {
		op = " NOT IN ("
	}
	return e.Operand.String() + op + strings.Join(parts, ", ") + ")"
}

func (e *BetweenExpr) String() string {
	op := " BETWEEN "
	if e.Negate {
		op = " NOT BETWEEN "
	}
	return e.Operand.String() + op + e.Low.String() + " AND " + e.High.String()
}

func (e *FuncCall) String() string {
	if e.Star {
		return e.Name + "(*)"
	}
	parts := make([]string, len(e.Args))
	for i, arg := range e.Args {
		parts[i] = arg.String()
	}
	prefix := ""
	if e.Distinct {
		prefix = "DISTINCT "
	}
	return e.Name + "(" + prefix + strings.Join(parts, ", ") + ")"
}

// --- statements ------------------------------------------------------------

// ColumnDef is one column of a CREATE TABLE.
type ColumnDef struct {
	Name       string
	Type       types.Type
	PrimaryKey bool
	NotNull    bool
	Unique     bool
}

// CreateTable creates a table.
type CreateTable struct {
	Name        string
	IfNotExists bool
	Columns     []ColumnDef
}

// DropTable removes a table and its indexes.
type DropTable struct {
	Name     string
	IfExists bool
}

// CreateIndex builds a secondary index over one column.
type CreateIndex struct {
	Name        string
	Table       string
	Column      string
	Unique      bool
	IfNotExists bool
}

// DropIndex removes a secondary index.
type DropIndex struct {
	Name     string
	IfExists bool
}

// Insert adds rows to a table.
type Insert struct {
	Table   string
	Columns []string
	Rows    [][]Expr
}

// ResultColumn is one entry of a SELECT list.
type ResultColumn struct {
	Star      bool
	StarTable string // qualified star: t.*
	Expr      Expr
	Alias     string
}

// TableRef names a table in a FROM clause.
type TableRef struct {
	Name  string
	Alias string
}

// Ident is the name the query uses to qualify columns of this table.
func (r TableRef) Ident() string {
	if r.Alias != "" {
		return r.Alias
	}
	return r.Name
}

// Join is an inner join against another table.
type Join struct {
	Table TableRef
	On    Expr
}

// OrderTerm is one ORDER BY entry.
type OrderTerm struct {
	Expr       Expr
	Descending bool
}

// Select reads rows.
type Select struct {
	Distinct bool
	Columns  []ResultColumn
	From     *TableRef
	Joins    []Join
	Where    Expr
	GroupBy  []Expr
	Having   Expr
	OrderBy  []OrderTerm
	Limit    Expr
	Offset   Expr
}

// Assignment is one SET entry of an UPDATE.
type Assignment struct {
	Column string
	Value  Expr
}

// Update changes rows in place.
type Update struct {
	Table       string
	Assignments []Assignment
	Where       Expr
}

// Delete removes rows.
type Delete struct {
	Table string
	Where Expr
}

// Begin, Commit and Rollback control explicit transactions.
type (
	Begin    struct{}
	Commit   struct{}
	Rollback struct{}
)

// Explain prints the plan of a statement instead of running it.
type Explain struct{ Statement Statement }

func (*CreateTable) statementNode() {}
func (*DropTable) statementNode()   {}
func (*CreateIndex) statementNode() {}
func (*DropIndex) statementNode()   {}
func (*Insert) statementNode()      {}
func (*Select) statementNode()      {}
func (*Update) statementNode()      {}
func (*Delete) statementNode()      {}
func (*Begin) statementNode()       {}
func (*Commit) statementNode()      {}
func (*Rollback) statementNode()    {}
func (*Explain) statementNode()     {}
