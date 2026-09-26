package sources

import (
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// What the probe reads beyond the directory tree is shown to the user and
// never decides anything, so every reader here gives up quietly: a failure
// leaves fields empty and never changes Kind or Reason.
const (
	maxDiskInfo = 4 << 10
	maxWIMXML   = 4 << 20
	maxEditions = 64
	maxText     = 128 // runes kept of any name read from the image
)

const (
	platformBIOS = 0x00
	platformUEFI = 0xEF

	entryBootable     = 0x88
	entryNotBootable  = 0x00
	entryHeader       = 0x90
	entryFinalHeader  = 0x91
	entryExtension    = 0x44
	catalogEntrySize  = 32
	validationEntryID = 0x01
)

// bootModes reads the El Torito boot catalog: the validation entry's
// platform covers the default entry, and each section header names the
// platform of the entries after it.
func (v *volume) bootModes() (bios, uefi bool) {
	if v.catalog == 0 {
		return false, false
	}
	off := int64(v.catalog) * v.blockSize
	if off+sectorSize > v.size {
		return false, false
	}
	cat := make([]byte, sectorSize)
	if _, err := v.r.ReadAt(cat, off); err != nil {
		return false, false
	}
	if cat[0] != validationEntryID || cat[30] != 0x55 || cat[31] != 0xAA {
		return false, false
	}
	var sum uint16
	for i := 0; i < catalogEntrySize; i += 2 {
		sum += binary.LittleEndian.Uint16(cat[i:])
	}
	if sum != 0 {
		return false, false
	}

	mark := func(platform byte) {
		switch platform {
		case platformBIOS:
			bios = true
		case platformUEFI:
			uefi = true
		}
	}
	platform := cat[1]
	if cat[catalogEntrySize] == entryBootable {
		mark(platform)
	}
	final := false
	for pos := 2 * catalogEntrySize; pos+catalogEntrySize <= len(cat); pos += catalogEntrySize {
		e := cat[pos : pos+catalogEntrySize]
		switch e[0] {
		case entryHeader, entryFinalHeader:
			if final {
				return bios, uefi
			}
			platform = e[1]
			final = e[0] == entryFinalHeader
		case entryBootable:
			mark(platform)
		case entryNotBootable:
			if final && bytes.Equal(e, make([]byte, catalogEntrySize)) {
				return bios, uefi
			}
		case entryExtension:
		default:
			return bios, uefi
		}
	}
	return bios, uefi
}

// efiLoaders maps the removable-media loader names UEFI firmware looks for
// to the architecture they boot, most specific first.
var efiLoaders = []struct{ name, arch string }{
	{"BOOTX64.EFI", "x86-64"},
	{"BOOTAA64.EFI", "ARM64"},
	{"BOOTRISCV64.EFI", "RISC-V"},
	{"BOOTIA32.EFI", "x86"},
}

func linuxMeta(vol *volume, info *SourceInfo) {
	for _, l := range efiLoaders {
		if _, found, err := vol.lookup("EFI", "BOOT", l.name); err == nil && found {
			info.Arch = l.arch
			break
		}
	}
	f, found, err := vol.lookup(".disk", "info")
	if err != nil || !found {
		return
	}
	b, err := f.readHead(maxDiskInfo)
	if err != nil {
		return
	}
	line, _, _ := strings.Cut(string(b), "\n")
	// "Ubuntu 24.04.1 LTS "Noble Numbat" - Release amd64 (20240827)"
	name, _, _ := strings.Cut(line, " - ")
	info.Name = cleanText(name)
}

// wimHeader is the start of a WIM (and ESD) file; only the XML resource's
// location is read from it.
const (
	wimHeaderSize = 208
	wimXMLOffset  = 72

	wimResourceCompressed = 0x04
)

var wimMagic = []byte("MSWIM\x00\x00\x00")

type wimXML struct {
	Images []struct {
		Name        string `xml:"NAME"`
		DisplayName string `xml:"DISPLAYNAME"`
		Windows     struct {
			Arch             string `xml:"ARCH"`
			InstallationType string `xml:"INSTALLATIONTYPE"`
			Languages        struct {
				Default string `xml:"DEFAULT"`
			} `xml:"LANGUAGES"`
			Version struct {
				Build string `xml:"BUILD"`
			} `xml:"VERSION"`
		} `xml:"WINDOWS"`
	} `xml:"IMAGE"`
}

func windowsMeta(wim *file, info *SourceInfo) {
	doc, err := readWIMXML(wim)
	if err != nil || len(doc.Images) == 0 {
		return
	}
	for _, im := range doc.Images {
		if len(info.Editions) == maxEditions {
			break
		}
		name := cleanText(im.DisplayName)
		if name == "" {
			name = cleanText(im.Name)
		}
		if name != "" {
			info.Editions = append(info.Editions, name)
		}
	}
	first := doc.Images[0].Windows
	switch strings.TrimSpace(first.Arch) {
	case "0":
		info.Arch = "x86"
	case "9":
		info.Arch = "x64"
	case "12":
		info.Arch = "ARM64"
	}
	info.Language = cleanText(first.Languages.Default)
	build, err := strconv.Atoi(strings.TrimSpace(first.Version.Build))
	if err != nil || build <= 0 {
		return
	}
	info.Name = windowsName(build, strings.HasPrefix(strings.TrimSpace(first.InstallationType), "Server"))
}

var (
	// 22621 is left out: Microsoft's 23H2 media carries the 22H2 base build
	// in the WIM, as its 22H2 media for Windows 10 carries 19041.
	clientReleases = map[int]string{19045: "22H2", 22631: "23H2", 26100: "24H2", 26200: "25H2"}
	serverReleases = map[int]string{17763: "2019", 20348: "2022", 26100: "2025"}
)

func windowsName(build int, server bool) string {
	name, releases := "Windows 10", clientReleases
	switch {
	case server:
		name, releases = "Windows Server", serverReleases
	case build >= 22000:
		name = "Windows 11"
	}
	if r, ok := releases[build]; ok {
		return name + " " + r
	}
	return fmt.Sprintf("%s (build %d)", name, build)
}

func readWIMXML(wim *file) (*wimXML, error) {
	hdr := make([]byte, wimHeaderSize)
	if _, err := wim.ReadAt(hdr, 0); err != nil {
		return nil, err
	}
	if !bytes.Equal(hdr[:8], wimMagic) {
		return nil, errors.New("not a WIM")
	}
	res := hdr[wimXMLOffset : wimXMLOffset+24]
	if res[7]&wimResourceCompressed != 0 {
		return nil, errors.New("WIM XML resource is compressed")
	}
	size := int64(binary.LittleEndian.Uint64(res[0:8]) & (1<<56 - 1))
	off := int64(binary.LittleEndian.Uint64(res[8:16]))
	if size < 2 || size > maxWIMXML || off < wimHeaderSize || off > wim.size-size {
		return nil, errors.New("WIM XML resource out of range")
	}
	raw := make([]byte, size)
	if _, err := wim.ReadAt(raw, off); err != nil && err != io.EOF {
		return nil, err
	}

	u := make([]uint16, len(raw)/2)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(raw[2*i:])
	}
	if len(u) > 0 && u[0] == 0xFEFF {
		u = u[1:]
	}
	var doc wimXML
	dec := xml.NewDecoder(strings.NewReader(string(utf16.Decode(u))))
	// The text is already decoded; a declaration naming UTF-16 must not
	// send the decoder looking for a charset reader.
	dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

// cleanText makes a string read from the image fit to show: valid UTF-8,
// no control characters, trimmed and capped.
func cleanText(s string) string {
	s = strings.ToValidUTF8(s, "")
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > maxText {
		s = strings.TrimSpace(string([]rune(s)[:maxText]))
	}
	return s
}
