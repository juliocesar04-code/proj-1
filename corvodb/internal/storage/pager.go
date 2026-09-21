package storage

import (
	"container/list"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// Errors returned by the storage layer.
var (
	ErrKeyTooLarge  = errors.New("storage: key exceeds maximum size")
	ErrTxDone       = errors.New("storage: transaction already finished")
	ErrTxReadOnly   = errors.New("storage: transaction is read only")
	ErrClosed       = errors.New("storage: store is closed")
	ErrCorruptFile  = errors.New("storage: database file is corrupt")
	ErrPageOverflow = errors.New("storage: value does not fit in a page")
)

const (
	metaMagic   = "CORVODB\x01"
	metaSize    = 48
	metaPageID  = 0
	defaultTail = 4 << 20 // checkpoint once the log passes this size
)

// Options tunes a store at open time. The zero value is usable.
type Options struct {
	// CacheSize is the number of pages kept in the buffer pool.
	CacheSize int
	// NoSync skips the fsync that makes a commit durable. Only useful for
	// throwaway databases and benchmarks.
	NoSync bool
	// CheckpointBytes is the log size that triggers a checkpoint after a
	// commit.
	CheckpointBytes int64
	// ReadOnly opens the database without allowing write transactions.
	ReadOnly bool
}

func (o Options) withDefaults() Options {
	if o.CacheSize <= 0 {
		o.CacheSize = 1024
	}
	if o.CheckpointBytes <= 0 {
		o.CheckpointBytes = defaultTail
	}
	return o
}

// Page is a pinned buffer pool frame.
type Page struct {
	ID    uint64
	Data  []byte
	dirty bool
	// locked marks a page written by a transaction that has not committed
	// yet. Such a page must never reach the data file (no-steal policy),
	// which is what lets the log get away with redo records only.
	locked bool
	pins   int
	elem   *list.Element
}

type meta struct {
	numPages    uint64
	freeHead    uint64
	catalogRoot uint64
	txnID       uint64
}

func (m meta) encode(dst []byte) {
	clear(dst[:metaSize])
	copy(dst, metaMagic)
	binary.LittleEndian.PutUint32(dst[8:], PageSize)
	binary.LittleEndian.PutUint64(dst[12:], m.numPages)
	binary.LittleEndian.PutUint64(dst[20:], m.freeHead)
	binary.LittleEndian.PutUint64(dst[28:], m.catalogRoot)
	binary.LittleEndian.PutUint64(dst[36:], m.txnID)
	binary.LittleEndian.PutUint32(dst[44:], crc32.ChecksumIEEE(dst[:44]))
}

func decodeMeta(src []byte) (meta, error) {
	var m meta
	if string(src[:8]) != metaMagic {
		return m, fmt.Errorf("%w: bad magic", ErrCorruptFile)
	}
	if binary.LittleEndian.Uint32(src[8:]) != PageSize {
		return m, fmt.Errorf("%w: unexpected page size", ErrCorruptFile)
	}
	if binary.LittleEndian.Uint32(src[44:]) != crc32.ChecksumIEEE(src[:44]) {
		return m, fmt.Errorf("%w: metadata checksum mismatch", ErrCorruptFile)
	}
	m.numPages = binary.LittleEndian.Uint64(src[12:])
	m.freeHead = binary.LittleEndian.Uint64(src[20:])
	m.catalogRoot = binary.LittleEndian.Uint64(src[28:])
	m.txnID = binary.LittleEndian.Uint64(src[36:])
	return m, nil
}

// Store is a transactional page store: a buffer pool over a paged file, a
// write-ahead log for durability, and B+Trees built on top of both.
//
// Concurrency follows the single writer, many readers model. Write
// transactions are serialised; read transactions run in parallel with each
// other and see the last committed state.
type Store struct {
	opts Options
	path string

	txmu sync.RWMutex // held for the lifetime of a transaction
	mu   sync.Mutex   // guards the buffer pool and the data file

	file  *os.File
	log   *wal
	cache map[uint64]*Page
	lru   *list.List
	meta  meta

	closed bool
}

// Open opens or creates a database at path, replaying the log if the previous
// process stopped before a checkpoint.
func Open(path string, opts Options) (*Store, error) {
	opts = opts.withDefaults()
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}

	flags := os.O_RDWR | os.O_CREATE
	if opts.ReadOnly {
		flags = os.O_RDONLY
	}
	file, err := os.OpenFile(path, flags, 0o644)
	if err != nil {
		return nil, err
	}

	s := &Store{
		opts:  opts,
		path:  path,
		file:  file,
		cache: make(map[uint64]*Page),
		lru:   list.New(),
	}

	if opts.ReadOnly {
		// Recovery writes, so a read only open cannot perform it. Refusing is
		// better than silently serving the state from before the crash.
		if info, err := os.Stat(logPath(path)); err == nil && info.Size() > 0 {
			file.Close()
			return nil, fmt.Errorf("%s needs recovery: open it for writing once before opening it read only", path)
		}
	} else if err := replayLog(logPath(path), file); err != nil {
		file.Close()
		return nil, err
	}

	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}

	if info.Size() == 0 {
		s.meta = meta{numPages: 1}
		if err := s.writeMetaPage(); err != nil {
			file.Close()
			return nil, err
		}
		if err := file.Sync(); err != nil {
			file.Close()
			return nil, err
		}
	} else {
		buf := make([]byte, PageSize)
		if _, err := file.ReadAt(buf, 0); err != nil {
			file.Close()
			return nil, err
		}
		m, err := decodeMeta(buf)
		if err != nil {
			file.Close()
			return nil, err
		}
		s.meta = m
	}

	if !opts.ReadOnly {
		s.log, err = openLog(logPath(path), !opts.NoSync)
		if err != nil {
			file.Close()
			return nil, err
		}
	}
	return s, nil
}

