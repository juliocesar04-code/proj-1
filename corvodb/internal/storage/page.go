package storage

import (
	"bytes"
	"encoding/binary"
)

// Page layout
//
// Every page is PageSize bytes. Page 0 holds the metadata record; every other
// page is either a B+Tree node (leaf or internal), an overflow page, or sits
// on the free list.
//
// Node header (24 bytes):
//
//	0      kind      (leaf or internal)
//	1      flags     (reserved)
//	2..3   numKeys
//	4..5   cellsEnd  offset where the cell area starts; cells grow downwards
//	6..7   reserved
//	8..15  link      next leaf for leaves, rightmost child for internal nodes
//	16..23 prev      previous leaf, unused on internal nodes
//
// The slot array follows the header: one 16 bit offset per cell, kept in key
// order. Cell payloads live at the tail of the page, so inserting a cell only
// moves the (small) slot array.
//
// Leaf cell:
//
//	flags(1) keyLen(2) valLen(4) key value            -- inline value
//	flags(1) keyLen(2) totalLen(4) overflowPage(8) key -- spilled value
//
// Internal cell:
//
//	keyLen(2) child(8) key
//
// In an internal node the child stored in cell i holds every key strictly
// smaller than the key of cell i; the link field holds the child for the
// remaining keys.
const (
	// PageSize is the fixed page size used by the file format.
	PageSize = 4096

	pageLeaf     = 1
	pageInternal = 2

	nodeHeaderSize = 24
	slotSize       = 2

	// MaxKeySize bounds key length so that an internal node always fits
	// several separators, which keeps the tree from degenerating.
	MaxKeySize = 512

	// maxInlineValue is the largest value stored directly inside a leaf.
	// Anything longer spills into a chain of overflow pages.
	maxInlineValue = 800

	leafInlineHeader   = 7
	leafOverflowHeader = 15
	internalCellHeader = 10

	cellFlagOverflow = 1 << 0

	overflowHeaderSize = 12
	overflowCapacity   = PageSize - overflowHeaderSize
)

type node []byte

func (n node) isLeaf() bool     { return n[0] == pageLeaf }
func (n node) numKeys() int     { return int(binary.LittleEndian.Uint16(n[2:4])) }
func (n node) setNumKeys(v int) { binary.LittleEndian.PutUint16(n[2:4], uint16(v)) }
func (n node) cellsEnd() int    { return int(binary.LittleEndian.Uint16(n[4:6])) }

func (n node) setCellsEnd(v int) { binary.LittleEndian.PutUint16(n[4:6], uint16(v)) }
func (n node) link() uint64      { return binary.LittleEndian.Uint64(n[8:16]) }
func (n node) setLink(v uint64)  { binary.LittleEndian.PutUint64(n[8:16], v) }
func (n node) prev() uint64      { return binary.LittleEndian.Uint64(n[16:24]) }
func (n node) setPrev(v uint64)  { binary.LittleEndian.PutUint64(n[16:24], v) }

func (n node) init(kind uint8) {
	clear(n[:nodeHeaderSize])
	n[0] = kind
	n.setCellsEnd(len(n))
}

func (n node) slot(i int) int {
	return int(binary.LittleEndian.Uint16(n[nodeHeaderSize+i*slotSize:]))
}

func (n node) setSlot(i, off int) {
	binary.LittleEndian.PutUint16(n[nodeHeaderSize+i*slotSize:], uint16(off))
}

// freeSpace is the contiguous gap between the slot array and the cell area.
func (n node) freeSpace() int {
	return n.cellsEnd() - (nodeHeaderSize + n.numKeys()*slotSize)
}

// totalFreeSpace also counts the bytes left behind by deleted cells, which
// only come back after a compaction.
func (n node) totalFreeSpace() int {
	used := 0
	for i, k := 0, n.numKeys(); i < k; i++ {
		used += n.cellSize(i)
	}
	return len(n) - nodeHeaderSize - n.numKeys()*slotSize - used
}

func (n node) cellSize(i int) int {
	off := n.slot(i)
	if n.isLeaf() {
		keyLen := int(binary.LittleEndian.Uint16(n[off+1:]))
		if n[off]&cellFlagOverflow != 0 {
			return leafOverflowHeader + keyLen
		}
		return leafInlineHeader + keyLen + int(binary.LittleEndian.Uint32(n[off+3:]))
	}
	return internalCellHeader + int(binary.LittleEndian.Uint16(n[off:]))
}

func (n node) cell(i int) []byte {
	off := n.slot(i)
	return n[off : off+n.cellSize(i)]
}

func (n node) key(i int) []byte {
	off := n.slot(i)
	if n.isLeaf() {
		keyLen := int(binary.LittleEndian.Uint16(n[off+1:]))
		start := off + leafInlineHeader
		if n[off]&cellFlagOverflow != 0 {
			start = off + leafOverflowHeader
		}
		return n[start : start+keyLen]
	}
	keyLen := int(binary.LittleEndian.Uint16(n[off:]))
	return n[off+internalCellHeader : off+internalCellHeader+keyLen]
}

func (n node) hasOverflow(i int) bool { return n[n.slot(i)]&cellFlagOverflow != 0 }

