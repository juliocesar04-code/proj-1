package storage

import (
	"bufio"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"os"
)

// Write-ahead log
//
// The log carries redo records only. Because a dirty page is pinned in the
// buffer pool until its transaction commits (a no-steal policy), the data file
// never contains uncommitted state and undo records are unnecessary.
//
// Record layout:
//
//	kind(1) txn(8) page(8) length(4) payload checksum(4)
//
// Recovery replays the page images of every transaction whose commit record
// survived, in log order, and then truncates the log.
const (
	logRecordPage   = 1
	logRecordCommit = 2

	logHeaderSize = 21
	logFooterSize = 4
)

type wal struct {
	file  *os.File
	bytes int64
	sync  bool
	buf   []byte
}

func openLog(path string, sync bool) (*wal, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	return &wal{
		file:  file,
		bytes: info.Size(),
		sync:  sync,
		buf:   make([]byte, logHeaderSize+PageSize+logFooterSize),
	}, nil
}

func (w *wal) size() int64 { return w.bytes }

func (w *wal) write(kind uint8, txn, page uint64, payload []byte) error {
	rec := w.buf[:logHeaderSize+len(payload)+logFooterSize]
	rec[0] = kind
	binary.LittleEndian.PutUint64(rec[1:], txn)
	binary.LittleEndian.PutUint64(rec[9:], page)
	binary.LittleEndian.PutUint32(rec[17:], uint32(len(payload)))
	copy(rec[logHeaderSize:], payload)
	binary.LittleEndian.PutUint32(rec[logHeaderSize+len(payload):],
		crc32.ChecksumIEEE(rec[:logHeaderSize+len(payload)]))

	n, err := w.file.Write(rec)
	w.bytes += int64(n)
	return err
}

func (w *wal) appendPage(txn, page uint64, data []byte) error {
	return w.write(logRecordPage, txn, page, data)
}

func (w *wal) appendCommit(txn uint64) error {
	if err := w.write(logRecordCommit, txn, 0, nil); err != nil {
		return err
	}
	if !w.sync {
		return nil
	}
	return w.file.Sync()
}

func (w *wal) reset() error {
	if err := w.file.Truncate(0); err != nil {
		return err
	}
	if _, err := w.file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	w.bytes = 0
	if !w.sync {
		return nil
	}
	return w.file.Sync()
}

func (w *wal) close() error { return w.file.Close() }

type logRecord struct {
	page uint64
	data []byte
}

// replayLog applies the committed part of the log to the data file. A partial
// record at the tail, which is what a crash in the middle of a commit leaves
// behind, is discarded along with everything else from that transaction.
func replayLog(path string, data *os.File) error {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer file.Close()

	reader := bufio.NewReaderSize(file, 1<<20)
	header := make([]byte, logHeaderSize)
	footer := make([]byte, logFooterSize)

	var pending, committed []logRecord
	var currentTxn uint64

	for {
		if _, err := io.ReadFull(reader, header); err != nil {
			break
		}
		kind := header[0]
		txn := binary.LittleEndian.Uint64(header[1:])
		page := binary.LittleEndian.Uint64(header[9:])
		length := binary.LittleEndian.Uint32(header[17:])
		if length > PageSize {
			break
		}

		payload := make([]byte, length)
		if _, err := io.ReadFull(reader, payload); err != nil {
			break
		}
		if _, err := io.ReadFull(reader, footer); err != nil {
			break
		}

		sum := crc32.NewIEEE()
		sum.Write(header)
		sum.Write(payload)
		if sum.Sum32() != binary.LittleEndian.Uint32(footer) {
			break
		}

		if currentTxn != 0 && txn != currentTxn && kind == logRecordPage {
			pending = pending[:0]
		}
		currentTxn = txn

		switch kind {
		case logRecordPage:
			pending = append(pending, logRecord{page: page, data: payload})
		case logRecordCommit:
			committed = append(committed, pending...)
			pending = nil
			currentTxn = 0
		default:
			pending = nil
		}
	}

	if len(committed) == 0 {
		return os.Truncate(path, 0)
	}
	for _, rec := range committed {
		if _, err := data.WriteAt(rec.data, int64(rec.page)*PageSize); err != nil {
			return err
		}
	}
	if err := data.Sync(); err != nil {
		return err
	}
	return os.Truncate(path, 0)
}