func logPath(path string) string { return path + "-log" }

// Path is the file backing the store.
func (s *Store) Path() string { return s.path }

func (s *Store) writeMetaPage() error {
	buf := make([]byte, PageSize)
	s.meta.encode(buf)
	_, err := s.file.WriteAt(buf, 0)
	return err
}

// Close checkpoints and releases the store.
func (s *Store) Close() error {
	s.txmu.Lock()
	defer s.txmu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}
	s.closed = true

	var firstErr error
	if !s.opts.ReadOnly {
		if err := s.checkpointLocked(); err != nil {
			firstErr = err
		}
		if err := s.log.close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if err := s.file.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// --- buffer pool -----------------------------------------------------------

func (s *Store) fetch(id uint64) (*Page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if p, ok := s.cache[id]; ok {
		s.lru.MoveToFront(p.elem)
		p.pins++
		return p, nil
	}

	data := make([]byte, PageSize)
	if _, err := s.file.ReadAt(data, int64(id)*PageSize); err != nil {
		// A page allocated by the current transaction may not exist in the
		// file yet; a short read simply means it is still all zeroes.
		if !isShortRead(err) {
			return nil, err
		}
	}

	p := &Page{ID: id, Data: data, pins: 1}
	p.elem = s.lru.PushFront(p)
	s.cache[id] = p
	s.evictLocked()
	return p, nil
}

func (s *Store) newPage(id uint64) *Page {
	s.mu.Lock()
	defer s.mu.Unlock()

	if p, ok := s.cache[id]; ok {
		clear(p.Data)
		s.lru.MoveToFront(p.elem)
		p.pins++
		return p
	}
	p := &Page{ID: id, Data: make([]byte, PageSize), pins: 1}
	p.elem = s.lru.PushFront(p)
	s.cache[id] = p
	s.evictLocked()
	return p
}

func (s *Store) unpin(p *Page) {
	s.mu.Lock()
	if p.pins > 0 {
		p.pins--
	}
	s.mu.Unlock()
}

func (s *Store) evictLocked() {
	for s.lru.Len() > s.opts.CacheSize {
		victim := s.lru.Back()
		for victim != nil {
			p := victim.Value.(*Page)
			if p.pins == 0 && !p.locked {
				break
			}
			victim = victim.Prev()
		}
		if victim == nil {
			return // everything is in use; the pool grows for now
		}
		p := victim.Value.(*Page)
		if p.dirty {
			if _, err := s.file.WriteAt(p.Data, int64(p.ID)*PageSize); err != nil {
				return
			}
			p.dirty = false
		}
		s.lru.Remove(victim)
		delete(s.cache, p.ID)
	}
}

func (s *Store) checkpointLocked() error {
	for _, p := range s.cache {
		if !p.dirty {
			continue
		}
		if _, err := s.file.WriteAt(p.Data, int64(p.ID)*PageSize); err != nil {
			return err
		}
		p.dirty = false
	}
	if err := s.file.Sync(); err != nil {
		return err
	}
	return s.log.reset()
}

// Checkpoint flushes every dirty page to the data file and empties the log.
func (s *Store) Checkpoint() error {
	s.txmu.Lock()
	defer s.txmu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if s.opts.ReadOnly {
		return nil
	}
	return s.checkpointLocked()
}

// --- transactions ----------------------------------------------------------

// Tx is a transaction over the store.
type Tx struct {
	s        *Store
	writable bool
	done     bool
	id       uint64
	dirty    map[uint64]*Page
	undo     map[uint64]undoImage
	saved    meta
}

// undoImage is the content of a page before the transaction touched it. Since
// commits are no-force, a page in the buffer pool may hold committed data that
// never reached the data file, so a rollback has to put the old bytes back
// rather than simply dropping the frame.
type undoImage struct {
	data  []byte
	dirty bool
}

// Begin starts a transaction. Only one write transaction runs at a time.
func (s *Store) Begin(writable bool) (*Tx, error) {
	if writable {
		if s.opts.ReadOnly {
			return nil, ErrTxReadOnly
		}
		s.txmu.Lock()
	} else {
		s.txmu.RLock()
	}
	if s.closed {
		s.release(writable)
		return nil, ErrClosed
	}

	tx := &Tx{s: s, writable: writable, saved: s.meta}
	if writable {
		s.meta.txnID++
		tx.id = s.meta.txnID
		tx.dirty = make(map[uint64]*Page)
		tx.undo = make(map[uint64]undoImage)
	}
	return tx, nil
}

func (s *Store) release(writable bool) {
	if writable {
		s.txmu.Unlock()
	} else {
		s.txmu.RUnlock()
	}
}

// Update runs fn inside a write transaction, committing on success and rolling
// back when fn returns an error or panics.
func (s *Store) Update(fn func(*Tx) error) error {
	tx, err := s.Begin(true)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// View runs fn inside a read transaction.
func (s *Store) View(fn func(*Tx) error) error {
	tx, err := s.Begin(false)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return fn(tx)
}

// Writable reports whether the transaction may modify the database.
func (tx *Tx) Writable() bool { return tx.writable }

// CatalogRoot is the root page of the tree holding the schema. It is zero on a
// fresh database.
func (tx *Tx) CatalogRoot() uint64 { return tx.s.meta.catalogRoot }

// SetCatalogRoot records the root page of the schema tree.
func (tx *Tx) SetCatalogRoot(root uint64) { tx.s.meta.catalogRoot = root }

func (tx *Tx) get(id uint64) (*Page, error) {
	if tx.done {
		return nil, ErrTxDone
	}
	return tx.s.fetch(id)
}

func (tx *Tx) unpin(p *Page) { tx.s.unpin(p) }

// markDirty must be called before a page is modified: it captures the undo
// image the rollback path needs.
func (tx *Tx) markDirty(p *Page) {
	tx.s.mu.Lock()
	if _, seen := tx.dirty[p.ID]; !seen {
		tx.undo[p.ID] = undoImage{data: append([]byte(nil), p.Data...), dirty: p.dirty}
		tx.dirty[p.ID] = p
	}
	p.dirty = true
	p.locked = true
	tx.s.mu.Unlock()
}

func (tx *Tx) allocate() (*Page, error) {
	if !tx.writable {
		return nil, ErrTxReadOnly
	}
	s := tx.s
	if head := s.meta.freeHead; head != 0 {
		p, err := tx.get(head)
		if err != nil {
			return nil, err
		}
		s.meta.freeHead = binary.LittleEndian.Uint64(p.Data)
		tx.markDirty(p)
		clear(p.Data)
		return p, nil
	}
	id := s.meta.numPages
	s.meta.numPages++
	p := s.newPage(id)
	tx.markDirty(p)
	return p, nil
}

func (tx *Tx) freePage(id uint64) error {
	if !tx.writable {
		return ErrTxReadOnly
	}
	p, err := tx.get(id)
	if err != nil {
		return err
	}
	defer tx.unpin(p)
	tx.markDirty(p)
	clear(p.Data)
	binary.LittleEndian.PutUint64(p.Data, tx.s.meta.freeHead)
	tx.s.meta.freeHead = id
	return nil
}

// Commit makes every change of the transaction durable.
func (tx *Tx) Commit() error {
	if tx.done {
		return ErrTxDone
	}
	if !tx.writable {
		tx.finish()
		return nil
	}

	s := tx.s
	metaPage, err := s.fetch(metaPageID)
	if err != nil {
		tx.rollbackLocked()
		return err
	}
	tx.markDirty(metaPage)
	s.meta.encode(metaPage.Data)
	s.unpin(metaPage)

	s.mu.Lock()
	pages := make([]*Page, 0, len(tx.dirty))
	for _, p := range tx.dirty {
		pages = append(pages, p)
	}
	s.mu.Unlock()

	for _, p := range pages {
		if err := s.log.appendPage(tx.id, p.ID, p.Data); err != nil {
			tx.rollbackLocked()
			return err
		}
	}
	if err := s.log.appendCommit(tx.id); err != nil {
		tx.rollbackLocked()
		return err
	}

	s.mu.Lock()
	for _, p := range pages {
		p.locked = false
	}
	needCheckpoint := s.log.size() >= s.opts.CheckpointBytes
	if needCheckpoint {
		if err := s.checkpointLocked(); err != nil {
			s.mu.Unlock()
			tx.finish()
			return err
		}
	}
	s.mu.Unlock()

	tx.finish()
	return nil
}

// Rollback discards every change of the transaction. Calling it after Commit
// is a no-op, which makes `defer tx.Rollback()` safe.
func (tx *Tx) Rollback() error {
	if tx.done {
		return nil
	}
	if !tx.writable {
		tx.finish()
		return nil
	}
	tx.rollbackLocked()
	return nil
}

func (tx *Tx) rollbackLocked() {
	s := tx.s
	s.mu.Lock()
	for id, p := range tx.dirty {
		p.locked = false
		if id >= tx.saved.numPages {
			// The page did not exist before this transaction.
			if p.elem != nil {
				s.lru.Remove(p.elem)
			}
			delete(s.cache, id)
			continue
		}
		image := tx.undo[id]
		copy(p.Data, image.data)
		p.dirty = image.dirty
	}
	s.meta = tx.saved
	s.mu.Unlock()
	tx.finish()
}

func (tx *Tx) finish() {
	tx.done = true
	tx.dirty = nil
	tx.undo = nil
	tx.s.release(tx.writable)
}

// isShortRead reports whether a read stopped at the end of the file, which
// happens for pages that were allocated but never written back yet.
func isShortRead(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}
