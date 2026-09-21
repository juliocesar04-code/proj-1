package sql

import (
	"strings"

	"github.com/juliocesar04-code/proj-1/internal/types"
)

// Parse reads every statement in the input. Statements are separated by
// semicolons; a trailing semicolon is optional.
func Parse(input string) ([]Statement, error) {
	p, err := newParser(input)
	if err != nil {
		return nil, err
	}
	var out []Statement
	for {
		for p.acceptOperator(";") {
		}
		if p.err != nil {
			return nil, p.err
		}
		if p.cur.Kind == EOF {
			return out, nil
		}
		stmt, err := p.parseStatement()
		if p.err != nil {
			return nil, p.err
		}
		if err != nil {
			return nil, err
		}
		out = append(out, stmt)
		if p.cur.Kind != EOF && !p.atOperator(";") {
			return nil, p.unexpected("a semicolon between statements")
		}
	}
}

// ParseStatement reads exactly one statement.
func ParseStatement(input string) (Statement, error) {
	stmts, err := Parse(input)
	if err != nil {
		return nil, err
	}
	switch len(stmts) {
	case 0:
		return nil, &SyntaxError{Message: "empty statement", Line: 1, Col: 1}
	case 1:
		return stmts[0], nil
	default:
		return nil, &SyntaxError{Message: "expected a single statement", Line: 1, Col: 1}
	}
}

type parser struct {
	lex *lexer
	cur Token
	// err holds the first lexical error. Once it is set the parser sees end
	// of input, so it unwinds instead of spinning on the bad character.
	err error
}

func newParser(input string) (*parser, error) {
	p := &parser{lex: newLexer(input)}
	return p, p.advance()
}

func (p *parser) advance() error {
	tok, err := p.lex.next()
	if err != nil {
		if p.err == nil {
			p.err = err
		}
		p.cur = Token{Kind: EOF, Line: p.cur.Line, Col: p.cur.Col}
		return err
	}
	p.cur = tok
	return nil
}

func (p *parser) unexpected(want string) error {
	return &SyntaxError{
		Message: "expected " + want + ", found " + p.cur.String(),
		Line:    p.cur.Line,
		Col:     p.cur.Col,
	}
}

func (p *parser) atKeyword(words ...string) bool {
	if p.cur.Kind != Keyword {
		return false
	}
	for _, w := range words {
		if p.cur.Text == w {
			return true
		}
	}
	return false
}

func (p *parser) atOperator(op string) bool {
	return p.cur.Kind == Operator && p.cur.Text == op
}

func (p *parser) acceptKeyword(words ...string) bool {
	if !p.atKeyword(words...) {
		return false
	}
	p.advance()
	return true
}

func (p *parser) acceptOperator(op string) bool {
	if !p.atOperator(op) {
		return false
	}
	p.advance()
	return true
}

func (p *parser) expectKeyword(word string) error {
	if !p.atKeyword(word) {
		return p.unexpected(word)
	}
	return p.advance()
}

func (p *parser) expectOperator(op string) error {
	if !p.atOperator(op) {
		return p.unexpected("'" + op + "'")
	}
	return p.advance()
}

func (p *parser) expectIdent(what string) (string, error) {
	if p.cur.Kind != Ident {
		return "", p.unexpected(what)
	}
	name := p.cur.Text
	return name, p.advance()
}

// --- statements ------------------------------------------------------------

func (p *parser) parseStatement() (Statement, error) {
	switch {
	case p.atKeyword("EXPLAIN"):
		p.advance()
		inner, err := p.parseStatement()
		if err != nil {
			return nil, err
		}
		return &Explain{Statement: inner}, nil
	case p.atKeyword("SELECT"):
		return p.parseSelect()
	case p.atKeyword("INSERT"):
		return p.parseInsert()
	case p.atKeyword("UPDATE"):
		return p.parseUpdate()
	case p.atKeyword("DELETE"):
		return p.parseDelete()
	case p.atKeyword("CREATE"):
		return p.parseCreate()
	case p.atKeyword("DROP"):
		return p.parseDrop()
	case p.acceptKeyword("BEGIN"):
		return &Begin{}, nil
	case p.acceptKeyword("COMMIT"):
		return &Commit{}, nil
	case p.acceptKeyword("ROLLBACK"):
		return &Rollback{}, nil
	default:
		return nil, p.unexpected("a statement")
	}
}

