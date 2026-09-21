package storage

import (
	"encoding/binary"
	"fmt"
)

// Tree is a B+Tree stored in the pages of a Store. Keys are ordered by their
// byte representation, so callers get range scans for free as long as they
// encode keys in an order preserving way.
type Tree struct {
	tx   *Tx
	root uint64
}

// CreateTree allocates an empty tree and returns it.
func (tx *Tx) CreateTree() (*Tree, error) {
	p, err := tx.allocate()
	if err != nil {
		return nil, err
	}
	defer tx.unpin(p)
	node(p.Data).init(pageLeaf)
	return &Tree{tx: tx, root: p.ID}, nil
}

// Tree opens the tree rooted at the given page.
func (tx *Tx) Tree(root uint64) *Tree { return &Tree{tx: tx, root: root} }

// Root is the current root page. Splits move the root, so a caller that
// persists the root must read it back after every write.
func (t *Tree) Root() uint64 { return t.root }

// Get returns the value stored under key, or nil when the key is absent.
func (t *Tree) Get(key []byte) ([]byte, error) {
	id := t.root
	for {
		p, err := t.tx.get(id)
		if err != nil {
			return nil, err
		}
		n := node(p.Data)
		if n.isLeaf() {
			i, found := n.searchLeaf(key)
			if !found {
				t.tx.unpin(p)
				return nil, nil
			}
			val, err := t.readValue(n, i)
			t.tx.unpin(p)
			return val, err
		}
		child := n.childAt(n.searchInternal(key))
		t.tx.unpin(p)
		id = child
	}
}

// Has reports whether key is present.
func (t *Tree) Has(key []byte) (bool, error) {
	id := t.root
	for {
		p, err := t.tx.get(id)
		if err != nil {
			return false, err
		}
		n := node(p.Data)
		if n.isLeaf() {
			_, found := n.searchLeaf(key)
			t.tx.unpin(p)
			return found, nil
		}
		child := n.childAt(n.searchInternal(key))
		t.tx.unpin(p)
		id = child
	}
}

func (t *Tree) readValue(n node, i int) ([]byte, error) {
	if !n.hasOverflow(i) {
		return append([]byte(nil), n.inlineValue(i)...), nil
	}
	first, total := n.overflowRef(i)
	return t.tx.readOverflow(first, total)
}

// Put inserts or replaces the value stored under key.
func (t *Tree) Put(key, val []byte) error {
	if !t.tx.writable {
		return ErrTxReadOnly
	}
	if len(key) == 0 || len(key) > MaxKeySize {
		return fmt.Errorf("%w: %d bytes", ErrKeyTooLarge, len(key))
	}

	sep, right, split, err := t.insert(t.root, key, val)
	if err != nil || !split {
		return err
	}

	p, err := t.tx.allocate()
	if err != nil {
		return err
	}
	defer t.tx.unpin(p)
	n := node(p.Data)
	n.init(pageInternal)
	n.setLink(right)
	n.insertCell(0, makeInternalCell(sep, t.root))
	t.root = p.ID
	return nil
}

func (t *Tree) insert(id uint64, key, val []byte) (sep []byte, right uint64, split bool, err error) {
	p, err := t.tx.get(id)
	if err != nil {
		return nil, 0, false, err
	}
	defer t.tx.unpin(p)
	n := node(p.Data)

	if n.isLeaf() {
		cell, err := t.tx.makeLeafCell(key, val)
		if err != nil {
			return nil, 0, false, err
		}
		pos, found := n.searchLeaf(key)
		t.tx.markDirty(p)
		if found {
			if n.hasOverflow(pos) {
				first, _ := n.overflowRef(pos)
				if err := t.tx.freeOverflow(first); err != nil {
					return nil, 0, false, err
				}
			}
			n.removeCell(pos)
		}
		if n.insertCell(pos, cell) {
			return nil, 0, false, nil
		}
		if n.totalFreeSpace() >= len(cell)+slotSize {
			n.compact(make([]byte, PageSize))
			n.insertCell(pos, cell)
			return nil, 0, false, nil
		}
		sep, right, err := t.splitLeaf(p, pos, cell)
		return sep, right, true, err
	}

	pos := n.searchInternal(key)
	childSep, childRight, childSplit, err := t.insert(n.childAt(pos), key, val)
	if err != nil || !childSplit {
		return nil, 0, false, err
	}

	cell := makeInternalCell(childSep, n.childAt(pos))
	t.tx.markDirty(p)
	if n.insertCell(pos, cell) {
		n.setChildAt(pos+1, childRight)
		return nil, 0, false, nil
	}
	if n.totalFreeSpace() >= len(cell)+slotSize {
		n.compact(make([]byte, PageSize))
		n.insertCell(pos, cell)
		n.setChildAt(pos+1, childRight)
		return nil, 0, false, nil
	}
	sep, right, err = t.splitInternal(p, pos, cell, childRight)
	return sep, right, true, err
}