func (n node) inlineValue(i int) []byte {
	off := n.slot(i)
	keyLen := int(binary.LittleEndian.Uint16(n[off+1:]))
	valLen := int(binary.LittleEndian.Uint32(n[off+3:]))
	start := off + leafInlineHeader + keyLen
	return n[start : start+valLen]
}

func (n node) overflowRef(i int) (page uint64, total int) {
	off := n.slot(i)
	return binary.LittleEndian.Uint64(n[off+7:]), int(binary.LittleEndian.Uint32(n[off+3:]))
}

func (n node) childAt(i int) uint64 {
	if i >= n.numKeys() {
		return n.link()
	}
	return binary.LittleEndian.Uint64(n[n.slot(i)+2:])
}

func (n node) setChildAt(i int, v uint64) {
	if i >= n.numKeys() {
		n.setLink(v)
		return
	}
	binary.LittleEndian.PutUint64(n[n.slot(i)+2:], v)
}

// searchLeaf returns the position of key, or the position it would occupy.
func (n node) searchLeaf(key []byte) (int, bool) {
	lo, hi := 0, n.numKeys()
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if bytes.Compare(n.key(mid), key) < 0 {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < n.numKeys() && bytes.Equal(n.key(lo), key) {
		return lo, true
	}
	return lo, false
}

// searchInternal returns the index of the child that may contain key.
func (n node) searchInternal(key []byte) int {
	lo, hi := 0, n.numKeys()
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if bytes.Compare(n.key(mid), key) <= 0 {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

func (n node) insertCell(i int, cell []byte) bool {
	if n.freeSpace() < len(cell)+slotSize {
		return false
	}
	end := n.cellsEnd() - len(cell)
	copy(n[end:], cell)
	n.setCellsEnd(end)

	num := n.numKeys()
	copy(n[nodeHeaderSize+(i+1)*slotSize:nodeHeaderSize+(num+1)*slotSize],
		n[nodeHeaderSize+i*slotSize:nodeHeaderSize+num*slotSize])
	n.setNumKeys(num + 1)
	n.setSlot(i, end)
	return true
}

func (n node) removeCell(i int) {
	num := n.numKeys()
	copy(n[nodeHeaderSize+i*slotSize:], n[nodeHeaderSize+(i+1)*slotSize:nodeHeaderSize+num*slotSize])
	n.setNumKeys(num - 1)
}

// compact rewrites the cell area so the space of deleted cells becomes usable
// again. scratch must be a buffer of at least len(n) bytes.
func (n node) compact(scratch []byte) {
	copy(scratch, n)
	old := node(scratch[:len(n)])
	n.setCellsEnd(len(n))
	for i, num := 0, old.numKeys(); i < num; i++ {
		cell := old.cell(i)
		end := n.cellsEnd() - len(cell)
		copy(n[end:], cell)
		n.setCellsEnd(end)
		n.setSlot(i, end)
	}
}

// appendCell writes a cell at the end of the node. Used when rebuilding a node
// from an ordered list of cells.
func (n node) appendCell(cell []byte) bool {
	return n.insertCell(n.numKeys(), cell)
}

func makeInternalCell(key []byte, child uint64) []byte {
	cell := make([]byte, internalCellHeader+len(key))
	binary.LittleEndian.PutUint16(cell, uint16(len(key)))
	binary.LittleEndian.PutUint64(cell[2:], child)
	copy(cell[internalCellHeader:], key)
	return cell
}

func makeInlineLeafCell(key, val []byte) []byte {
	cell := make([]byte, leafInlineHeader+len(key)+len(val))
	binary.LittleEndian.PutUint16(cell[1:], uint16(len(key)))
	binary.LittleEndian.PutUint32(cell[3:], uint32(len(val)))
	copy(cell[leafInlineHeader:], key)
	copy(cell[leafInlineHeader+len(key):], val)
	return cell
}

func makeOverflowLeafCell(key []byte, total int, first uint64) []byte {
	cell := make([]byte, leafOverflowHeader+len(key))
	cell[0] = cellFlagOverflow
	binary.LittleEndian.PutUint16(cell[1:], uint16(len(key)))
	binary.LittleEndian.PutUint32(cell[3:], uint32(total))
	binary.LittleEndian.PutUint64(cell[7:], first)
	copy(cell[leafOverflowHeader:], key)
	return cell
}

// splitPoint picks the index that divides cells into two halves that both fit
// in a page, preferring the most balanced option. It returns -1 when no such
// index exists, which the size limits on keys and inline values prevent.
func splitPoint(cells [][]byte, dropSeparator bool) int {
	capacity := PageSize - nodeHeaderSize
	total := 0
	for _, c := range cells {
		total += len(c) + slotSize
	}

	best, bestScore := -1, 0
	prefix := 0
	for i := 1; i < len(cells); i++ {
		prefix += len(cells[i-1]) + slotSize
		suffix := total - prefix
		if dropSeparator {
			suffix -= len(cells[i]) + slotSize
		}
		if prefix > capacity || suffix > capacity {
			continue
		}
		score := prefix - suffix
		if score < 0 {
			score = -score
		}
		if best == -1 || score < bestScore {
			best, bestScore = i, score
		}
	}
	return best
}
