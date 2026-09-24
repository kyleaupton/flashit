package sources

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"strings"
	"unicode/utf16"
)

const (
	sectorSize = 2048
	pvdSector  = 16
	pvdOffset  = pvdSector * sectorSize

	descTypePrimary       = 1
	descTypeSupplementary = 2
	descTypeTerminator    = 255

	maxDescriptors = 64
	// A directory extent larger than this is not a real one; it stops a
	// crafted image from making the probe allocate gigabytes.
	maxDirSize = 16 << 20

	flagDirectory   = 0x02
	flagMultiExtent = 0x80
)

var (
	iso9660Magic = []byte("CD001")
	mbrSignature = []byte{0x55, 0xAA}
	// Joliet escape sequences: UCS-2 levels 1, 2 and 3.
	jolietEscapes = [][]byte{[]byte("%/@"), []byte("%/C"), []byte("%/E")}
)

// volume is the part of an ISO 9660 image the probe needs.
type volume struct {
	r         io.ReaderAt
	size      int64
	blockSize int64
	label     string
	hybrid    bool
	roots     []dirRef // Joliet first when present, then the primary tree
}

type dirRef struct {
	extent uint32
	length uint32
	joliet bool
}

// dirEntry is one file or directory as listed by its parent, with the sizes
// of a multi-extent file already summed.
type dirEntry struct {
	name  string
	dir   bool
	ref   dirRef
	total int64
	multi bool // more extents of this file follow
}

func readVolume(r io.ReaderAt, size int64) (*volume, error) {
	if size < pvdOffset+sectorSize {
		return nil, &probeError{"file too small to be an ISO 9660 image"}
	}
	pvd := make([]byte, sectorSize)
	if _, err := r.ReadAt(pvd, pvdOffset); err != nil {
		return nil, err
	}
	if pvd[0] != descTypePrimary || !bytes.Equal(pvd[1:6], iso9660Magic) {
		return nil, &probeError{"not an ISO 9660 image"}
	}

	v := &volume{
		r:         r,
		size:      size,
		blockSize: int64(binary.LittleEndian.Uint16(pvd[128:130])),
		label:     strings.TrimRight(string(pvd[40:72]), " "),
	}
	if v.blockSize == 0 {
		v.blockSize = sectorSize
	}

	mbr := make([]byte, 2)
	if _, err := r.ReadAt(mbr, 510); err != nil {
		return nil, err
	}
	v.hybrid = bytes.Equal(mbr, mbrSignature)

	primary, err := parseRecord(pvd[156:190], false)
	if err != nil {
		return nil, fmt.Errorf("root directory record: %w", err)
	}

	desc := make([]byte, sectorSize)
	for i := 1; i < maxDescriptors; i++ {
		if _, err := r.ReadAt(desc, pvdOffset+int64(i)*sectorSize); err != nil {
			break
		}
		if !bytes.Equal(desc[1:6], iso9660Magic) || desc[0] == descTypeTerminator {
			break
		}
		if desc[0] != descTypeSupplementary || !isJoliet(desc[88:91]) {
			continue
		}
		joliet, err := parseRecord(desc[156:190], true)
		if err != nil {
			continue
		}
		v.roots = append(v.roots, dirRef{extent: joliet.ref.extent, length: joliet.ref.length, joliet: true})
		break
	}
	v.roots = append(v.roots, dirRef{extent: primary.ref.extent, length: primary.ref.length})
	return v, nil
}

func isJoliet(escape []byte) bool {
	for _, e := range jolietEscapes {
		if bytes.Equal(escape, e) {
			return true
		}
	}
	return false
}

// lookup finds a file by path, trying each directory tree in order. The
// second result is false when no tree has it.
func (v *volume) lookup(path ...string) (int64, bool, error) {
	for _, root := range v.roots {
		size, found, err := v.lookupIn(root, path)
		if err != nil {
			return 0, false, err
		}
		if found {
			return size, true, nil
		}
	}
	return 0, false, nil
}

