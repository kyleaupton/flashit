package isofs

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"golift.io/udf"
)

// ErrNoUDF means the image has no UDF file system at all, as on Linux
// hybrid ISOs, rather than one that failed a check.
var ErrNoUDF = errors.New("no UDF anchor")

const (
	tagAnchor      = 2
	tagPartition   = 5
	tagLogicalVol  = 6
	tagTerminating = 8
	tagFileSet     = 256
)

// rootICB reads the volume layout the same way golift does and returns
// where the root directory's file entry is. It runs before golift parses
// anything, because golift follows metadata partition extents and the
// root's allocation descriptors without a bound on the total; a crafted
// chain there fans out into billions of reads. Microsoft's ISOs have one
// type 1 partition and nothing else, so that is all this accepts.
func rootICB(r io.ReaderAt, size int64) (udf.ExtentLong, error) {
	const sector = udf.SectorSize
	read := func(n uint64) ([]byte, error) {
		buf := make([]byte, sector)
		if _, err := r.ReadAt(buf, int64(n)*sector); err != nil {
			return nil, err
		}
		return buf, nil
	}

	anchors := []uint64{256}
	if size >= sector {
		last := uint64(size/sector) - 1
		if last != 256 {
			anchors = append(anchors, last)
		}
		if last > 256 {
			anchors = append(anchors, last-256)
		}
	}
	var anchor []byte
	for _, s := range anchors {
		if b, err := read(s); err == nil && binary.LittleEndian.Uint16(b) == tagAnchor {
			anchor = b
			break
		}
	}
	if anchor == nil {
		return udf.ExtentLong{}, ErrNoUDF
	}

	start := uint64(binary.LittleEndian.Uint32(anchor[20:]))
	count := uint64(binary.LittleEndian.Uint32(anchor[16:])&0x3fffffff) / sector
	if count == 0 {
		count = 16
	}
	count = min(count, 64)

	var partitions, lvds int
	var partStart uint32
	var lvd []byte
	terminated := false
	for i := uint64(0); i < count && !terminated; i++ {
		b, err := read(start + i)
		if err != nil {
			return udf.ExtentLong{}, err
		}
		switch binary.LittleEndian.Uint16(b) {
		case tagPartition:
			partitions++
			partStart = binary.LittleEndian.Uint32(b[188:])
		case tagLogicalVol:
			lvds++
			lvd = b
		case tagTerminating:
			terminated = true
		}
	}
	if !terminated || lvd == nil {
		return udf.ExtentLong{}, errors.New("no terminated UDF volume descriptor sequence")
	}
	if partitions != 1 || lvds != 1 {
		return udf.ExtentLong{}, fmt.Errorf("%d partitions and %d logical volumes, want one of each", partitions, lvds)
	}
	if n := binary.LittleEndian.Uint32(lvd[268:]); n != 1 || lvd[440] != 1 || lvd[441] != 6 {
		return udf.ExtentLong{}, errors.New("unsupported UDF partition map")
	}

	fsd := udf.NewExtentLong(lvd[248:])
	b, err := read(uint64(partStart) + fsd.Location)
	if err != nil {
		return udf.ExtentLong{}, fmt.Errorf("file set descriptor: %w", err)
	}
	if binary.LittleEndian.Uint16(b) != tagFileSet {
		return udf.ExtentLong{}, errors.New("no UDF file set descriptor")
	}
	return udf.NewExtentLong(b[400:]), nil
}
