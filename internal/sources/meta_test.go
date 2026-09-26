package sources

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"unicode/utf16"
)

// files writes a primary directory tree holding the given files, keyed by
// slash-separated ISO 9660 names, and returns the root sector.
func (im *image) files(files map[string][]byte) int {
	type node struct {
		dirs  map[string]*node
		files map[string][]byte
	}
	newNode := func() *node { return &node{dirs: map[string]*node{}, files: map[string][]byte{}} }
	root := newNode()
	for path, data := range files {
		parts := strings.Split(path, "/")
		n := root
		for _, d := range parts[:len(parts)-1] {
			if n.dirs[d] == nil {
				n.dirs[d] = newNode()
			}
			n = n.dirs[d]
		}
		n.files[parts[len(parts)-1]] = data
	}

	var write func(n *node, self, parent int)
	write = func(n *node, self, parent int) {
		s := im.sector(self)
		pos := 0
		put := func(r []byte) { copy(s[pos:], r); pos += len(r) }
		put(record([]byte{0}, uint32(self), sectorSize, flagDirectory))
		put(record([]byte{1}, uint32(parent), sectorSize, flagDirectory))
		names := make([]string, 0, len(n.dirs)+len(n.files))
		for name := range n.dirs {
			names = append(names, name)
		}
		for name := range n.files {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if child, ok := n.dirs[name]; ok {
				sec := im.alloc()
				put(record([]byte(name), uint32(sec), sectorSize, flagDirectory))
				write(child, sec, self)
				continue
			}
			data := n.files[name]
			sec := im.alloc()
			for i := 1; i*sectorSize < len(data); i++ {
				im.alloc()
			}
			for i := 0; i*sectorSize < len(data); i++ {
				copy(im.sector(sec+i), data[i*sectorSize:])
			}
			put(record([]byte(name), uint32(sec), uint32(len(data)), 0))
		}
	}
	sec := im.alloc()
	write(root, sec, sec)
	return sec
}

// catalogEntry is one 32-byte El Torito boot catalog entry.
func catalogEntry(kind, platform byte, count uint16) []byte {
	e := make([]byte, catalogEntrySize)
	e[0], e[1] = kind, platform
	binary.LittleEndian.PutUint16(e[2:4], count)
	return e
}

// elTorito writes a boot record at sector 17 pointing at a catalog whose
// validation entry names platform and is followed by entries.
func (im *image) elTorito(platform byte, entries ...[]byte) {
	br := im.sector(17)
	br[0] = descTypeBoot
	copy(br[1:6], iso9660Magic)
	br[6] = 1
	copy(br[7:], elTorito)
	cat := im.alloc()
	binary.LittleEndian.PutUint32(br[71:75], uint32(cat))

	s := im.sector(cat)
	s[0], s[1] = validationEntryID, platform
	s[30], s[31] = 0x55, 0xAA
	var sum uint16
	for i := 0; i < catalogEntrySize; i += 2 {
		sum += binary.LittleEndian.Uint16(s[i:])
	}
	binary.LittleEndian.PutUint16(s[28:30], -sum)
	for i, e := range entries {
		copy(s[(i+1)*catalogEntrySize:], e)
	}
}

// wimFile builds a WIM header with the XML fixture from testdata/wim as its
// XML resource, encoded as Windows writes it.
func wimFile(t testing.TB, fixture string) []byte {
	t.Helper()
	text, err := os.ReadFile(filepath.Join("testdata", "wim", fixture))
	if err != nil {
		t.Fatal(err)
	}
	return wimWithXML(string(text))
}

