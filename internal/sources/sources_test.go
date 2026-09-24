package sources

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"unicode/utf16"
)

// image builds a small ISO 9660 image, sector by sector, the way mkisofs
// and oscdimg lay one out. Sectors 0-15 are the system area, 16 the PVD, 17
// an optional Joliet SVD, 18 the terminator, and directories follow.
type image struct {
	sectors map[int][]byte
	next    int
}

func newImage() *image { return &image{sectors: map[int][]byte{}, next: 19} }

func (im *image) sector(n int) []byte {
	if im.sectors[n] == nil {
		im.sectors[n] = make([]byte, sectorSize)
	}
	return im.sectors[n]
}

func (im *image) alloc() int { n := im.next; im.next++; return n }

func (im *image) bytes() []byte {
	end := im.next
	for n := range im.sectors {
		if n >= end {
			end = n + 1
		}
	}
	out := make([]byte, end*sectorSize)
	for n, s := range im.sectors {
		copy(out[n*sectorSize:], s)
	}
	return out
}

func (im *image) write(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.iso")
	if err := os.WriteFile(path, im.bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func both32(b []byte, v uint32) {
	binary.LittleEndian.PutUint32(b[0:4], v)
	binary.BigEndian.PutUint32(b[4:8], v)
}

// record encodes one directory record. A raw name is written as given; the
// caller passes UCS-2 for Joliet trees.
func record(name []byte, extent, length uint32, flags byte) []byte {
	n := 33 + len(name)
	if n%2 == 1 {
		n++
	}
	b := make([]byte, n)
	b[0] = byte(n)
	both32(b[2:10], extent)
	both32(b[10:18], length)
	b[25] = flags
	binary.LittleEndian.PutUint16(b[28:30], 1)
	binary.BigEndian.PutUint16(b[30:32], 1)
	b[32] = byte(len(name))
	copy(b[33:], name)
	return b
}

func ucs2(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, 2*len(u))
	for i, c := range u {
		binary.BigEndian.PutUint16(b[2*i:], c)
	}
	return b
}

type entry struct {
	name   string
	dir    int      // sector of a subdirectory, when this is one
	sizes  []uint32 // one extent per size; more than one makes a multi-extent file
	joliet bool
}

// dir writes a directory listing at sector n.
func (im *image) dir(n int, self, parent int, entries []entry) {
	s := im.sector(n)
	pos := 0
	put := func(r []byte) {
		copy(s[pos:], r)
		pos += len(r)
	}
	put(record([]byte{0}, uint32(self), sectorSize, flagDirectory))
	put(record([]byte{1}, uint32(parent), sectorSize, flagDirectory))
	for _, e := range entries {
		name := []byte(e.name)
		if e.joliet {
			name = ucs2(e.name)
		}
		if e.dir > 0 {
			put(record(name, uint32(e.dir), sectorSize, flagDirectory))
			continue
		}
		for i, size := range e.sizes {
			flags := byte(0)
			if i < len(e.sizes)-1 {
				flags = flagMultiExtent
			}
			put(record(name, 1000, size, flags))
		}
	}
}

func (im *image) descriptor(n int, typ byte, label string, root int) []byte {
	s := im.sector(n)
	s[0] = typ
	copy(s[1:6], iso9660Magic)
	s[6] = 1
	copy(s[40:72], []byte("                                "))
	copy(s[40:], label)
	binary.LittleEndian.PutUint16(s[128:130], sectorSize)
	binary.BigEndian.PutUint16(s[130:132], sectorSize)
	copy(s[156:190], record([]byte{0}, uint32(root), sectorSize, flagDirectory))
	return s
}

func (im *image) pvd(label string, root int) { im.descriptor(pvdSector, descTypePrimary, label, root) }

func (im *image) joliet(root int) {
	s := im.descriptor(17, descTypeSupplementary, "", root)
	copy(s[40:], ucs2("JOLIET LABEL"))
	copy(s[88:91], "%/E")
}

func (im *image) terminator(n int) {
	s := im.sector(n)
	s[0] = descTypeTerminator
	copy(s[1:6], iso9660Magic)
}

func (im *image) mbr() { copy(im.sector(0)[510:512], mbrSignature) }

// tree writes a root directory holding "sources" with the given files and
// returns the root sector.
func (im *image) tree(joliet bool, files ...entry) int {
	root, sources := im.alloc(), im.alloc()
	for i := range files {
		files[i].joliet = joliet
	}
	im.dir(root, root, root, []entry{{name: "SOURCES", dir: sources, joliet: joliet}})
	im.dir(sources, sources, root, files)
	return root
}

func probe(t *testing.T, im *image) SourceInfo {
	t.Helper()
	info, err := Probe(im.write(t))
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	return info
}

func TestProbe_PlainISO9660Windows(t *testing.T) {
	im := newImage()
	root := im.tree(false, entry{name: "INSTALL.WIM;1", sizes: []uint32{123456}})
	im.pvd("WIN_TEST", root)
	im.terminator(17)

	info := probe(t, im)
	if info.Kind != WindowsISO || !info.HasWIM || info.WIMSize != 123456 {
		t.Fatalf("got %+v", info)
	}
	if info.Label != "WIN_TEST" || info.Hybrid || info.Reason != "" {
		t.Fatalf("got %+v", info)
	}
}

func TestProbe_InstallESD(t *testing.T) {
	im := newImage()
	root := im.tree(false, entry{name: "INSTALL.ESD;1", sizes: []uint32{99}})
	im.pvd("ESD", root)
	im.terminator(17)

	info := probe(t, im)
	if info.Kind != WindowsISO || info.WIMSize != 99 {
		t.Fatalf("got %+v", info)
	}
}

func TestProbe_JolietPreferred(t *testing.T) {
	im := newImage()
	// The primary tree only has the 8.3 name; the Joliet tree has the real
	// one with a different size, so the result shows which tree was read.
	primary := im.tree(false, entry{name: "INSTALL.WIM;1", sizes: []uint32{1}})
	joliet := im.tree(true, entry{name: "install.wim", sizes: []uint32{2}})
	im.pvd("PRIMARY", primary)
	im.joliet(joliet)
	im.terminator(18)

	info := probe(t, im)
	if info.Kind != WindowsISO || info.WIMSize != 2 {
		t.Fatalf("got %+v", info)
	}
	if info.Label != "PRIMARY" {
		t.Fatalf("label should come from the PVD, got %q", info.Label)
	}
}

func TestProbe_JolietFallsBackToPrimary(t *testing.T) {
	im := newImage()
	primary := im.tree(false, entry{name: "INSTALL.WIM;1", sizes: []uint32{7}})
	joliet := im.tree(true, entry{name: "other.txt", sizes: []uint32{1}})
	im.pvd("X", primary)
	im.joliet(joliet)
	im.terminator(18)

	if info := probe(t, im); info.WIMSize != 7 {
		t.Fatalf("got %+v", info)
	}
}

func TestProbe_MultiExtent(t *testing.T) {
	im := newImage()
	root := im.tree(true, entry{name: "install.wim", sizes: []uint32{0xFFFFF800, 0xFFFFF800, 4096}})
	im.pvd("BIG", root)
	im.joliet(root)
	im.terminator(18)

	info := probe(t, im)
	if want := int64(0xFFFFF800)*2 + 4096; info.WIMSize != want {
		t.Fatalf("WIMSize = %d, want %d", info.WIMSize, want)
	}
}

func TestProbe_HybridLinux(t *testing.T) {
	im := newImage()
	root := im.alloc()
	im.dir(root, root, root, nil)
	im.pvd("alpine-virt 3.22.1 x86_64", root)
	im.terminator(17)
	im.mbr()

	info := probe(t, im)
	if info.Kind != LinuxISO || !info.Hybrid || info.HasWIM || info.Reason != "" {
		t.Fatalf("got %+v", info)
	}
	if info.Label != "alpine-virt 3.22.1 x86_64" {
		t.Fatalf("label %q", info.Label)
	}
}

func TestProbe_NonHybridNoSources(t *testing.T) {
	im := newImage()
	root := im.alloc()
	im.dir(root, root, root, []entry{{name: "README.TXT;1", sizes: []uint32{10}}})
	im.pvd("DATA", root)
	im.terminator(17)

	info := probe(t, im)
	if info.Kind != Unknown || info.Reason != "not a hybrid ISO and no Windows sources" {
		t.Fatalf("got %+v", info)
	}
	if info.Label != "DATA" {
		t.Fatalf("label %q", info.Label)
	}
}

func TestProbe_NotISO(t *testing.T) {
	path := filepath.Join(t.TempDir(), "junk.bin")
	if err := os.WriteFile(path, make([]byte, 40*sectorSize), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := Probe(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Kind != Unknown || info.Reason != "not an ISO 9660 image" || info.Size != 40*sectorSize {
		t.Fatalf("got %+v", info)
	}
}

func TestProbe_ShortFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "short.iso")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := Probe(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Kind != Unknown || info.Reason != "file too small to be an ISO 9660 image" {
		t.Fatalf("got %+v", info)
	}
}

func TestProbe_Unreadable(t *testing.T) {
	if _, err := Probe(filepath.Join(t.TempDir(), "missing.iso")); err == nil {
		t.Fatal("expected an error for a missing file")
	}
	if _, err := Probe(t.TempDir()); err == nil {
		t.Fatal("expected an error for a directory")
	}
}

// udfTag writes a descriptor tag with the identifier the reader checks.
func udfTag(b []byte, id uint16) { binary.LittleEndian.PutUint16(b[0:2], id) }

func putLongAD(b []byte, length, block uint32, part uint16) {
	binary.LittleEndian.PutUint32(b[0:4], length)
	binary.LittleEndian.PutUint32(b[4:8], block)
	binary.LittleEndian.PutUint16(b[8:10], part)
}

// udfFID encodes a File Identifier Descriptor with an 8-bit compressed name.
func udfFID(name string, chars byte, icbBlock uint32) []byte {
	var raw []byte
	if name != "" {
		raw = append([]byte{8}, name...)
	}
	n := 38 + len(raw)
	n += (4 - n%4) % 4
	b := make([]byte, n)
	udfTag(b, tagFileIdentifier)
	b[18] = chars
	b[19] = byte(len(raw))
	putLongAD(b[20:36], sectorSize, icbBlock, 0)
	copy(b[38:], raw)
	return b
}

// windowsUDF lays out what Microsoft ships: an ISO 9660 side holding only
// README.TXT and a UDF side with sources/install.wim. The partition starts
// at sector 300; the root directory lists through short allocation
// descriptors, the sources directory embeds its listing, and install.wim
// is an Extended File Entry.
func windowsUDF(wimSize uint64) *image {
	im := newImage()
	root := im.alloc()
	im.dir(root, root, root, []entry{{name: "README.TXT;1", sizes: []uint32{135}}})
	im.pvd("CCCOMA_X64FRE_EN-US_DV9", root)
	im.terminator(17)

	const part = 300
	anchor := im.sector(udfAnchorSector)
	udfTag(anchor, tagAnchor)
	binary.LittleEndian.PutUint32(anchor[16:20], 4*sectorSize)
	binary.LittleEndian.PutUint32(anchor[20:24], 257)

	pd := im.sector(257)
	udfTag(pd, tagPartition)
	binary.LittleEndian.PutUint16(pd[22:24], 0)
	binary.LittleEndian.PutUint32(pd[188:192], part)

	lvd := im.sector(258)
	udfTag(lvd, tagLogicalVolume)
	binary.LittleEndian.PutUint32(lvd[212:216], sectorSize)
	putLongAD(lvd[248:264], sectorSize, 0, 0)
	binary.LittleEndian.PutUint32(lvd[268:272], 1)
	lvd[440], lvd[441] = 1, 6
	binary.LittleEndian.PutUint16(lvd[444:446], 0)

	udfTag(im.sector(259), tagTerminating)

	fsd := im.sector(part)
	udfTag(fsd, tagFileSet)
	putLongAD(fsd[400:416], sectorSize, 1, 0)

	rootData := append(udfFID("", fidParent|fidDirectory, 1), udfFID("sources", fidDirectory, 3)...)
	copy(im.sector(part+2), rootData)
	rootFE := im.sector(part + 1)
	udfTag(rootFE, tagFileEntry)
	binary.LittleEndian.PutUint16(rootFE[34:36], adShort)
	binary.LittleEndian.PutUint64(rootFE[56:64], uint64(len(rootData)))
	binary.LittleEndian.PutUint32(rootFE[172:176], 8)
	binary.LittleEndian.PutUint32(rootFE[176:180], uint32(len(rootData)))
	binary.LittleEndian.PutUint32(rootFE[180:184], 2)

	sourcesData := append(udfFID("", fidParent|fidDirectory, 1), udfFID("install.wim", 0, 4)...)
	sourcesFE := im.sector(part + 3)
	udfTag(sourcesFE, tagFileEntry)
	binary.LittleEndian.PutUint16(sourcesFE[34:36], adEmbedded)
	binary.LittleEndian.PutUint64(sourcesFE[56:64], uint64(len(sourcesData)))
	binary.LittleEndian.PutUint32(sourcesFE[172:176], uint32(len(sourcesData)))
	copy(sourcesFE[176:], sourcesData)

	wimFE := im.sector(part + 4)
	udfTag(wimFE, tagExtendedFileEntry)
	binary.LittleEndian.PutUint16(wimFE[34:36], adShort)
	binary.LittleEndian.PutUint64(wimFE[56:64], wimSize)
	return im
}

func TestProbe_WindowsUDF(t *testing.T) {
	info := probe(t, windowsUDF(6017925238))
	if info.Kind != WindowsISO || !info.HasWIM || info.WIMSize != 6017925238 {
		t.Fatalf("got %+v", info)
	}
	if info.Label != "CCCOMA_X64FRE_EN-US_DV9" || info.Hybrid {
		t.Fatalf("got %+v", info)
	}
}

func TestProbe_UDFWithoutSources(t *testing.T) {
	im := windowsUDF(1)
	// Rename install.wim so the walk succeeds but finds nothing.
	copy(im.sector(303)[176:], append(udfFID("", fidParent|fidDirectory, 1), udfFID("install.txt", 0, 4)...))
	info := probe(t, im)
	if info.Kind != Unknown || info.HasWIM {
		t.Fatalf("got %+v", info)
	}
}