func (p *parser) parseCreate() (Statement, error) {
	if err := p.expectKeyword("CREATE"); err != nil {
		return nil, err
	}
	unique := p.acceptKeyword("UNIQUE")
	switch {
	case p.acceptKeyword("TABLE"):
		if unique {
			return nil, p.unexpected("INDEX after UNIQUE")
		}
		return p.parseCreateTable()
	case p.acceptKeyword("INDEX"):
		return p.parseCreateIndex(unique)
	default:
		return nil, p.unexpected("TABLE or INDEX")
	}
}

func (p *parser) parseIfNotExists() (bool, error) {
	if !p.acceptKeyword("IF") {
		return false, nil
	}
	if err := p.expectKeyword("NOT"); err != nil {
		return false, err
	}
	return true, p.expectKeyword("EXISTS")
}

func (p *parser) parseCreateTable() (Statement, error) {
	ifNotExists, err := p.parseIfNotExists()
	if err != nil {
		return nil, err
	}
	name, err := p.expectIdent("a table name")
	if err != nil {
		return nil, err
	}
	if err := p.expectOperator("("); err != nil {
		return nil, err
	}

	stmt := &CreateTable{Name: name, IfNotExists: ifNotExists}
	for {
		column, err := p.parseColumnDef()
		if err != nil {
			return nil, err
		}
		stmt.Columns = append(stmt.Columns, column)
		if !p.acceptOperator(",") {
			break
		}
	}
	if err := p.expectOperator(")"); err != nil {
		return nil, err
	}
	return stmt, nil
}

func (p *parser) parseColumnDef() (ColumnDef, error) {
	var def ColumnDef
	name, err := p.expectIdent("a column name")
	if err != nil {
		return def, err
	}
	def.Name = name

	typeName, err := p.expectIdent("a column type")
	if err != nil {
		return def, err
	}
	kind, ok := types.ParseType(typeName)
	if !ok {
		return def, &SyntaxError{
			Message: "unknown column type " + typeName,
			Line:    p.cur.Line,
			Col:     p.cur.Col,
		}
	}
	def.Type = kind

	// A length such as VARCHAR(255) parses but carries no meaning here.
	if p.acceptOperator("(") {
		for !p.atOperator(")") {
			if p.cur.Kind == EOF {
				return def, p.unexpected("')'")
			}
			p.advance()
		}
		if err := p.expectOperator(")"); err != nil {
			return def, err
		}
	}

	for {
		switch {
		case p.acceptKeyword("PRIMARY"):
			if err := p.expectKeyword("KEY"); err != nil {
				return def, err
			}
			def.PrimaryKey = true
			def.NotNull = true
		case p.acceptKeyword("NOT"):
			if err := p.expectKeyword("NULL"); err != nil {
				return def, err
			}
			def.NotNull = true
		case p.acceptKeyword("UNIQUE"):
			def.Unique = true
		default:
			return def, nil
		}
	}
}

func (p *parser) parseCreateIndex(unique bool) (Statement, error) {
	ifNotExists, err := p.parseIfNotExists()
	if err != nil {
		return nil, err
	}
	name, err := p.expectIdent("an index name")
	if err != nil {
		return nil, err
	}
	if err := p.expectKeyword("ON"); err != nil {
		return nil, err
	}
	table, err := p.expectIdent("a table name")
	if err != nil {
		return nil, err
	}
	if err := p.expectOperator("("); err != nil {
		return nil, err
	}
	column, err := p.expectIdent("a column name")
	if err != nil {
		return nil, err
	}
	if err := p.expectOperator(")"); err != nil {
		return nil, err
	}
	return &CreateIndex{
		Name: name, Table: table, Column: column,
		Unique: unique, IfNotExists: ifNotExists,
	}, nil
}

func (p *parser) parseDrop() (Statement, error) {
	if err := p.expectKeyword("DROP"); err != nil {
		return nil, err
	}
	isTable := true
	switch {
	case p.acceptKeyword("TABLE"):
	case p.acceptKeyword("INDEX"):
		isTable = false
	default:
		return nil, p.unexpected("TABLE or INDEX")
	}

	ifExists := false
	if p.acceptKeyword("IF") {
		if err := p.expectKeyword("EXISTS"); err != nil {
			return nil, err
		}
		ifExists = true
	}
	name, err := p.expectIdent("a name")
	if err != nil {
		return nil, err
	}
	if isTable {
		return &DropTable{Name: name, IfExists: ifExists}, nil
	}
	return &DropIndex{Name: name, IfExists: ifExists}, nil
}

