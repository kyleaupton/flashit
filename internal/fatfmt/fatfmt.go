// Package fatfmt writes an MBR with one FAT32 partition over a whole disk,
// through go-diskfs. It is the only importer of go-diskfs, and it hands
// go-diskfs an adapter over the device it was given: nothing here opens a
// path.
package fatfmt

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"time"

	"github.com/diskfs/go-diskfs/backend"
	"github.com/diskfs/go-diskfs/filesystem/fat32"
	"github.com/diskfs/go-diskfs/partition/mbr"
)

// Device is a whole disk, or an image of one, addressed by byte offset.
// Format only ever reads and writes whole sectors through it.
type Device interface {
	io.ReaderAt
	io.WriterAt
}

const (
	// PartitionStart is where the partition begins, as Windows and parted
	// place it.
	PartitionStart = 1 << 20
	// MaxDiskSize is the most an MBR with 512-byte sectors can address.
	MaxDiskSize = 2 << 40

	minClusters = 65525 // fewer and every driver takes the volume for FAT16
	wipeSize    = 1 << 20
)

// Format writes an MBR with one active FAT32 LBA (0x0C) partition from
// PartitionStart to the end of the disk and formats it with label. size is
// the disk's size in bytes, sectorSize its logical sector size. A trailing
// partial sector is ignored.
func Format(dev Device, size, sectorSize int64, label string) (err error) {
	if sectorSize != 512 && sectorSize != 4096 {
		return fmt.Errorf("sector size %d is not supported, only 512 and 4096", sectorSize)
	}
	if size > MaxDiskSize {
		return fmt.Errorf("disk is %d bytes; MBR cannot address more than %d", size, int64(MaxDiskSize))
	}
	size -= size % sectorSize
	if err := checkLabel(label); err != nil {
		return err
	}
	partSize := size - PartitionStart
	if err := checkLayout(partSize, sectorSize); err != nil {
		return err
	}

	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("go-diskfs panicked: %v", r)
		}
	}()

	sio := &sectorIO{dev: dev, sector: sectorSize, size: size}
	if err := sio.zero(0, wipeSize); err != nil {
		return fmt.Errorf("clear start of disk: %w", err)
	}
	// A stale backup GPT at the end would make firmware and Linux see the
	// old layout.
	if err := sio.zero(size-wipeSize, wipeSize); err != nil {
		return fmt.Errorf("clear end of disk: %w", err)
	}
	if err := writeDiskSignature(sio); err != nil {
		return err
	}

	startLBA := uint32(PartitionStart / sectorSize)
	sectors := uint32(partSize / sectorSize)
	table := &mbr.Table{
		LogicalSectorSize:  int(sectorSize),
		PhysicalSectorSize: int(sectorSize),
		Partitions:         []*mbr.Partition{partition(startLBA, sectors)},
	}
	st := &storage{sio: sio}
	if err := table.Write(st, size); err != nil {
		return fmt.Errorf("write MBR: %w", err)
	}
	if _, err := fat32.Create(st, partSize, PartitionStart, sectorSize, label, false); err != nil {
		return fmt.Errorf("create FAT32: %w", err)
	}
	return nil
}

func checkLabel(label string) error {
	if len(label) == 0 || len(label) > 11 {
		return fmt.Errorf("label %q must be 1 to 11 characters", label)
	}
	for _, r := range label {
		if r < 0x20 || r > 0x7e {
			return fmt.Errorf("label %q must be printable ASCII", label)
		}
	}
	return nil
}

// checkLayout refuses a partition too small to be FAT32. It repeats
// go-diskfs's cluster size table and FAT sizing (fat32.layout), which does
// not check the minimum cluster count itself.
func checkLayout(partSize, sectorSize int64) error {
	var cluster int64
	switch {
	case partSize <= 260*fat32.MB:
		cluster = 512
	case partSize <= 8*fat32.GB:
		cluster = 4 * fat32.KB
	case partSize <= 16*fat32.GB:
		cluster = 8 * fat32.KB
	case partSize <= 32*fat32.GB:
		cluster = 16 * fat32.KB
	default:
		cluster = 32 * fat32.KB
	}
	spc := max(cluster/sectorSize, 1)
	const reserved = 32
	total := partSize / sectorSize
	if total <= reserved {
		return errors.New("disk is too small for FAT32")
	}
	denom := sectorSize*spc + 8
	fatSectors := (4*(total-reserved) + 8*spc + denom - 1) / denom
	clusters := (total - reserved - 2*fatSectors) / spc
	if clusters < minClusters {
		return fmt.Errorf("disk is too small for FAT32: %d clusters, at least %d needed", clusters, minClusters)
	}
	return nil
}