func wimWithXML(text string) []byte {
	u := append([]uint16{0xFEFF}, utf16.Encode([]rune(text))...)
	xmlData := make([]byte, 2*len(u))
	for i, c := range u {
		binary.LittleEndian.PutUint16(xmlData[2*i:], c)
	}
	hdr := make([]byte, wimHeaderSize)
	copy(hdr, wimMagic)
	binary.LittleEndian.PutUint32(hdr[8:12], wimHeaderSize)
	binary.LittleEndian.PutUint64(hdr[wimXMLOffset:], uint64(len(xmlData)))
	binary.LittleEndian.PutUint64(hdr[wimXMLOffset+8:], wimHeaderSize)
	binary.LittleEndian.PutUint64(hdr[wimXMLOffset+16:], uint64(len(xmlData)))
	return append(hdr, xmlData...)
}

func linuxImage(files map[string][]byte, catalog func(im *image)) *image {
	im := newImage()
	root := im.files(files)
	im.pvd("LINUX", root)
	if catalog != nil {
		catalog(im)
		im.terminator(18)
	} else {
		im.terminator(17)
	}
	im.mbr()
	return im
}

func windowsImage(wim []byte) *image {
	im := newImage()
	im.pvd("CCCOMA_X64FRE_EN-US_DV9", im.files(map[string][]byte{"SOURCES/INSTALL.WIM;1": wim}))
	im.elTorito(platformBIOS, catalogEntry(entryBootable, 0, 0),
		catalogEntry(entryFinalHeader, platformUEFI, 1), catalogEntry(entryBootable, 0, 0))
	im.terminator(18)
	return im
}

func biosAndUEFI(im *image) {
	im.elTorito(platformBIOS, catalogEntry(entryBootable, 0, 0),
		catalogEntry(entryFinalHeader, platformUEFI, 1), catalogEntry(entryBootable, 0, 0))
}