func (v *volume) lookupIn(dir dirRef, path []string) (int64, bool, error) {
	for i, name := range path {
		entries, err := v.readDir(dir)
		if err != nil {
			return 0, false, err
		}
		var match *dirEntry
		for j := range entries {
			if strings.EqualFold(entries[j].name, name) {
				match = &entries[j]
				break
			}
		}
		if match == nil {
			return 0, false, nil
		}
		last := i == len(path)-1
		if last {
			if match.dir {
				return 0, false, nil
			}
			return match.total, true, nil
		}
		if !match.dir {
			return 0, false, nil
		}
		dir = match.ref
	}
	return 0, false, nil
}

func (v *volume) readDir(dir dirRef) ([]dirEntry, error) {
	if dir.length == 0 || dir.length > maxDirSize {
		return nil, fmt.Errorf("directory extent %d has implausible size %d", dir.extent, dir.length)
	}
	off := int64(dir.extent) * v.blockSize
	if off+int64(dir.length) > v.size {
		return nil, fmt.Errorf("directory extent %d lies past the end of the image", dir.extent)
	}
	buf := make([]byte, dir.length)
	if _, err := v.r.ReadAt(buf, off); err != nil {
		return nil, err
	}

	var entries []dirEntry
	pos := 0
	for pos < len(buf) {
		n := int(buf[pos])
		if n == 0 {
			// Records never straddle a sector; a zero length pads to the next one.
			pos = (pos/sectorSize + 1) * sectorSize
			continue
		}
		if pos+n > len(buf) || n < 33 {
			break
		}
		rec, err := parseRecord(buf[pos:pos+n], dir.joliet)
		pos += n
		if err != nil {
			continue
		}
		if rec.name == "" || rec.name == "\x00" || rec.name == "\x01" {
			continue
		}
		// Windows install.wim exceeds the 4 GiB an extent can describe, so it
		// is listed as consecutive records with the same name; every one but
		// the last carries the multi-extent flag.
		if last := len(entries) - 1; last >= 0 && entries[last].multi && !rec.dir && entries[last].name == rec.name {
			entries[last].total += rec.total
			entries[last].multi = rec.multi
			continue
		}
		entries = append(entries, rec)
	}
	return entries, nil
}

func parseRecord(b []byte, joliet bool) (dirEntry, error) {
	if len(b) < 33 {
		return dirEntry{}, fmt.Errorf("record too short (%d bytes)", len(b))
	}
	nameLen := int(b[32])
	if 33+nameLen > len(b) {
		return dirEntry{}, fmt.Errorf("name length %d exceeds record", nameLen)
	}
	flags := b[25]
	raw := b[33 : 33+nameLen]
	e := dirEntry{
		dir: flags&flagDirectory != 0,
		ref: dirRef{
			extent: binary.LittleEndian.Uint32(b[2:6]),
			length: binary.LittleEndian.Uint32(b[10:14]),
			joliet: joliet,
		},
	}
	e.total = int64(e.ref.length)
	e.name = decodeName(raw, joliet)
	e.multi = flags&flagMultiExtent != 0
	return e, nil
}

func decodeName(raw []byte, joliet bool) string {
	if len(raw) == 1 && (raw[0] == 0 || raw[0] == 1) {
		return string(raw)
	}
	var name string
	if joliet && len(raw)%2 == 0 {
		u := make([]uint16, len(raw)/2)
		for i := range u {
			u[i] = binary.BigEndian.Uint16(raw[2*i:])
		}
		name = string(utf16.Decode(u))
	} else {
		name = string(raw)
	}
	// ISO 9660 level names carry a ";1" version; a trailing dot marks an
	// empty extension.
	if i := strings.IndexByte(name, ';'); i >= 0 {
		name = name[:i]
	}
	return strings.TrimSuffix(name, ".")
}