func (t *Tree) splitLeaf(p *Page, pos int, cell []byte) ([]byte, uint64, error) {
	scratch := make([]byte, PageSize)
	copy(scratch, p.Data)
	old := node(scratch)

	cells := make([][]byte, 0, old.numKeys()+1)
	for i, num := 0, old.numKeys(); i < num; i++ {
		cells = append(cells, old.cell(i))
	}
	cells = append(cells, nil)
	copy(cells[pos+1:], cells[pos:])
	cells[pos] = cell

	mid := splitPoint(cells, false)
	if mid < 0 {
		return nil, 0, ErrPageOverflow
	}

	rightPage, err := t.tx.allocate()
	if err != nil {
		return nil, 0, err
	}
	defer t.tx.unpin(rightPage)

	oldPrev, oldNext := old.prev(), old.link()
	left := node(p.Data)
	left.init(pageLeaf)
	left.setPrev(oldPrev)
	left.setLink(rightPage.ID)
	for _, c := range cells[:mid] {
		left.appendCell(c)
	}

	right := node(rightPage.Data)
	right.init(pageLeaf)
	right.setPrev(p.ID)
	right.setLink(oldNext)
	for _, c := range cells[mid:] {
		right.appendCell(c)
	}

	if oldNext != 0 {
		np, err := t.tx.get(oldNext)
		if err != nil {
			return nil, 0, err
		}
		t.tx.markDirty(np)
		node(np.Data).setPrev(rightPage.ID)
		t.tx.unpin(np)
	}

	return append([]byte(nil), right.key(0)...), rightPage.ID, nil
}

func (t *Tree) splitInternal(p *Page, pos int, cell []byte, childRight uint64) ([]byte, uint64, error) {
	scratch := make([]byte, PageSize)
	copy(scratch, p.Data)
	old := node(scratch)

	cells := make([][]byte, 0, old.numKeys()+1)
	for i, num := 0, old.numKeys(); i < num; i++ {
		cells = append(cells, old.cell(i))
	}
	cells = append(cells, nil)
	copy(cells[pos+1:], cells[pos:])
	cells[pos] = cell

	rightmost := old.link()
	if pos+1 < len(cells) {
		binary.LittleEndian.PutUint64(cells[pos+1][2:], childRight)
	} else {
		rightmost = childRight
	}

	mid := splitPoint(cells, true)
	if mid < 0 {
		return nil, 0, ErrPageOverflow
	}
	promoted := cells[mid]
	sep := append([]byte(nil), promoted[internalCellHeader:]...)

	rightPage, err := t.tx.allocate()
	if err != nil {
		return nil, 0, err
	}
	defer t.tx.unpin(rightPage)

	left := node(p.Data)
	left.init(pageInternal)
	for _, c := range cells[:mid] {
		left.appendCell(c)
	}
	left.setLink(binary.LittleEndian.Uint64(promoted[2:]))

	right := node(rightPage.Data)
	right.init(pageInternal)
	for _, c := range cells[mid+1:] {
		right.appendCell(c)
	}
	right.setLink(rightmost)

	return sep, rightPage.ID, nil
}

// Delete removes key and reports whether it was present.
func (t *Tree) Delete(key []byte) (bool, error) {
	if !t.tx.writable {
		return false, ErrTxReadOnly
	}
	removed, empty, err := t.deleteFrom(t.root, key)
	if err != nil || !removed {
		return removed, err
	}

	if empty {
		p, err := t.tx.get(t.root)
		if err != nil {
			return true, err
		}
		t.tx.markDirty(p)
		node(p.Data).init(pageLeaf)
		t.tx.unpin(p)
		return true, nil
	}

	// A root that lost every separator has a single child; make that child
	// the new root so the tree does not keep growing taller than it needs.
	for {
		p, err := t.tx.get(t.root)
		if err != nil {
			return true, err
		}
		n := node(p.Data)
		if n.isLeaf() || n.numKeys() != 0 || n.link() == 0 {
			t.tx.unpin(p)
			return true, nil
		}
		child := n.link()
		t.tx.unpin(p)
		if err := t.tx.freePage(t.root); err != nil {
			return true, err
		}
		t.root = child

		cp, err := t.tx.get(child)
		if err != nil {
			return true, err
		}
		if cn := node(cp.Data); cn.isLeaf() {
			t.tx.markDirty(cp)
			cn.setPrev(0)
			cn.setLink(0)
		}
		t.tx.unpin(cp)
	}
}