func (p *parser) parseInsert() (Statement, error) {
	if err := p.expectKeyword("INSERT"); err != nil {
		return nil, err
	}
	if err := p.expectKeyword("INTO"); err != nil {
		return nil, err
	}
	table, err := p.expectIdent("a table name")
	if err != nil {
		return nil, err
	}
	stmt := &Insert{Table: table}

	if p.acceptOperator("(") {
		for {
			column, err := p.expectIdent("a column name")
			if err != nil {
				return nil, err
			}
			stmt.Columns = append(stmt.Columns, column)
			if !p.acceptOperator(",") {
				break
			}
		}
		if err := p.expectOperator(")"); err != nil {
			return nil, err
		}
	}

	if err := p.expectKeyword("VALUES"); err != nil {
		return nil, err
	}
	for {
		if err := p.expectOperator("("); err != nil {
			return nil, err
		}
		var row []Expr
		for {
			value, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			row = append(row, value)
			if !p.acceptOperator(",") {
				break
			}
		}
		if err := p.expectOperator(")"); err != nil {
			return nil, err
		}
		stmt.Rows = append(stmt.Rows, row)
		if !p.acceptOperator(",") {
			break
		}
	}
	return stmt, nil
}

func (p *parser) parseUpdate() (Statement, error) {
	if err := p.expectKeyword("UPDATE"); err != nil {
		return nil, err
	}
	table, err := p.expectIdent("a table name")
	if err != nil {
		return nil, err
	}
	if err := p.expectKeyword("SET"); err != nil {
		return nil, err
	}

	stmt := &Update{Table: table}
	for {
		column, err := p.expectIdent("a column name")
		if err != nil {
			return nil, err
		}
		if err := p.expectOperator("="); err != nil {
			return nil, err
		}
		value, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		stmt.Assignments = append(stmt.Assignments, Assignment{Column: column, Value: value})
		if !p.acceptOperator(",") {
			break
		}
	}

	if p.acceptKeyword("WHERE") {
		stmt.Where, err = p.parseExpr()
		if err != nil {
			return nil, err
		}
	}
	return stmt, nil
}

func (p *parser) parseDelete() (Statement, error) {
	if err := p.expectKeyword("DELETE"); err != nil {
		return nil, err
	}
	if err := p.expectKeyword("FROM"); err != nil {
		return nil, err
	}
	table, err := p.expectIdent("a table name")
	if err != nil {
		return nil, err
	}
	stmt := &Delete{Table: table}
	if p.acceptKeyword("WHERE") {
		stmt.Where, err = p.parseExpr()
		if err != nil {
			return nil, err
		}
	}
	return stmt, nil
}

func (p *parser) parseSelect() (Statement, error) {
	if err := p.expectKeyword("SELECT"); err != nil {
		return nil, err
	}
	stmt := &Select{Distinct: p.acceptKeyword("DISTINCT")}

	for {
		column, err := p.parseResultColumn()
		if err != nil {
			return nil, err
		}
		stmt.Columns = append(stmt.Columns, column)
		if !p.acceptOperator(",") {
			break
		}
	}

	if p.acceptKeyword("FROM") {
		table, err := p.parseTableRef()
		if err != nil {
			return nil, err
		}
		stmt.From = &table

		for {
			p.acceptKeyword("INNER")
			if !p.acceptKeyword("JOIN") {
				break
			}
			joined, err := p.parseTableRef()
			if err != nil {
				return nil, err
			}
			if err := p.expectKeyword("ON"); err != nil {
				return nil, err
			}
			on, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			stmt.Joins = append(stmt.Joins, Join{Table: joined, On: on})
		}
	}

	var err error
	if p.acceptKeyword("WHERE") {
		if stmt.Where, err = p.parseExpr(); err != nil {
			return nil, err
		}
	}

	if p.acceptKeyword("GROUP") {
		if err := p.expectKeyword("BY"); err != nil {
			return nil, err
		}
		for {
			group, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			stmt.GroupBy = append(stmt.GroupBy, group)
			if !p.acceptOperator(",") {
				break
			}
		}
		if p.acceptKeyword("HAVING") {
			if stmt.Having, err = p.parseExpr(); err != nil {
				return nil, err
			}
		}
	}

	if p.acceptKeyword("ORDER") {
		if err := p.expectKeyword("BY"); err != nil {
			return nil, err
		}
		for {
			term, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			order := OrderTerm{Expr: term}
			switch {
			case p.acceptKeyword("DESC"):
				order.Descending = true
			case p.acceptKeyword("ASC"):
			}
			stmt.OrderBy = append(stmt.OrderBy, order)
			if !p.acceptOperator(",") {
				break
			}
		}
	}

	if p.acceptKeyword("LIMIT") {
		if stmt.Limit, err = p.parseExpr(); err != nil {
			return nil, err
		}
		if p.acceptKeyword("OFFSET") {
			if stmt.Offset, err = p.parseExpr(); err != nil {
				return nil, err
			}
		}
	}
	return stmt, nil
}