func partition(start, sectors uint32) *mbr.Partition {
	p := &mbr.Partition{
		Index:    1,
		Bootable: true,
		Type:     mbr.Fat32LBA,
		Start:    start,
		Size:     sectors,
	}
	p.StartHead, p.StartSector, p.StartCylinder = chs(start)
	p.EndHead, p.EndSector, p.EndCylinder = chs(start + sectors - 1)
	return p
}

// chs encodes lba for 255 heads and 63 sectors per track, or the
// conventional 1023/254/63 once it is out of CHS range.
func chs(lba uint32) (head, sector, cylinder byte) {
	c := lba / (255 * 63)
	h := (lba / 63) % 255
	s := lba%63 + 1
	if c > 1023 {
		c, h, s = 1023, 254, 63
	}
	return byte(h), byte(s) | byte((c>>2)&0xc0), byte(c)
}

// writeDiskSignature puts a random, non-zero MBR disk signature at 440;
// Windows tells disks apart by it.
func writeDiskSignature(sio *sectorIO) error {
	var sig [4]byte
	for binary.LittleEndian.Uint32(sig[:]) == 0 {
		if _, err := rand.Read(sig[:]); err != nil {
			return err
		}
	}
	if _, err := sio.WriteAt(sig[:], 440); err != nil {
		return fmt.Errorf("write disk signature: %w", err)
	}
	return nil
}

// sectorIO turns go-diskfs's arbitrary reads and writes into whole-sector
// ones, which raw disk handles require.
type sectorIO struct {
	dev    Device
	sector int64
	size   int64
}

func (s *sectorIO) span(off int64, n int) (start, end int64, err error) {
	if off < 0 || off+int64(n) > s.size {
		return 0, 0, fmt.Errorf("access at %d+%d is outside the %d-byte disk", off, n, s.size)
	}
	start = off - off%s.sector
	end = off + int64(n)
	if r := end % s.sector; r != 0 {
		end += s.sector - r
	}
	return start, end, nil
}

func (s *sectorIO) ReadAt(p []byte, off int64) (int, error) {
	start, end, err := s.span(off, len(p))
	if err != nil {
		return 0, err
	}
	if start == off && end == off+int64(len(p)) {
		return s.dev.ReadAt(p, off)
	}
	buf := make([]byte, end-start)
	if _, err := s.dev.ReadAt(buf, start); err != nil {
		return 0, err
	}
	return copy(p, buf[off-start:]), nil
}

func (s *sectorIO) WriteAt(p []byte, off int64) (int, error) {
	start, end, err := s.span(off, len(p))
	if err != nil {
		return 0, err
	}
	if start == off && end == off+int64(len(p)) {
		return s.dev.WriteAt(p, off)
	}
	buf := make([]byte, end-start)
	if _, err := s.dev.ReadAt(buf, start); err != nil {
		return 0, err
	}
	copy(buf[off-start:], p)
	if _, err := s.dev.WriteAt(buf, start); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (s *sectorIO) zero(off, n int64) error {
	if off < 0 {
		off, n = 0, n+off
	}
	_, err := s.WriteAt(make([]byte, n), off)
	return err
}

// storage is the backend.Storage go-diskfs formats through. It has no path
// and no *os.File, so go-diskfs cannot reach the device any other way.
type storage struct {
	sio *sectorIO
	pos int64
}

var (
	_ backend.Storage      = (*storage)(nil)
	_ backend.WritableFile = (*storage)(nil)
)

var errNoFile = errors.New("fatfmt: no OS file behind this device")

func (s *storage) Stat() (fs.FileInfo, error)              { return info{size: s.sio.size}, nil }
func (s *storage) Close() error                            { return nil }
func (s *storage) Sys() (*os.File, error)                  { return nil, errNoFile }
func (s *storage) Path() string                            { return "" }
func (s *storage) Writable() (backend.WritableFile, error) { return s, nil }

func (s *storage) ReadAt(p []byte, off int64) (int, error)  { return s.sio.ReadAt(p, off) }
func (s *storage) WriteAt(p []byte, off int64) (int, error) { return s.sio.WriteAt(p, off) }

func (s *storage) Read(p []byte) (int, error) {
	n, err := s.sio.ReadAt(p, s.pos)
	s.pos += int64(n)
	return n, err
}

func (s *storage) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		offset += s.pos
	case io.SeekEnd:
		offset += s.sio.size
	default:
		return 0, fmt.Errorf("bad whence %d", whence)
	}
	if offset < 0 {
		return 0, fmt.Errorf("negative position %d", offset)
	}
	s.pos = offset
	return offset, nil
}

type info struct{ size int64 }

func (i info) Name() string       { return "disk" }
func (i info) Size() int64        { return i.size }
func (i info) Mode() fs.FileMode  { return fs.ModeDevice }
func (i info) ModTime() time.Time { return time.Time{} }
func (i info) IsDir() bool        { return false }
func (i info) Sys() any           { return nil }
