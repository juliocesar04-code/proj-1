package engine

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/juliocesar04-code/proj-1/internal/types"
)

type harness struct {
	t       *testing.T
	db      *DB
	session *Session
	path    string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	path := filepath.Join(t.TempDir(), "engine.db")
	db, err := Open(path, Options{NoSync: true})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return &harness{t: t, db: db, session: db.Session(), path: path}
}

func (h *harness) exec(query string) []Result {
	h.t.Helper()
	results, err := h.session.Exec(query)
	if err != nil {
		h.t.Fatalf("exec %q: %v", query, err)
	}
	return results
}

func (h *harness) query(query string) Result {
	h.t.Helper()
	results := h.exec(query)
	return results[len(results)-1]
}

// rows renders a result as "a|b|c" lines so tests stay readable.
func (h *harness) rows(query string) []string {
	h.t.Helper()
	result := h.query(query)
	out := make([]string, 0, len(result.Rows))
	for _, row := range result.Rows {
		parts := make([]string, len(row))
		for i, value := range row {
			parts[i] = value.String()
		}
		out = append(out, strings.Join(parts, "|"))
	}
	return out
}

func (h *harness) expectError(query, contains string) {
	h.t.Helper()
	_, err := h.session.Exec(query)
	if err == nil {
		h.t.Fatalf("%q should have failed", query)
	}
	if !strings.Contains(err.Error(), contains) {
		h.t.Fatalf("%q failed with %q, expected it to mention %q", query, err, contains)
	}
}