func (p *parser) parseResultColumn() (ResultColumn, error) {
	if p.acceptOperator("*") {
		return ResultColumn{Star: true}, nil
	}

	// A qualified star, t.*, needs two tokens of context.
	if p.cur.Kind == Ident {
		name := p.cur.Text
		saved := *p.lex
		savedTok := p.cur
		if err := p.advance(); err != nil {
			return ResultColumn{}, err
		}
		if p.atOperator(".") {
			if err := p.advance(); err != nil {
				return ResultColumn{}, err
			}
			if p.acceptOperator("*") {
				return ResultColumn{Star: true, StarTable: name}, nil
			}
		}
		*p.lex = saved
		p.cur = savedTok
	}

	expr, err := p.parseExpr()
	if err != nil {
		return ResultColumn{}, err
	}
	column := ResultColumn{Expr: expr}
	if p.acceptKeyword("AS") {
		alias, err := p.expectIdent("an alias")
		if err != nil {
			return column, err
		}
		column.Alias = alias
	} else if p.cur.Kind == Ident {
		column.Alias = p.cur.Text
		if err := p.advance(); err != nil {
			return column, err
		}
	}
	return column, nil
}

func (p *parser) parseTableRef() (TableRef, error) {
	name, err := p.expectIdent("a table name")
	if err != nil {
		return TableRef{}, err
	}
	ref := TableRef{Name: name}
	if p.acceptKeyword("AS") {
		alias, err := p.expectIdent("an alias")
		if err != nil {
			return ref, err
		}
		ref.Alias = alias
	} else if p.cur.Kind == Ident {
		ref.Alias = p.cur.Text
		if err := p.advance(); err != nil {
			return ref, err
		}
	}
	return ref, nil
}

// --- expressions -----------------------------------------------------------

func (p *parser) parseExpr() (Expr, error) { return p.parseOr() }

func (p *parser) parseOr() (Expr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.acceptKeyword("OR") {
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: "OR", Left: left, Right: right}
	}
	return left, nil
}

func (p *parser) parseAnd() (Expr, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	for p.acceptKeyword("AND") {
		right, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: "AND", Left: left, Right: right}
	}
	return left, nil
}

func (p *parser) parseNot() (Expr, error) {
	if p.acceptKeyword("NOT") {
		operand, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		return &UnaryExpr{Op: "NOT", Operand: operand}, nil
	}
	return p.parseComparison()
}

var comparisonOps = map[string]bool{"=": true, "!=": true, "<": true, "<=": true, ">": true, ">=": true}

func (p *parser) parseComparison() (Expr, error) {
	left, err := p.parseConcat()
	if err != nil {
		return nil, err
	}

	for {
		switch {
		case p.cur.Kind == Operator && comparisonOps[p.cur.Text]:
			op := p.cur.Text
			if err := p.advance(); err != nil {
				return nil, err
			}
			right, err := p.parseConcat()
			if err != nil {
				return nil, err
			}
			left = &BinaryExpr{Op: op, Left: left, Right: right}

		case p.atKeyword("IS"):
			if err := p.advance(); err != nil {
				return nil, err
			}
			negate := p.acceptKeyword("NOT")
			if err := p.expectKeyword("NULL"); err != nil {
				return nil, err
			}
			left = &IsNullExpr{Operand: left, Negate: negate}

		case p.atKeyword("IN", "LIKE", "BETWEEN"):
			left, err = p.parseMatch(left, false)
			if err != nil {
				return nil, err
			}

		case p.atKeyword("NOT"):
			// Only NOT IN, NOT LIKE and NOT BETWEEN are postfix here.
			saved := *p.lex
			savedTok := p.cur
			if err := p.advance(); err != nil {
				return nil, err
			}
			if !p.atKeyword("IN", "LIKE", "BETWEEN") {
				*p.lex = saved
				p.cur = savedTok
				return left, nil
			}
			left, err = p.parseMatch(left, true)
			if err != nil {
				return nil, err
			}

		default:
			return left, nil
		}
	}
}

