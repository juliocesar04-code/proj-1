package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/juliocesar04-code/proj-1/internal/engine"
)

func newShell(t *testing.T) (*shell, *strings.Builder) {
	t.Helper()
	db, err := engine.Open(filepath.Join(t.TempDir(), "shell.db"), engine.Options{NoSync: true})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	out := &strings.Builder{}
	return &shell{db: db, session: db.Session(), out: out}, out
}

func TestReplRunsAScript(t *testing.T) {
	sh, out := newShell(t)

	script := `
CREATE TABLE produtos (
    id INTEGER PRIMARY KEY,
    nome TEXT NOT NULL UNIQUE,
    preco FLOAT
);
INSERT INTO produtos (nome, preco) VALUES ('teclado', 199.9), ('monitor', 1299);
SELECT nome, preco FROM produtos ORDER BY preco DESC;
.tables
.schema produtos
.indexes
.exit
`
	if err := sh.repl(strings.NewReader(script)); err != nil {
		t.Fatalf("repl: %v", err)
	}

	text := out.String()
	for _, want := range []string{
		"CREATE TABLE",
		"INSERT 2",
		"nome    | preco",
		"monitor |  1299",
		"teclado | 199.9",
		"(2 rows)",
		"produtos",
		"produtos_nome_key on produtos(nome) unique",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output is missing %q:\n%s", want, text)
		}
	}
}

func TestReplReportsErrorsAndKeepsGoing(t *testing.T) {
	sh, out := newShell(t)

	script := `
SELECT * FROM inexistente;
.nope
SELECT 1 + 1;
`
	if err := sh.repl(strings.NewReader(script)); err != nil {
		t.Fatalf("repl: %v", err)
	}

	text := out.String()
	if !strings.Contains(text, "no such table: inexistente") {
		t.Errorf("the failing query was not reported:\n%s", text)
	}
	if !strings.Contains(text, "unknown command .nope") {
		t.Errorf("the unknown dot command was not reported:\n%s", text)
	}
	if !strings.Contains(text, "2") {
		t.Errorf("the shell stopped after the error:\n%s", text)
	}
}

func TestTransactionPrompt(t *testing.T) {
	sh, _ := newShell(t)

	if got := sh.prompt(false); got != "corvo> " {
		t.Fatalf("prompt outside a transaction is %q", got)
	}
	if got := sh.prompt(true); got != "   ...> " {
		t.Fatalf("continuation prompt is %q", got)
	}
	if err := sh.execute("BEGIN;"); err != nil {
		t.Fatal(err)
	}
	if got := sh.prompt(false); got != "corvo*> " {
		t.Fatalf("prompt inside a transaction is %q", got)
	}
	if err := sh.execute("ROLLBACK;"); err != nil {
		t.Fatal(err)
	}
}

func TestTimerToggle(t *testing.T) {
	sh, out := newShell(t)

	if _, err := sh.dotCommand(".timer on"); err != nil {
		t.Fatal(err)
	}
	if !sh.timing {
		t.Fatal("the timer should be on")
	}
	if err := sh.execute("SELECT 1;"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "time:") {
		t.Fatalf("no timing in the output:\n%s", out.String())
	}

	if _, err := sh.dotCommand(".timer"); err != nil {
		t.Fatal(err)
	}
	if sh.timing {
		t.Fatal("the timer should have been turned off")
	}
}

func TestDropIndexAndSchemaOfMissingTable(t *testing.T) {
	sh, out := newShell(t)

	if err := sh.execute(`
		CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER);
		CREATE INDEX idx_t_v ON t (v);
		INSERT INTO t (v) VALUES (1), (2), (3);
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := sh.dotCommand(".indexes"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "idx_t_v on t(v)") {
		t.Fatalf("the index is missing from .indexes:\n%s", out.String())
	}

	if err := sh.execute("DROP INDEX idx_t_v;"); err != nil {
		t.Fatal(err)
	}
	if err := sh.execute("SELECT COUNT(*) FROM t WHERE v = 2;"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "1") {
		t.Fatalf("the query stopped working after the index was dropped:\n%s", out.String())
	}

	if _, err := sh.dotCommand(".schema inexistente"); err == nil {
		t.Fatal("expected an error for an unknown table")
	}
	if err := sh.execute("DROP INDEX inexistente;"); err == nil {
		t.Fatal("expected an error for an unknown index")
	}
	if err := sh.execute("DROP INDEX IF EXISTS inexistente;"); err != nil {
		t.Fatalf("IF EXISTS should not fail: %v", err)
	}
}

func TestPadAlignment(t *testing.T) {
	if got := pad("ab", 5, false); got != "ab   " {
		t.Fatalf("left aligned padding produced %q", got)
	}
	if got := pad("12", 5, true); got != "   12" {
		t.Fatalf("right aligned padding produced %q", got)
	}
	if got := pad("café", 4, false); got != "café" {
		t.Fatalf("multibyte padding produced %q", got)
	}
}
