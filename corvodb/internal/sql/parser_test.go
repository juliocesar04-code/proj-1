package sql

import (
	"strings"
	"testing"
)

func parseOne(t *testing.T, input string) Statement {
	t.Helper()
	stmt, err := ParseStatement(input)
	if err != nil {
		t.Fatalf("parse %q: %v", input, err)
	}
	return stmt
}

func TestParseCreateTable(t *testing.T) {
	stmt := parseOne(t, `CREATE TABLE users (
		id INTEGER PRIMARY KEY,
		email VARCHAR(255) NOT NULL UNIQUE,
		score FLOAT,
		active BOOLEAN
	)`).(*CreateTable)

	if stmt.Name != "users" || len(stmt.Columns) != 4 {
		t.Fatalf("unexpected table: %+v", stmt)
	}
	if !stmt.Columns[0].PrimaryKey || !stmt.Columns[0].NotNull {
		t.Fatal("primary key should imply not null")
	}
	if !stmt.Columns[1].Unique || !stmt.Columns[1].NotNull {
		t.Fatalf("column constraints lost: %+v", stmt.Columns[1])
	}
}

func TestParseSelectShape(t *testing.T) {
	stmt := parseOne(t, `
		SELECT u.name, COUNT(*) AS total
		FROM users u
		INNER JOIN orders o ON o.user_id = u.id
		WHERE u.active = TRUE AND o.total BETWEEN 10 AND 100
		GROUP BY u.name
		HAVING COUNT(*) > 2
		ORDER BY total DESC, u.name
		LIMIT 10 OFFSET 5`).(*Select)

	if stmt.From == nil || stmt.From.Name != "users" || stmt.From.Alias != "u" {
		t.Fatalf("FROM parsed as %+v", stmt.From)
	}
	if len(stmt.Joins) != 1 || stmt.Joins[0].Table.Alias != "o" {
		t.Fatalf("JOIN parsed as %+v", stmt.Joins)
	}
	if len(stmt.Columns) != 2 || stmt.Columns[1].Alias != "total" {
		t.Fatalf("result columns parsed as %+v", stmt.Columns)
	}
	if len(stmt.GroupBy) != 1 || stmt.Having == nil {
		t.Fatal("GROUP BY / HAVING lost")
	}
	if len(stmt.OrderBy) != 2 || !stmt.OrderBy[0].Descending || stmt.OrderBy[1].Descending {
		t.Fatalf("ORDER BY parsed as %+v", stmt.OrderBy)
	}
	if stmt.Limit == nil || stmt.Offset == nil {
		t.Fatal("LIMIT / OFFSET lost")
	}
}

func TestOperatorPrecedence(t *testing.T) {
	cases := map[string]string{
		"SELECT 1 + 2 * 3":             "(1 + (2 * 3))",
		"SELECT (1 + 2) * 3":           "((1 + 2) * 3)",
		"SELECT a = 1 AND b = 2 OR c":  "(((a = 1) AND (b = 2)) OR c)",
		"SELECT NOT a = 1":             "NOT (a = 1)",
		"SELECT -a + b":                "(-a + b)",
		"SELECT a || b || c":           "((a || b) || c)",
		"SELECT a NOT IN (1, 2)":       "a NOT IN (1, 2)",
		"SELECT a IS NOT NULL":         "a IS NOT NULL",
		"SELECT a NOT BETWEEN 1 AND 2": "a NOT BETWEEN 1 AND 2",
	}
	for input, want := range cases {
		stmt := parseOne(t, input).(*Select)
		if got := stmt.Columns[0].Expr.String(); got != want {
			t.Errorf("%s\n got: %s\nwant: %s", input, got, want)
		}
	}
}

func TestParseStarForms(t *testing.T) {
	stmt := parseOne(t, "SELECT *, u.* FROM users u").(*Select)
	if !stmt.Columns[0].Star || stmt.Columns[0].StarTable != "" {
		t.Fatalf("plain star parsed as %+v", stmt.Columns[0])
	}
	if !stmt.Columns[1].Star || stmt.Columns[1].StarTable != "u" {
		t.Fatalf("qualified star parsed as %+v", stmt.Columns[1])
	}
}

func TestParseMultipleStatements(t *testing.T) {
	stmts, err := Parse("BEGIN; INSERT INTO t (a) VALUES (1), (2); COMMIT;")
	if err != nil {
		t.Fatal(err)
	}
	if len(stmts) != 3 {
		t.Fatalf("got %d statements", len(stmts))
	}
	insert := stmts[1].(*Insert)
	if len(insert.Rows) != 2 {
		t.Fatalf("got %d rows", len(insert.Rows))
	}
}

func TestStringLiteralEscaping(t *testing.T) {
	stmt := parseOne(t, "SELECT 'it''s here'").(*Select)
	if got := stmt.Columns[0].Expr.(*Literal).Value.S; got != "it's here" {
		t.Fatalf("got %q", got)
	}
}

func TestComments(t *testing.T) {
	stmt := parseOne(t, "SELECT 1 -- trailing\n/* block */ , 2").(*Select)
	if len(stmt.Columns) != 2 {
		t.Fatalf("comments confused the lexer: %+v", stmt.Columns)
	}
}

func TestSyntaxErrorsReportPosition(t *testing.T) {
	cases := []string{
		"SELECT FROM",
		"SELECT * FROM",
		"INSERT INTO t VALUES",
		"CREATE TABLE t (a NOTATYPE)",
		"SELECT 'unterminated",
		"SELECT * FROM t WHERE",
		"DROP",
	}
	for _, input := range cases {
		if _, err := ParseStatement(input); err == nil {
			t.Errorf("%q parsed without an error", input)
		} else if !strings.Contains(err.Error(), "line") {
			t.Errorf("%q: error without a position: %v", input, err)
		}
	}
}

// FuzzParse makes sure no input can panic the parser.
func FuzzParse(f *testing.F) {
	seeds := []string{
		"SELECT * FROM t WHERE a = 1",
		"INSERT INTO t (a, b) VALUES (1, 'x')",
		"CREATE UNIQUE INDEX i ON t (a)",
		"UPDATE t SET a = a + 1 WHERE b LIKE 'x%'",
		"EXPLAIN SELECT COUNT(*) FROM t GROUP BY a HAVING COUNT(*) > 1",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		_, _ = Parse(input)
	})
}