func TestProbe_Metadata(t *testing.T) {
	ubuntuInfo := []byte("Ubuntu 24.04.1 LTS \"Noble Numbat\" - Release amd64 (20240827)\nsecond line\n")
	longInfo := []byte("\x1b[31m" + strings.Repeat("é", 300) + "\n")
	bigInfo := append([]byte("Debian GNU/Linux 12.7.0 \"Bookworm\" - Official amd64 NETINST\n"), bytes.Repeat([]byte{'x'}, 10000)...)

	tests := []struct {
		name string
		im   func(t *testing.T) *image
		want SourceInfo
	}{
		{
			name: "ubuntu",
			im: func(*testing.T) *image {
				return linuxImage(map[string][]byte{
					".DISK/INFO;1":              ubuntuInfo,
					"EFI/BOOT/BOOTIA32.EFI;1":   {1},
					"EFI/BOOT/BOOTX64.EFI;1":    {1},
					"EFI/BOOT/GRUBX64.EFI;1":    {1},
					"CASPER/FILESYSTEM.SQUASHF": {1},
				}, biosAndUEFI)
			},
			want: SourceInfo{Kind: LinuxISO, Name: `Ubuntu 24.04.1 LTS "Noble Numbat"`, Arch: "x86-64", BIOS: true, UEFI: true},
		},
		{
			name: "arm64 uefi only, no disk info",
			im: func(*testing.T) *image {
				return linuxImage(map[string][]byte{"EFI/BOOT/bootaa64.efi": {1}},
					func(im *image) { im.elTorito(platformUEFI, catalogEntry(entryBootable, 0, 0)) })
			},
			want: SourceInfo{Kind: LinuxISO, Arch: "ARM64", UEFI: true},
		},
		{
			name: "ia32 only",
			im: func(*testing.T) *image {
				return linuxImage(map[string][]byte{"EFI/BOOT/BOOTIA32.EFI;1": {1}}, nil)
			},
			want: SourceInfo{Kind: LinuxISO, Arch: "x86"},
		},
		{
			name: "bios only, non-bootable uefi section",
			im: func(*testing.T) *image {
				return linuxImage(map[string][]byte{"EFI/BOOT/BOOTRISCV64.EFI;1": {1}}, func(im *image) {
					im.elTorito(platformBIOS, catalogEntry(entryBootable, 0, 0),
						catalogEntry(entryFinalHeader, platformUEFI, 1), catalogEntry(entryNotBootable, 0, 0))
				})
			},
			want: SourceInfo{Kind: LinuxISO, Arch: "RISC-V", BIOS: true},
		},
		{
			name: "catalog with a bad checksum",
			im: func(*testing.T) *image {
				return linuxImage(nil, func(im *image) {
					biosAndUEFI(im)
					im.sector(im.next - 1)[28]++
				})
			},
			want: SourceInfo{Kind: LinuxISO},
		},
		{
			name: "catalog past the end of the image",
			im: func(*testing.T) *image {
				return linuxImage(nil, func(im *image) {
					biosAndUEFI(im)
					binary.LittleEndian.PutUint32(im.sector(17)[71:75], 1<<31)
				})
			},
			want: SourceInfo{Kind: LinuxISO},
		},
		{
			name: "disk info with control characters, capped",
			im: func(*testing.T) *image {
				return linuxImage(map[string][]byte{".DISK/INFO;1": longInfo}, nil)
			},
			want: SourceInfo{Kind: LinuxISO, Name: "[31m" + strings.Repeat("é", maxText-4)},
		},
		{
			name: "disk info over the cap",
			im: func(*testing.T) *image {
				return linuxImage(map[string][]byte{".DISK/INFO;1": bigInfo}, nil)
			},
			want: SourceInfo{Kind: LinuxISO, Name: `Debian GNU/Linux 12.7.0 "Bookworm"`},
		},
		{
			name: "windows 11 24h2",
			im:   func(t *testing.T) *image { return windowsImage(wimFile(t, "win11-24h2.xml")) },
			want: SourceInfo{Kind: WindowsISO, Name: "Windows 11 24H2", Arch: "x64", BIOS: true, UEFI: true,
				Editions: []string{"Windows 11 Home", "Windows 11 Pro"}, Language: "en-US"},
		},
		{
			name: "windows server with a utf-16 declaration",
			im:   func(t *testing.T) *image { return windowsImage(wimFile(t, "server-2022.xml")) },
			want: SourceInfo{Kind: WindowsISO, Name: "Windows Server 2022", Arch: "x64", BIOS: true, UEFI: true,
				Editions: []string{"Windows Server 2022 Standard Evaluation"}, Language: "de-DE"},
		},
		{
			name: "arm64, unmapped build, name without display name",
			im:   func(t *testing.T) *image { return windowsImage(wimFile(t, "arm64-unmapped.xml")) },
			want: SourceInfo{Kind: WindowsISO, Name: "Windows 11 (build 22621)", Arch: "ARM64", BIOS: true, UEFI: true,
				Editions: []string{"Windows 11 Pro"}},
		},
		{
			name: "external entity is not resolved",
			im:   func(t *testing.T) *image { return windowsImage(wimFile(t, "entity.xml")) },
			want: SourceInfo{Kind: WindowsISO, BIOS: true, UEFI: true},
		},
		{
			name: "xml resource past the end of the wim",
			im: func(t *testing.T) *image {
				wim := wimFile(t, "win11-24h2.xml")
				binary.LittleEndian.PutUint64(wim[wimXMLOffset+8:], 1<<40)
				return windowsImage(wim)
			},
			want: SourceInfo{Kind: WindowsISO, BIOS: true, UEFI: true},
		},
		{
			name: "xml resource over the cap",
			im: func(t *testing.T) *image {
				wim := wimFile(t, "win11-24h2.xml")
				binary.LittleEndian.PutUint64(wim[wimXMLOffset:], maxWIMXML+2)
				return windowsImage(wim)
			},
			want: SourceInfo{Kind: WindowsISO, BIOS: true, UEFI: true},
		},
		{
			name: "compressed xml resource",
			im: func(t *testing.T) *image {
				wim := wimFile(t, "win11-24h2.xml")
				wim[wimXMLOffset+7] = wimResourceCompressed
				return windowsImage(wim)
			},
			want: SourceInfo{Kind: WindowsISO, BIOS: true, UEFI: true},
		},
		{
			name: "not a wim",
			im: func(t *testing.T) *image {
				wim := wimFile(t, "win11-24h2.xml")
				wim[0] = 'X'
				return windowsImage(wim)
			},
			want: SourceInfo{Kind: WindowsISO, BIOS: true, UEFI: true},
		},
		{
			name: "truncated wim",
			im:   func(t *testing.T) *image { return windowsImage(wimFile(t, "win11-24h2.xml")[:100]) },
			want: SourceInfo{Kind: WindowsISO, BIOS: true, UEFI: true},
		},
		{
			name: "udf wim across extents",
			im: func(t *testing.T) *image {
				return windowsUDFWithWIM(wimFile(t, "win11-24h2.xml"))
			},
			want: SourceInfo{Kind: WindowsISO, Name: "Windows 11 24H2", Arch: "x64",
				Editions: []string{"Windows 11 Home", "Windows 11 Pro"}, Language: "en-US"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := probeImage(t, tt.im(t))
			if got.Kind != tt.want.Kind || got.Reason != "" {
				t.Fatalf("kind %s reason %q", got.Kind, got.Reason)
			}
			if got.Name != tt.want.Name || got.Arch != tt.want.Arch || got.BIOS != tt.want.BIOS ||
				got.UEFI != tt.want.UEFI || got.Language != tt.want.Language || !slices.Equal(got.Editions, tt.want.Editions) {
				t.Fatalf("got  %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

// windowsUDFWithWIM is windowsUDF with the WIM's bytes on the image, split
// over two short allocation descriptors that are not contiguous on the image.
func windowsUDFWithWIM(wim []byte) *image {
	im := windowsUDF(uint64(len(wim)))
	const part = 300
	split := sectorSize / 2
	first, second := 10, 20
	copy(im.sector(part+first), wim[:split])
	for i := 0; split+i*sectorSize < len(wim); i++ {
		copy(im.sector(part+second+i), wim[split+i*sectorSize:])
	}
	fe := im.sector(part + 4)
	binary.LittleEndian.PutUint32(fe[212:216], 16)
	binary.LittleEndian.PutUint32(fe[216:220], uint32(split))
	binary.LittleEndian.PutUint32(fe[220:224], uint32(first))
	binary.LittleEndian.PutUint32(fe[224:228], uint32(len(wim)-split))
	binary.LittleEndian.PutUint32(fe[228:232], uint32(second))
	return im
}

func TestFile_ReadAt(t *testing.T) {
	img := []byte("0123456789abcdefghij")
	f := &file{r: bytes.NewReader(img), image: int64(len(img)), size: 9,
		extents: []extent{{off: 2, n: 3}, {off: -1, n: 2}, {off: 15, n: 5}}}

	got := make([]byte, 9)
	if n, err := f.ReadAt(got, 0); n != 9 || err != nil {
		t.Fatalf("ReadAt = %d, %v", n, err)
	}
	if want := "234\x00\x00fghi"; string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	got = make([]byte, 4)
	if n, err := f.ReadAt(got, 7); n != 2 || err != io.EOF || string(got[:n]) != "hi" {
		t.Fatalf("ReadAt past size = %d %q, %v", n, got[:n], err)
	}

	short := &file{r: bytes.NewReader(img), image: int64(len(img)), size: 10, extents: []extent{{off: 0, n: 4}}}
	if _, err := short.ReadAt(make([]byte, 10), 0); err != errShortFile {
		t.Fatalf("short extents: %v", err)
	}

	outside := &file{r: bytes.NewReader(img), image: int64(len(img)), size: 4, extents: []extent{{off: 18, n: 4}}}
	if _, err := outside.ReadAt(make([]byte, 4), 0); err == nil {
		t.Fatal("extent past the image read without error")
	}
}