func (p *parser) parseMatch(left Expr, negate bool) (Expr, error) {
	switch {
	case p.acceptKeyword("LIKE"):
		pattern, err := p.parseConcat()
		if err != nil {
			return nil, err
		}
		op := "LIKE"
		if negate {
			op = "NOT LIKE"
		}
		return &BinaryExpr{Op: op, Left: left, Right: pattern}, nil

	case p.acceptKeyword("IN"):
		if err := p.expectOperator("("); err != nil {
			return nil, err
		}
		expr := &InExpr{Operand: left, Negate: negate}
		for {
			item, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			expr.List = append(expr.List, item)
			if !p.acceptOperator(",") {
				break
			}
		}
		return expr, p.expectOperator(")")

	case p.acceptKeyword("BETWEEN"):
		low, err := p.parseConcat()
		if err != nil {
			return nil, err
		}
		if err := p.expectKeyword("AND"); err != nil {
			return nil, err
		}
		high, err := p.parseConcat()
		if err != nil {
			return nil, err
		}
		return &BetweenExpr{Operand: left, Low: low, High: high, Negate: negate}, nil
	}
	return nil, p.unexpected("IN, LIKE or BETWEEN")
}

func (p *parser) parseConcat() (Expr, error) {
	left, err := p.parseAdditive()
	if err != nil {
		return nil, err
	}
	for p.atOperator("||") {
		if err := p.advance(); err != nil {
			return nil, err
		}
		right, err := p.parseAdditive()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: "||", Left: left, Right: right}
	}
	return left, nil
}

func (p *parser) parseAdditive() (Expr, error) {
	left, err := p.parseMultiplicative()
	if err != nil {
		return nil, err
	}
	for p.atOperator("+") || p.atOperator("-") {
		op := p.cur.Text
		if err := p.advance(); err != nil {
			return nil, err
		}
		right, err := p.parseMultiplicative()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: op, Left: left, Right: right}
	}
	return left, nil
}

func (p *parser) parseMultiplicative() (Expr, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.atOperator("*") || p.atOperator("/") || p.atOperator("%") {
		op := p.cur.Text
		if err := p.advance(); err != nil {
			return nil, err
		}
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: op, Left: left, Right: right}
	}
	return left, nil
}

func (p *parser) parseUnary() (Expr, error) {
	if p.atOperator("-") || p.atOperator("+") {
		op := p.cur.Text
		if err := p.advance(); err != nil {
			return nil, err
		}
		operand, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		if op == "+" {
			return operand, nil
		}
		return &UnaryExpr{Op: "-", Operand: operand}, nil
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() (Expr, error) {
	tok := p.cur
	switch tok.Kind {
	case Number:
		value, err := literalFromNumber(tok)
		if err != nil {
			return nil, err
		}
		return &Literal{Value: value}, p.advance()

	case String:
		return &Literal{Value: types.NewText(tok.Text)}, p.advance()

	case Keyword:
		switch tok.Text {
		case "NULL":
			return &Literal{Value: types.NullValue}, p.advance()
		case "TRUE":
			return &Literal{Value: types.NewBool(true)}, p.advance()
		case "FALSE":
			return &Literal{Value: types.NewBool(false)}, p.advance()
		}

	case Ident:
		name := tok.Text
		if err := p.advance(); err != nil {
			return nil, err
		}
		if p.atOperator(".") {
			if err := p.advance(); err != nil {
				return nil, err
			}
			column, err := p.expectIdent("a column name")
			if err != nil {
				return nil, err
			}
			return &ColumnRef{Table: name, Name: column}, nil
		}
		if p.atOperator("(") {
			return p.parseCall(name)
		}
		return &ColumnRef{Name: name}, nil

	case Operator:
		if tok.Text == "(" {
			if err := p.advance(); err != nil {
				return nil, err
			}
			inner, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			return inner, p.expectOperator(")")
		}
	}
	return nil, p.unexpected("an expression")
}

func (p *parser) parseCall(name string) (Expr, error) {
	if err := p.expectOperator("("); err != nil {
		return nil, err
	}
	call := &FuncCall{Name: strings.ToUpper(name)}

	if p.acceptOperator("*") {
		call.Star = true
		return call, p.expectOperator(")")
	}
	if p.acceptOperator(")") {
		return call, nil
	}
	call.Distinct = p.acceptKeyword("DISTINCT")
	for {
		arg, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		call.Args = append(call.Args, arg)
		if !p.acceptOperator(",") {
			break
		}
	}
	return call, p.expectOperator(")")
}
