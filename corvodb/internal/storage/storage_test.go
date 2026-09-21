package storage

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func openTemp(t *testing.T, opts Options) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path, opts)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func key(i int) []byte { return []byte(fmt.Sprintf("key-%08d", i)) }
func val(i int) []byte { return []byte(fmt.Sprintf("value-%d", i)) }

func TestPutGetDelete(t *testing.T) {
	s := openTemp(t, Options{})

	var root uint64
	if err := s.Update(func(tx *Tx) error {
		tree, err := tx.CreateTree()
		if err != nil {
			return err
		}
		for i := 0; i < 5000; i++ {
			if err := tree.Put(key(i), val(i)); err != nil {
				return err
			}
		}
		root = tree.Root()
		return nil
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	if err := s.View(func(tx *Tx) error {
		tree := tx.Tree(root)
		for i := 0; i < 5000; i++ {
			got, err := tree.Get(key(i))
			if err != nil {
				return err
			}
			if !bytes.Equal(got, val(i)) {
				return fmt.Errorf("key %d: got %q want %q", i, got, val(i))
			}
		}
		missing, err := tree.Get([]byte("absent"))
		if err != nil {
			return err
		}
		if missing != nil {
			return fmt.Errorf("expected nil for absent key, got %q", missing)
		}
		return nil
	}); err != nil {
		t.Fatalf("read back: %v", err)
	}

	if err := s.Update(func(tx *Tx) error {
		tree := tx.Tree(root)
		for i := 0; i < 5000; i += 2 {
			ok, err := tree.Delete(key(i))
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("key %d was not deleted", i)
			}
		}
		root = tree.Root()
		return nil
	}); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if err := s.View(func(tx *Tx) error {
		tree := tx.Tree(root)
		for i := 0; i < 5000; i++ {
			got, err := tree.Get(key(i))
			if err != nil {
				return err
			}
			if i%2 == 0 && got != nil {
				return fmt.Errorf("key %d should be gone", i)
			}
			if i%2 == 1 && !bytes.Equal(got, val(i)) {
				return fmt.Errorf("key %d: got %q", i, got)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("verify delete: %v", err)
	}
}

func TestRandomWorkloadMatchesMap(t *testing.T) {
	s := openTemp(t, Options{CacheSize: 64})
	rng := rand.New(rand.NewSource(7))

	model := map[string]string{}
	var root uint64
	if err := s.Update(func(tx *Tx) error {
		tree, err := tx.CreateTree()
		if err != nil {
			return err
		}
		root = tree.Root()
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	for round := 0; round < 20; round++ {
		if err := s.Update(func(tx *Tx) error {
			tree := tx.Tree(root)
			for i := 0; i < 500; i++ {
				k := fmt.Sprintf("k%04d", rng.Intn(1500))
				switch rng.Intn(4) {
				case 0:
					ok, err := tree.Delete([]byte(k))
					if err != nil {
						return err
					}
					if ok {
						delete(model, k)
					} else if _, exists := model[k]; exists {
						return fmt.Errorf("delete missed existing key %q", k)
					}
				default:
					v := fmt.Sprintf("v%d-%d", round, i)
					if err := tree.Put([]byte(k), []byte(v)); err != nil {
						return err
					}
					model[k] = v
				}
			}
			root = tree.Root()
			return nil
		}); err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
	}

	if err := s.View(func(tx *Tx) error {
		tree := tx.Tree(root)
		for k, want := range model {
			got, err := tree.Get([]byte(k))
			if err != nil {
				return err
			}
			if string(got) != want {
				return fmt.Errorf("key %q: got %q want %q", k, got, want)
			}
		}

		keys := make([]string, 0, len(model))
		for k := range model {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		c := tree.Cursor()
		defer c.Close()
		var scanned []string
		for c.First(); c.Valid(); c.Next() {
			scanned = append(scanned, string(c.Key()))
		}
		if c.Err() != nil {
			return c.Err()
		}
		if len(scanned) != len(keys) {
			return fmt.Errorf("cursor returned %d keys, model has %d", len(scanned), len(keys))
		}
		for i := range keys {
			if keys[i] != scanned[i] {
				return fmt.Errorf("position %d: cursor %q, model %q", i, scanned[i], keys[i])
			}
		}

		var reverse []string
		for c.Last(); c.Valid(); c.Prev() {
			reverse = append(reverse, string(c.Key()))
		}
		if len(reverse) != len(keys) {
			return fmt.Errorf("reverse scan returned %d keys", len(reverse))
		}
		for i := range keys {
			if reverse[len(reverse)-1-i] != keys[i] {
				return fmt.Errorf("reverse position %d mismatch", i)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestCursorSeek(t *testing.T) {
	s := openTemp(t, Options{})
	var root uint64
	if err := s.Update(func(tx *Tx) error {
		tree, err := tx.CreateTree()
		if err != nil {
			return err
		}
		for i := 0; i < 1000; i += 10 {
			if err := tree.Put(key(i), val(i)); err != nil {
				return err
			}
		}
		root = tree.Root()
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.View(func(tx *Tx) error {
		c := tx.Tree(root).Cursor()
		defer c.Close()

		c.Seek(key(105))
		if !c.Valid() || !bytes.Equal(c.Key(), key(110)) {
			return fmt.Errorf("seek forward landed on %q", c.Key())
		}
		c.SeekReverse(key(105))
		if !c.Valid() || !bytes.Equal(c.Key(), key(100)) {
			return fmt.Errorf("seek backward landed on %q", c.Key())
		}
		c.Seek(key(2000))
		if c.Valid() {
			return fmt.Errorf("seek past the end should be invalid, got %q", c.Key())
		}
		c.SeekReverse([]byte("a"))
		if c.Valid() {
			return fmt.Errorf("reverse seek before the start should be invalid")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestOverflowValues(t *testing.T) {
	s := openTemp(t, Options{})
	big := bytes.Repeat([]byte("abcdefghij"), 4000) // 40 KB, many overflow pages
	var root uint64

	if err := s.Update(func(tx *Tx) error {
		tree, err := tx.CreateTree()
		if err != nil {
			return err
		}
		for i := 0; i < 20; i++ {
			if err := tree.Put(key(i), append(big, byte(i))); err != nil {
				return err
			}
		}
		root = tree.Root()
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.View(func(tx *Tx) error {
		tree := tx.Tree(root)
		for i := 0; i < 20; i++ {
			got, err := tree.Get(key(i))
			if err != nil {
				return err
			}
			if !bytes.Equal(got, append(big, byte(i))) {
				return fmt.Errorf("key %d: overflow value round trip failed (%d bytes)", i, len(got))
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Overwriting and deleting must release the chains back to the free list.
	if err := s.Update(func(tx *Tx) error {
		tree := tx.Tree(root)
		for i := 0; i < 20; i++ {
			if err := tree.Put(key(i), []byte("small")); err != nil {
				return err
			}
		}
		root = tree.Root()
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	before := s.meta.numPages
	if err := s.Update(func(tx *Tx) error {
		tree := tx.Tree(root)
		for i := 100; i < 120; i++ {
			if err := tree.Put(key(i), append(big, byte(i))); err != nil {
				return err
			}
		}
		root = tree.Root()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if s.meta.numPages > before {
		t.Fatalf("free list was not reused: %d pages before, %d after", before, s.meta.numPages)
	}
}

func TestRollbackDiscardsChanges(t *testing.T) {
	s := openTemp(t, Options{})
	var root uint64
	if err := s.Update(func(tx *Tx) error {
		tree, err := tx.CreateTree()
		if err != nil {
			return err
		}
		if err := tree.Put([]byte("a"), []byte("1")); err != nil {
			return err
		}
		root = tree.Root()
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	tx, err := s.Begin(true)
	if err != nil {
		t.Fatal(err)
	}
	tree := tx.Tree(root)
	for i := 0; i < 2000; i++ {
		if err := tree.Put(key(i), val(i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}

	if err := s.View(func(tx *Tx) error {
		tree := tx.Tree(root)
		got, err := tree.Get([]byte("a"))
		if err != nil {
			return err
		}
		if string(got) != "1" {
			return fmt.Errorf("committed value lost after rollback: %q", got)
		}
		leaked, err := tree.Get(key(10))
		if err != nil {
			return err
		}
		if leaked != nil {
			return fmt.Errorf("rolled back value is still readable: %q", leaked)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestRecoveryAfterCrash drops the store without closing it, which leaves the
// data file behind without a checkpoint. Reopening must replay the log.
func TestRecoveryAfterCrash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crash.db")

	s, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var root uint64
	if err := s.Update(func(tx *Tx) error {
		tree, err := tx.CreateTree()
		if err != nil {
			return err
		}
		for i := 0; i < 3000; i++ {
			if err := tree.Put(key(i), val(i)); err != nil {
				return err
			}
		}
		root = tree.Root()
		tx.SetCatalogRoot(root)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// A transaction that never commits must leave no trace.
	tx, err := s.Begin(true)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Tree(root).Put([]byte("uncommitted"), []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(logPath(path))
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() == 0 {
		t.Fatal("log is empty, the test would not exercise recovery")
	}

	reopened, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()

	if err := reopened.View(func(tx *Tx) error {
		if tx.CatalogRoot() != root {
			return fmt.Errorf("catalog root not recovered: got %d want %d", tx.CatalogRoot(), root)
		}
		tree := tx.Tree(tx.CatalogRoot())
		for i := 0; i < 3000; i++ {
			got, err := tree.Get(key(i))
			if err != nil {
				return err
			}
			if !bytes.Equal(got, val(i)) {
				return fmt.Errorf("key %d lost after recovery", i)
			}
		}
		leaked, err := tree.Get([]byte("uncommitted"))
		if err != nil {
			return err
		}
		if leaked != nil {
			return fmt.Errorf("uncommitted write survived recovery")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDropReleasesPages(t *testing.T) {
	s := openTemp(t, Options{})
	var root uint64
	if err := s.Update(func(tx *Tx) error {
		tree, err := tx.CreateTree()
		if err != nil {
			return err
		}
		for i := 0; i < 4000; i++ {
			if err := tree.Put(key(i), val(i)); err != nil {
				return err
			}
		}
		root = tree.Root()
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	grown := s.meta.numPages
	if err := s.Update(func(tx *Tx) error { return tx.Tree(root).Drop() }); err != nil {
		t.Fatal(err)
	}
	if err := s.Update(func(tx *Tx) error {
		tree, err := tx.CreateTree()
		if err != nil {
			return err
		}
		for i := 0; i < 4000; i++ {
			if err := tree.Put(key(i), val(i)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if s.meta.numPages > grown {
		t.Fatalf("dropped pages were not reused: %d before, %d after", grown, s.meta.numPages)
	}
}

func TestKeyTooLarge(t *testing.T) {
	s := openTemp(t, Options{})
	err := s.Update(func(tx *Tx) error {
		tree, err := tx.CreateTree()
		if err != nil {
			return err
		}
		return tree.Put(bytes.Repeat([]byte("k"), MaxKeySize+1), []byte("v"))
	})
	if err == nil {
		t.Fatal("expected an error for an oversized key")
	}
}

func BenchmarkSequentialInsert(b *testing.B) {
	path := filepath.Join(b.TempDir(), "bench.db")
	s, err := Open(path, Options{NoSync: true, CacheSize: 4096})
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()

	var root uint64
	if err := s.Update(func(tx *Tx) error {
		tree, err := tx.CreateTree()
		if err != nil {
			return err
		}
		root = tree.Root()
		return nil
	}); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	if err := s.Update(func(tx *Tx) error {
		tree := tx.Tree(root)
		for i := 0; i < b.N; i++ {
			if err := tree.Put(key(i), val(i)); err != nil {
				return err
			}
		}
		root = tree.Root()
		return nil
	}); err != nil {
		b.Fatal(err)
	}
}

func TestCheckpointEmptiesTheLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkpoint.db")
	s, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	var root uint64
	if err := s.Update(func(tx *Tx) error {
		tree, err := tx.CreateTree()
		if err != nil {
			return err
		}
		for i := 0; i < 500; i++ {
			if err := tree.Put(key(i), val(i)); err != nil {
				return err
			}
		}
		root = tree.Root()
		tx.SetCatalogRoot(root)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(logPath(path))
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() == 0 {
		t.Fatal("the log should hold the committed pages before a checkpoint")
	}

	if err := s.Checkpoint(); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	if info, err = os.Stat(logPath(path)); err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Fatalf("the log still holds %d bytes after the checkpoint", info.Size())
	}

	// After a checkpoint the data file alone has to be enough.
	reopened, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.View(func(tx *Tx) error {
		value, err := tx.Tree(tx.CatalogRoot()).Get(key(250))
		if err != nil {
			return err
		}
		if !bytes.Equal(value, val(250)) {
			return fmt.Errorf("value lost after the checkpoint")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestReadOnlyRefusesAPendingLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "readonly.db")
	s, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Update(func(tx *Tx) error {
		tree, err := tx.CreateTree()
		if err != nil {
			return err
		}
		tx.SetCatalogRoot(tree.Root())
		return tree.Put(key(1), val(1))
	}); err != nil {
		t.Fatal(err)
	}

	// The log still holds the commit, so a read only open has to refuse.
	if _, err := Open(path, Options{ReadOnly: true}); err == nil {
		t.Fatal("a read only open should refuse a database that needs recovery")
	}

	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	readOnly, err := Open(path, Options{ReadOnly: true})
	if err != nil {
		t.Fatalf("read only open after a clean close: %v", err)
	}
	defer readOnly.Close()

	if err := readOnly.View(func(tx *Tx) error {
		value, err := tx.Tree(tx.CatalogRoot()).Get(key(1))
		if err != nil {
			return err
		}
		if !bytes.Equal(value, val(1)) {
			return fmt.Errorf("unexpected value %q", value)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := readOnly.Begin(true); err == nil {
		t.Fatal("a write transaction should be refused on a read only store")
	}
}