func (t *Tree) deleteFrom(id uint64, key []byte) (removed, empty bool, err error) {
	p, err := t.tx.get(id)
	if err != nil {
		return false, false, err
	}
	defer t.tx.unpin(p)
	n := node(p.Data)

	if n.isLeaf() {
		pos, found := n.searchLeaf(key)
		if !found {
			return false, false, nil
		}
		if n.hasOverflow(pos) {
			first, _ := n.overflowRef(pos)
			if err := t.tx.freeOverflow(first); err != nil {
				return false, false, err
			}
		}
		t.tx.markDirty(p)
		n.removeCell(pos)
		return true, n.numKeys() == 0, nil
	}

	pos := n.searchInternal(key)
	child := n.childAt(pos)
	removed, childEmpty, err := t.deleteFrom(child, key)
	if err != nil || !removed || !childEmpty {
		return removed, false, err
	}

	cp, err := t.tx.get(child)
	if err != nil {
		return true, false, err
	}
	if cn := node(cp.Data); cn.isLeaf() {
		if err := t.unlinkLeaf(cn); err != nil {
			t.tx.unpin(cp)
			return true, false, err
		}
	}
	t.tx.unpin(cp)

	t.tx.markDirty(p)
	switch {
	case n.numKeys() == 0:
		n.setLink(0)
	case pos >= n.numKeys():
		last := n.numKeys() - 1
		grandchild := n.childAt(last)
		n.removeCell(last)
		n.setLink(grandchild)
	default:
		n.removeCell(pos)
	}
	if err := t.tx.freePage(child); err != nil {
		return true, false, err
	}
	return true, n.numKeys() == 0 && n.link() == 0, nil
}

func (t *Tree) unlinkLeaf(n node) error {
	prev, next := n.prev(), n.link()
	if prev != 0 {
		p, err := t.tx.get(prev)
		if err != nil {
			return err
		}
		t.tx.markDirty(p)
		node(p.Data).setLink(next)
		t.tx.unpin(p)
	}
	if next != 0 {
		p, err := t.tx.get(next)
		if err != nil {
			return err
		}
		t.tx.markDirty(p)
		node(p.Data).setPrev(prev)
		t.tx.unpin(p)
	}
	return nil
}

// Drop releases every page owned by the tree, including overflow chains.
func (t *Tree) Drop() error {
	if !t.tx.writable {
		return ErrTxReadOnly
	}
	return t.dropPage(t.root)
}

func (t *Tree) dropPage(id uint64) error {
	p, err := t.tx.get(id)
	if err != nil {
		return err
	}
	n := node(p.Data)
	children := make([]uint64, 0, 16)
	overflows := make([]uint64, 0, 4)
	if n.isLeaf() {
		for i, num := 0, n.numKeys(); i < num; i++ {
			if n.hasOverflow(i) {
				first, _ := n.overflowRef(i)
				overflows = append(overflows, first)
			}
		}
	} else {
		for i, num := 0, n.numKeys(); i <= num; i++ {
			children = append(children, n.childAt(i))
		}
	}
	t.tx.unpin(p)

	for _, first := range overflows {
		if err := t.tx.freeOverflow(first); err != nil {
			return err
		}
	}
	for _, child := range children {
		if err := t.dropPage(child); err != nil {
			return err
		}
	}
	return t.tx.freePage(id)
}

// --- overflow pages --------------------------------------------------------

func (tx *Tx) makeLeafCell(key, val []byte) ([]byte, error) {
	if len(val) <= maxInlineValue {
		return makeInlineLeafCell(key, val), nil
	}
	first, err := tx.writeOverflow(val)
	if err != nil {
		return nil, err
	}
	return makeOverflowLeafCell(key, len(val), first), nil
}

func (tx *Tx) writeOverflow(val []byte) (uint64, error) {
	var first, prev uint64
	for offset := 0; offset < len(val); offset += overflowCapacity {
		p, err := tx.allocate()
		if err != nil {
			return 0, err
		}
		chunk := val[offset:min(offset+overflowCapacity, len(val))]
		tx.markDirty(p)
		binary.LittleEndian.PutUint32(p.Data[8:], uint32(len(chunk)))
		copy(p.Data[overflowHeaderSize:], chunk)

		if first == 0 {
			first = p.ID
		}
		if prev != 0 {
			pp, err := tx.get(prev)
			if err != nil {
				tx.unpin(p)
				return 0, err
			}
			tx.markDirty(pp)
			binary.LittleEndian.PutUint64(pp.Data, p.ID)
			tx.unpin(pp)
		}
		prev = p.ID
		tx.unpin(p)
	}
	return first, nil
}

func (tx *Tx) readOverflow(first uint64, total int) ([]byte, error) {
	out := make([]byte, 0, total)
	for id := first; id != 0; {
		p, err := tx.get(id)
		if err != nil {
			return nil, err
		}
		length := int(binary.LittleEndian.Uint32(p.Data[8:]))
		if length > overflowCapacity {
			tx.unpin(p)
			return nil, ErrCorruptFile
		}
		out = append(out, p.Data[overflowHeaderSize:overflowHeaderSize+length]...)
		id = binary.LittleEndian.Uint64(p.Data)
		tx.unpin(p)
	}
	if len(out) != total {
		return nil, fmt.Errorf("%w: overflow chain is %d bytes, expected %d", ErrCorruptFile, len(out), total)
	}
	return out, nil
}

func (tx *Tx) freeOverflow(first uint64) error {
	for id := first; id != 0; {
		p, err := tx.get(id)
		if err != nil {
			return err
		}
		next := binary.LittleEndian.Uint64(p.Data)
		tx.unpin(p)
		if err := tx.freePage(id); err != nil {
			return err
		}
		id = next
	}
	return nil
}