func equal(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d rows %v, want %d rows %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("row %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func (h *harness) seed() {
	h.exec(`
		CREATE TABLE users (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			email TEXT UNIQUE,
			age INTEGER,
			active BOOLEAN
		);
		CREATE TABLE orders (
			id INTEGER PRIMARY KEY,
			user_id INTEGER NOT NULL,
			total FLOAT NOT NULL,
			status TEXT NOT NULL
		);
		INSERT INTO users (id, name, email, age, active) VALUES
			(1, 'Ana',   'ana@example.com',   34, TRUE),
			(2, 'Bruno', 'bruno@example.com', 27, TRUE),
			(3, 'Carla', 'carla@example.com', 41, FALSE),
			(4, 'Diego', NULL,                19, TRUE);
		INSERT INTO orders (id, user_id, total, status) VALUES
			(1, 1, 120.50, 'paid'),
			(2, 1,  49.90, 'paid'),
			(3, 2, 310.00, 'pending'),
			(4, 3,  15.00, 'paid'),
			(5, 1,  80.10, 'cancelled');
	`)
}

func TestCreateInsertSelect(t *testing.T) {
	h := newHarness(t)
	h.seed()

	equal(t, h.rows("SELECT name, age FROM users ORDER BY age"), []string{
		"Diego|19", "Bruno|27", "Ana|34", "Carla|41",
	})
	equal(t, h.rows("SELECT COUNT(*) FROM users"), []string{"4"})
	equal(t, h.rows("SELECT name FROM users WHERE age > 30 ORDER BY name"), []string{"Ana", "Carla"})
	equal(t, h.rows("SELECT name FROM users WHERE email IS NULL"), []string{"Diego"})
}

func TestAutoIncrementRowID(t *testing.T) {
	h := newHarness(t)
	h.exec("CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT)")
	h.exec("INSERT INTO notes (body) VALUES ('first'), ('second')")
	h.exec("INSERT INTO notes (id, body) VALUES (10, 'tenth')")
	h.exec("INSERT INTO notes (body) VALUES ('eleventh')")

	equal(t, h.rows("SELECT id, body FROM notes ORDER BY id"), []string{
		"1|first", "2|second", "10|tenth", "11|eleventh",
	})
}

func TestConstraints(t *testing.T) {
	h := newHarness(t)
	h.seed()

	h.expectError("INSERT INTO users (id, name) VALUES (1, 'Duplicate')", "duplicate primary key")
	h.expectError("INSERT INTO users (id, name, email) VALUES (9, 'X', 'ana@example.com')", "unique index")
	h.expectError("INSERT INTO users (id, name) VALUES (9, NULL)", "cannot be NULL")
	h.expectError("INSERT INTO users (id, name, age) VALUES (9, 'X', 'not a number')", "cannot store")

	// Two NULLs in a unique column are allowed, as in standard SQL.
	h.exec("INSERT INTO users (id, name, email) VALUES (9, 'Elisa', NULL)")
	equal(t, h.rows("SELECT COUNT(*) FROM users WHERE email IS NULL"), []string{"2"})
}

func TestUpdateAndDelete(t *testing.T) {
	h := newHarness(t)
	h.seed()

	result := h.query("UPDATE users SET age = age + 1 WHERE active = TRUE")
	if result.RowsAffected != 3 {
		t.Fatalf("updated %d rows, want 3", result.RowsAffected)
	}
	equal(t, h.rows("SELECT name, age FROM users ORDER BY id"), []string{
		"Ana|35", "Bruno|28", "Carla|41", "Diego|20",
	})

	result = h.query("DELETE FROM orders WHERE status = 'cancelled'")
	if result.RowsAffected != 1 {
		t.Fatalf("deleted %d rows, want 1", result.RowsAffected)
	}
	equal(t, h.rows("SELECT COUNT(*) FROM orders"), []string{"4"})

	// An update that changes the unique column must keep the index correct.
	h.exec("UPDATE users SET email = 'novo@example.com' WHERE id = 1")
	equal(t, h.rows("SELECT name FROM users WHERE email = 'novo@example.com'"), []string{"Ana"})
	equal(t, h.rows("SELECT COUNT(*) FROM users WHERE email = 'ana@example.com'"), []string{"0"})
	h.exec("INSERT INTO users (id, name, email) VALUES (20, 'Reuse', 'ana@example.com')")
}

func TestAggregatesAndGrouping(t *testing.T) {
	h := newHarness(t)
	h.seed()

	equal(t, h.rows(`
		SELECT status, COUNT(*), ROUND(SUM(total), 2)
		FROM orders GROUP BY status ORDER BY status`), []string{
		"cancelled|1|80.1", "paid|3|185.4", "pending|1|310",
	})

	equal(t, h.rows(`
		SELECT status FROM orders GROUP BY status HAVING COUNT(*) > 1`), []string{"paid"})

	equal(t, h.rows("SELECT MIN(age), MAX(age), COUNT(email) FROM users"), []string{"19|41|3"})
	equal(t, h.rows("SELECT COUNT(DISTINCT status) FROM orders"), []string{"3"})
	equal(t, h.rows("SELECT AVG(total) FROM orders WHERE status = 'nothing'"), []string{"NULL"})
	equal(t, h.rows("SELECT COUNT(*) FROM orders WHERE status = 'nothing'"), []string{"0"})

	h.expectError("SELECT name, COUNT(*) FROM users", "must appear in GROUP BY")
	h.expectError("SELECT * FROM users WHERE COUNT(*) > 1", "not allowed in WHERE")
}

func TestJoins(t *testing.T) {
	h := newHarness(t)
	h.seed()

	equal(t, h.rows(`
		SELECT u.name, COUNT(*) AS orders, ROUND(SUM(o.total), 2) AS spent
		FROM users u
		JOIN orders o ON o.user_id = u.id
		WHERE o.status = 'paid'
		GROUP BY u.name
		ORDER BY spent DESC`), []string{
		"Ana|2|170.4", "Carla|1|15",
	})

	equal(t, h.rows(`
		SELECT u.name, o.total FROM users u
		JOIN orders o ON o.user_id = u.id AND o.total > 100
		ORDER BY o.total`), []string{"Ana|120.5", "Bruno|310"})
}

func TestIndexIsUsedAndCorrect(t *testing.T) {
	h := newHarness(t)
	h.seed()
	h.exec("CREATE INDEX idx_orders_status ON orders (status)")

	plan := strings.Join(h.rows("EXPLAIN SELECT * FROM orders WHERE status = 'paid'"), "\n")
	if !strings.Contains(plan, "Index Scan on orders using idx_orders_status") {
		t.Fatalf("the planner ignored the index:\n%s", plan)
	}
	equal(t, h.rows("SELECT id FROM orders WHERE status = 'paid' ORDER BY id"), []string{"1", "2", "4"})

	plan = strings.Join(h.rows("EXPLAIN SELECT * FROM users WHERE id = 2"), "\n")
	if !strings.Contains(plan, "Primary Key Scan on users (id = 2)") {
		t.Fatalf("the planner ignored the primary key:\n%s", plan)
	}

	// A range on an indexed column narrows the scan without losing rows.
	h.exec("CREATE INDEX idx_orders_total ON orders (total)")
	plan = strings.Join(h.rows("EXPLAIN SELECT id FROM orders WHERE total >= 49.9 AND total < 310"), "\n")
	if !strings.Contains(plan, "Index Scan on orders using idx_orders_total") {
		t.Fatalf("the planner ignored the range index:\n%s", plan)
	}
	equal(t, h.rows("SELECT id FROM orders WHERE total >= 49.9 AND total < 310 ORDER BY id"),
		[]string{"1", "2", "5"})

	// An ORDER BY that matches an index does not need a sort.
	plan = strings.Join(h.rows("EXPLAIN SELECT id FROM orders ORDER BY total DESC"), "\n")
	if strings.Contains(plan, "Sort:") {
		t.Fatalf("the sort should have been dropped:\n%s", plan)
	}
	equal(t, h.rows("SELECT id FROM orders ORDER BY total DESC"), []string{"3", "1", "5", "2", "4"})

	plan = strings.Join(h.rows("EXPLAIN SELECT u.name FROM users u JOIN orders o ON o.user_id = u.id"), "\n")
	if !strings.Contains(plan, "Hash Join") {
		t.Fatalf("an equality join should become a hash join:\n%s", plan)
	}
}

// TestIndexStaysConsistent compares an indexed column against a full scan of
// the same predicate after a long run of writes.
func TestIndexStaysConsistent(t *testing.T) {
	h := newHarness(t)
	h.exec("CREATE TABLE items (id INTEGER PRIMARY KEY, bucket INTEGER, label TEXT)")

	var insert strings.Builder
	insert.WriteString("INSERT INTO items (id, bucket, label) VALUES ")
	for i := 1; i <= 800; i++ {
		if i > 1 {
			insert.WriteString(", ")
		}
		fmt.Fprintf(&insert, "(%d, %d, 'label-%d')", i, i%17, i)
	}
	h.exec(insert.String())
	h.exec("CREATE INDEX idx_items_bucket ON items (bucket)")

	h.exec("DELETE FROM items WHERE id % 3 = 0")
	h.exec("UPDATE items SET bucket = bucket + 100 WHERE id % 5 = 0")

	for bucket := 0; bucket < 120; bucket++ {
		indexed := h.rows(fmt.Sprintf("SELECT COUNT(*) FROM items WHERE bucket = %d", bucket))
		scanned := h.rows(fmt.Sprintf("SELECT COUNT(*) FROM items WHERE bucket + 0 = %d", bucket))
		if indexed[0] != scanned[0] {
			t.Fatalf("bucket %d: index says %s, full scan says %s", bucket, indexed[0], scanned[0])
		}
	}
}

func TestTransactions(t *testing.T) {
	h := newHarness(t)
	h.seed()

	h.exec("BEGIN")
	h.exec("DELETE FROM users")
	equal(t, h.rows("SELECT COUNT(*) FROM users"), []string{"0"})
	h.exec("ROLLBACK")
	equal(t, h.rows("SELECT COUNT(*) FROM users"), []string{"4"})

	h.exec("BEGIN")
	h.exec("INSERT INTO users (id, name) VALUES (50, 'Fernanda')")
	h.exec("COMMIT")
	equal(t, h.rows("SELECT name FROM users WHERE id = 50"), []string{"Fernanda"})

	h.expectError("COMMIT", "no transaction")

	// A rolled back CREATE TABLE must leave no trace in the schema.
	h.exec("BEGIN")
	h.exec("CREATE TABLE temporary (id INTEGER PRIMARY KEY)")
	h.exec("ROLLBACK")
	h.expectError("SELECT * FROM temporary", "no such table")
}

func TestDurability(t *testing.T) {
	path := filepath.Join(t.TempDir(), "durable.db")
	db, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	session := db.Session()
	if _, err := session.Exec(`
		CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT UNIQUE);
		INSERT INTO t (v) VALUES ('a'), ('b'), ('c');
		CREATE INDEX idx_t_v ON t (v);
	`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()

	h := &harness{t: t, db: reopened, session: reopened.Session(), path: path}
	equal(t, h.rows("SELECT id, v FROM t ORDER BY id"), []string{"1|a", "2|b", "3|c"})
	h.expectError("INSERT INTO t (v) VALUES ('a')", "unique index")
	h.exec("INSERT INTO t (v) VALUES ('d')")
	equal(t, h.rows("SELECT v FROM t WHERE v = 'd'"), []string{"d"})
}

func TestDropTableReleasesEverything(t *testing.T) {
	h := newHarness(t)
	h.seed()
	h.exec("CREATE INDEX idx_users_age ON users (age)")
	h.exec("DROP TABLE users")
	h.expectError("SELECT * FROM users", "no such table")
	h.exec("DROP TABLE IF EXISTS users")
	h.exec("CREATE TABLE users (id INTEGER PRIMARY KEY)")
	equal(t, h.rows("SELECT COUNT(*) FROM users"), []string{"0"})
}

func TestExpressionsAndFunctions(t *testing.T) {
	h := newHarness(t)
	h.seed()

	equal(t, h.rows("SELECT UPPER(name) || ' <' || email || '>' FROM users WHERE id = 1"),
		[]string{"ANA <ana@example.com>"})
	equal(t, h.rows("SELECT LENGTH('café'), ABS(-3), ROUND(2.567, 2), COALESCE(NULL, 'x')"),
		[]string{"4|3|2.57|x"})
	equal(t, h.rows("SELECT SUBSTR('database', 5, 4)"), []string{"base"})
	equal(t, h.rows("SELECT name FROM users WHERE name LIKE 'A%' OR name LIKE '%go'"),
		[]string{"Ana", "Diego"})
	equal(t, h.rows("SELECT name FROM users WHERE id IN (2, 3) ORDER BY id"), []string{"Bruno", "Carla"})
	equal(t, h.rows("SELECT name FROM users WHERE age BETWEEN 20 AND 35 ORDER BY name"),
		[]string{"Ana", "Bruno"})
	equal(t, h.rows("SELECT 7 / 2, 7 % 2, 7.0 / 2, 1 / 0"), []string{"3|1|3.5|NULL"})

	// NULL comparisons never pass a WHERE clause.
	equal(t, h.rows("SELECT COUNT(*) FROM users WHERE email = NULL"), []string{"0"})
	equal(t, h.rows("SELECT DISTINCT active FROM users ORDER BY active"), []string{"false", "true"})
	equal(t, h.rows("SELECT 2 + 3 * 4"), []string{"14"})
}

func TestLimitAndOffset(t *testing.T) {
	h := newHarness(t)
	h.seed()

	equal(t, h.rows("SELECT id FROM orders ORDER BY id LIMIT 2"), []string{"1", "2"})
	equal(t, h.rows("SELECT id FROM orders ORDER BY id LIMIT 2 OFFSET 3"), []string{"4", "5"})
	equal(t, h.rows("SELECT id FROM orders ORDER BY id LIMIT 0"), nil)
}

func TestErrorsAreReported(t *testing.T) {
	h := newHarness(t)
	h.seed()

	h.expectError("SELECT * FROM missing", "no such table")
	h.expectError("SELECT missing FROM users", "no such column")
	h.expectError("CREATE TABLE users (id INTEGER)", "already exists")
	h.expectError("CREATE TABLE t (a INTEGER, a TEXT)", "duplicate column")
	h.expectError("CREATE INDEX i ON users (missing)", "no such column")
	h.expectError("SELECT id FROM users u JOIN orders o ON o.user_id = u.id WHERE id > 0", "ambiguous")
}

func TestLargeValues(t *testing.T) {
	h := newHarness(t)
	h.exec("CREATE TABLE docs (id INTEGER PRIMARY KEY, body TEXT)")

	body := strings.Repeat("conteúdo longo ", 5000)
	if _, err := h.session.Exec(fmt.Sprintf("INSERT INTO docs (id, body) VALUES (1, '%s')", body)); err != nil {
		t.Fatal(err)
	}
	result := h.query("SELECT body FROM docs WHERE id = 1")
	if got := result.Rows[0][0].S; got != body {
		t.Fatalf("stored %d bytes, read back %d", len(body), len(got))
	}
}

func BenchmarkInsert(b *testing.B) {
	path := filepath.Join(b.TempDir(), "bench.db")
	db, err := Open(path, Options{NoSync: true, CacheSize: 4096})
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()

	session := db.Session()
	if _, err := session.Exec("CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)"); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	if _, err := session.Exec("BEGIN"); err != nil {
		b.Fatal(err)
	}
	for i := 0; i < b.N; i++ {
		if _, err := session.Exec(fmt.Sprintf("INSERT INTO t (v) VALUES ('row-%d')", i)); err != nil {
			b.Fatal(err)
		}
	}
	if _, err := session.Exec("COMMIT"); err != nil {
		b.Fatal(err)
	}
}

func BenchmarkIndexedLookup(b *testing.B) {
	path := filepath.Join(b.TempDir(), "bench.db")
	db, err := Open(path, Options{NoSync: true, CacheSize: 4096})
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()

	session := db.Session()
	session.Exec("CREATE TABLE t (id INTEGER PRIMARY KEY, v INTEGER)")
	session.Exec("BEGIN")
	for i := 0; i < 20000; i++ {
		if _, err := session.Exec(fmt.Sprintf("INSERT INTO t (v) VALUES (%d)", i)); err != nil {
			b.Fatal(err)
		}
	}
	session.Exec("COMMIT")
	session.Exec("CREATE INDEX idx_t_v ON t (v)")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result, err := session.Exec(fmt.Sprintf("SELECT id FROM t WHERE v = %d", i%20000))
		if err != nil {
			b.Fatal(err)
		}
		if len(result[0].Rows) != 1 {
			b.Fatalf("expected one row, got %d", len(result[0].Rows))
		}
	}
}

var _ = types.NullValue
