package storage

// Cursor walks a tree in key order. It keeps one leaf pinned in the buffer
// pool, so every cursor must be closed.
//
// A cursor is only valid inside the transaction that created it, and the tree
// must not be modified while the cursor is open.
type Cursor struct {
	tree  *Tree
	page  *Page
	index int
	err   error
}

// Cursor returns a cursor positioned before the first entry.
func (t *Tree) Cursor() *Cursor { return &Cursor{tree: t} }

// Close releases the leaf held by the cursor.
func (c *Cursor) Close() {
	c.release()
	c.index = -1
}

// Err reports the first error hit while moving the cursor.
func (c *Cursor) Err() error { return c.err }

// Valid reports whether the cursor points at an entry.
func (c *Cursor) Valid() bool {
	return c.err == nil && c.page != nil && c.index >= 0 && c.index < node(c.page.Data).numKeys()
}

// Key returns a copy of the current key.
func (c *Cursor) Key() []byte {
	if !c.Valid() {
		return nil
	}
	return append([]byte(nil), node(c.page.Data).key(c.index)...)
}

// Value returns a copy of the current value.
func (c *Cursor) Value() ([]byte, error) {
	if !c.Valid() {
		return nil, c.err
	}
	return c.tree.readValue(node(c.page.Data), c.index)
}

// First moves to the smallest key.
func (c *Cursor) First() {
	if c.descend(nil, dirLeftmost) {
		c.index = 0
		c.settleForward()
	}
}

// Last moves to the largest key.
func (c *Cursor) Last() {
	if c.descend(nil, dirRightmost) {
		c.index = node(c.page.Data).numKeys() - 1
		c.settleBackward()
	}
}

// Seek moves to the smallest key greater than or equal to the given key.
func (c *Cursor) Seek(key []byte) {
	if c.descend(key, dirSearch) {
		c.index, _ = node(c.page.Data).searchLeaf(key)
		c.settleForward()
	}
}

// SeekReverse moves to the largest key less than or equal to the given key.
func (c *Cursor) SeekReverse(key []byte) {
	if !c.descend(key, dirSearch) {
		return
	}
	index, found := node(c.page.Data).searchLeaf(key)
	if !found {
		index--
	}
	c.index = index
	c.settleBackward()
}

// Next advances to the following key.
func (c *Cursor) Next() {
	if c.page == nil || c.err != nil {
		return
	}
	c.index++
	c.settleForward()
}

// Prev steps back to the preceding key.
func (c *Cursor) Prev() {
	if c.page == nil || c.err != nil {
		return
	}
	c.index--
	c.settleBackward()
}

const (
	dirSearch = iota
	dirLeftmost
	dirRightmost
)

func (c *Cursor) descend(key []byte, mode int) bool {
	c.release()
	c.err = nil

	id := c.tree.root
	for {
		p, err := c.tree.tx.get(id)
		if err != nil {
			c.err = err
			return false
		}
		n := node(p.Data)
		if n.isLeaf() {
			c.page = p
			return true
		}
		var index int
		switch mode {
		case dirLeftmost:
			index = 0
		case dirRightmost:
			index = n.numKeys()
		default:
			index = n.searchInternal(key)
		}
		child := n.childAt(index)
		c.tree.tx.unpin(p)
		id = child
	}
}

// settleForward skips over exhausted (or empty) leaves to the right.
func (c *Cursor) settleForward() {
	for c.page != nil {
		n := node(c.page.Data)
		if c.index < n.numKeys() {
			return
		}
		next := n.link()
		if next == 0 {
			c.index = n.numKeys()
			return
		}
		if !c.move(next) {
			return
		}
		c.index = 0
	}
}

// settleBackward skips over exhausted (or empty) leaves to the left.
func (c *Cursor) settleBackward() {
	for c.page != nil {
		n := node(c.page.Data)
		if c.index >= 0 && c.index < n.numKeys() {
			return
		}
		if c.index >= n.numKeys() {
			c.index = n.numKeys() - 1
			if c.index >= 0 {
				return
			}
		}
		prev := n.prev()
		if prev == 0 {
			c.index = -1
			return
		}
		if !c.move(prev) {
			return
		}
		c.index = node(c.page.Data).numKeys() - 1
	}
}

func (c *Cursor) move(id uint64) bool {
	p, err := c.tree.tx.get(id)
	if err != nil {
		c.err = err
		c.release()
		return false
	}
	c.release()
	c.page = p
	return true
}

func (c *Cursor) release() {
	if c.page != nil {
		c.tree.tx.unpin(c.page)
		c.page = nil
	}
}
