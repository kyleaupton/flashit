// Package isofs reads the UDF file system of a Windows ISO in process, so
// no host needs to mount an untrusted image. It is the only package that
// imports golift.io/udf.
//
// Open walks and checks the whole tree before returning. Everything the
// copy step later writes to the stick is named by this index, so the checks
// here are what keeps a crafted image from steering writes, hanging the app
// or copying anything but plain files.
package isofs

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"golift.io/udf"
)

// Limits on what Open accepts. Win11 23H2 has 946 files, 10 levels deep.
// They are variables so tests can lower them.
var (
	maxEntries = 100_000
	maxDepth   = 32
	// maxMetadata bounds the directory listings and file entry tails
	// golift reads, so one image cannot make Open read or hold gigabytes.
	maxMetadata int64 = 64 << 20
	// maxExtents bounds the allocation descriptors across all files; each
	// becomes a retained run in the file's reader.
	maxExtents = 1 << 20
)

// ECMA-167 4/14.6.6 file types.
const (
	typeDirectory = 4
	typeFile      = 5
)

const fidDirectory = 0x02

// Image is an opened ISO. It implements fs.FS, fs.ReadDirFS and fs.StatFS.
type Image struct {
	c     io.Closer
	nodes map[string]*node
}

type node struct {
	name string
	dir  bool
	size int64
	mod  time.Time
	kids []*node
	data *io.SectionReader
}

// Open reads and checks the UDF tree of the image at path.
func Open(name string) (*Image, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	im, err := newImage(f, st.Size(), f)
	if err != nil {
		f.Close()
		return nil, err
	}
	return im, nil
}

func newImage(r io.ReaderAt, size int64, c io.Closer) (im *Image, err error) {
	defer recoverTo(&err)

	icb, err := rootICB(r, size)
	if err != nil {
		return nil, fmt.Errorf("image has no readable UDF file system: %w", err)
	}
	u, err := udf.NewUdfFromReader(io.NewSectionReader(r, 0, size))
	if err != nil {
		return nil, fmt.Errorf("image has no readable UDF file system: %w", err)
	}

	im = &Image{c: c, nodes: map[string]*node{}}
	root := &node{name: ".", dir: true}
	im.nodes["."] = root
	w := &walker{image: im, seen: map[fileRef]string{{icb.Partition, icb.Location}: "the root directory"}}

	top := &udf.File{Udf: u, Fid: &udf.FileIdentifierDescriptor{ICB: icb, FileCharacteristics: fidDirectory}}
	fe, err := top.FileEntry()
	if err != nil {
		return nil, fmt.Errorf("image root directory is unreadable: %w", err)
	}
	if err := w.checkEntry("the root directory", fe); err != nil {
		return nil, err
	}
	if fe.ICBTag.FileType != typeDirectory {
		return nil, errors.New("image root is not a directory")
	}
	root.mod = fe.ModificationTime
	if w.metadata += int64(fe.InformationLength); w.metadata > maxMetadata {
		return nil, errors.New("image root directory is implausibly large")
	}
	list, err := top.ReadDir()
	if err != nil {
		return nil, fmt.Errorf("image root directory is unreadable: %w", err)
	}
	if err := w.dir(root, ".", list); err != nil {
		return nil, err
	}
	return im, nil
}

type fileRef struct {
	part  uint16
	block uint64
}

type walker struct {
	image    *Image
	seen     map[fileRef]string
	entries  int
	metadata int64
	extents  int
}

func (w *walker) dir(parent *node, dirPath string, list []udf.File) error {
	if w.entries += len(list); w.entries > maxEntries {
		return fmt.Errorf("image has more than %d files and directories", maxEntries)
	}
	folded := map[string]string{}
	var subdirs []int
	for i := range list {
		f := &list[i]
		name := f.Name()
		if err := checkName(name); err != nil {
			return fmt.Errorf("image has a file named %q%s: %s", name, where(dirPath), err)
		}
		key := foldName(name)
		if other, ok := folded[key]; ok {
			return fmt.Errorf("image has both %q and %q%s, which FAT32 cannot tell apart", other, name, where(dirPath))
		}
		folded[key] = name

		p := path.Join(dirPath, name)
		if !fs.ValidPath(p) {
			return fmt.Errorf("image has an invalid path %q", p)
		}
		if strings.Count(p, "/") >= maxDepth {
			return fmt.Errorf("image nests %s more than %d levels deep", p, maxDepth)
		}

		ref := fileRef{f.Fid.ICB.Partition, f.Fid.ICB.Location}
		if first, ok := w.seen[ref]; ok {
			return fmt.Errorf("image lists the same file as %s and %s", first, p)
		}
		w.seen[ref] = p

		fe, err := f.FileEntry()
		if err != nil {
			return fmt.Errorf("image entry %s is unreadable: %w", p, err)
		}
		if err := w.checkEntry(p, fe); err != nil {
			return err
		}
		isDir := fe.ICBTag.FileType == typeDirectory
		if isDir != (f.Fid.FileCharacteristics&fidDirectory != 0) {
			return fmt.Errorf("image disagrees with itself about whether %s is a directory", p)
		}

		n := &node{name: name, dir: isDir, mod: fe.ModificationTime}
		if isDir {
			if w.metadata += int64(fe.InformationLength); w.metadata > maxMetadata {
				return fmt.Errorf("image directories are implausibly large at %s", p)
			}
			subdirs = append(subdirs, i)
		} else {
			if err := w.file(n, p, f, fe); err != nil {
				return err
			}
		}
		parent.kids = append(parent.kids, n)
		w.image.nodes[p] = n
	}
	slices.SortFunc(parent.kids, func(a, b *node) int { return strings.Compare(a.name, b.name) })

	for _, i := range subdirs {
		f := &list[i]
		p := path.Join(dirPath, f.Name())
		kids, err := f.ReadDir()
		if err != nil {
			return fmt.Errorf("image directory %s is unreadable: %w", p, err)
		}
		if err := w.dir(w.image.nodes[p], p, kids); err != nil {
			return err
		}
	}
	return nil
}

func (w *walker) checkEntry(p string, fe *udf.FileEntry) error {
	if fe.ICBTag == nil {
		return fmt.Errorf("image entry %s has no ICB tag", p)
	}
	switch t := fe.ICBTag.FileType; t {
	case typeDirectory, typeFile:
	case 12:
		return fmt.Errorf("image has a symbolic link at %s", p)
	case 6, 7:
		return fmt.Errorf("image has a device node at %s", p)
	default:
		return fmt.Errorf("image has %s, which is UDF file type %d, not a file or directory", p, t)
	}
	if w.metadata += int64(fe.LengthOfExtendedAttributes) + int64(fe.LengthOfAllocationDescriptors); w.metadata > maxMetadata {
		return fmt.Errorf("image file entries are implausibly large at %s", p)
	}
	if w.extents += len(fe.AllocationDescriptors); w.extents > maxExtents {
		return fmt.Errorf("image is implausibly fragmented at %s", p)
	}
	// A chain can fan out into further chains that golift follows before
	// any limit here sees them. oscdimg never writes one.
	for _, ad := range fe.AllocationDescriptors {
		if ad.ExtentType() == udf.ExtentNextDescriptors {
			return fmt.Errorf("image uses chained allocation descriptors for %s", p)
		}
	}
	return nil
}

// file records a regular file after proving its reader yields exactly the
// size its entry claims: the end of every recorded extent and the last byte
// must be readable, so a short or truncated file fails here and not half
// way through the copy.
func (w *walker) file(n *node, p string, f *udf.File, fe *udf.FileEntry) error {
	r, err := f.NewReader()
	if err != nil {
		return fmt.Errorf("image file %s is unreadable: %w", p, err)
	}
	if fe.InformationLength > 1<<62 || r.Size() != int64(fe.InformationLength) {
		return fmt.Errorf("image says %s is %d bytes but holds %d", p, fe.InformationLength, r.Size())
	}
	size := r.Size()
	var ends []int64
	var off int64
	for _, ad := range fe.AllocationDescriptors {
		off += int64(ad.DataLength())
		if ad.ExtentType() == udf.ExtentRecorded && ad.DataLength() > 0 {
			ends = append(ends, min(off, size)-1)
		}
	}
	if size > 0 {
		ends = append(ends, size-1)
	}
	var b [1]byte
	for _, end := range ends {
		if end < 0 {
			continue
		}
		if n, err := r.ReadAt(b[:], end); n != 1 {
			return fmt.Errorf("image says %s is %d bytes but its data ends early: %w", p, size, err)
		}
	}
	n.size = size
	n.data = r
	return nil
}

func where(dir string) string {
	if dir == "." {
		return ""
	}
	return " in " + dir
}

// checkName rejects names that could escape the target directory or that
// FAT32 would store differently from how the image lists them.
func checkName(name string) error {
	switch {
	case name == "":
		return errors.New("the name is empty")
	case name == "." || name == "..":
		return errors.New("the name is a path element")
	case !utf8.ValidString(name):
		return errors.New("the name is not valid text")
	case len(utf16.Encode([]rune(name))) > 255:
		return errors.New("the name is too long for FAT32")
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return errors.New("the name contains a control character")
		}
		if strings.ContainsRune(`/\<>:"|?*`, r) {
			return fmt.Errorf("FAT32 cannot store %q", r)
		}
	}
	// Windows drops trailing dots and spaces, so "a." would land on "a".
	if strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		return errors.New("the name ends in a dot or space")
	}
	if isDeviceName(name) {
		return errors.New("the name is a Windows device")
	}
	return nil
}

// isDeviceName reports whether Windows would open a device instead of a
// file for this name, with or without an extension.
func isDeviceName(name string) bool {
	base, _, _ := strings.Cut(name, ".")
	base = strings.ToUpper(strings.TrimRight(base, " "))
	switch base {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return true
	}
	if prefix, suffix := base[:min(3, len(base))], base[min(3, len(base)):]; prefix == "COM" || prefix == "LPT" {
		return len(suffix) == 1 && suffix[0] >= '0' && suffix[0] <= '9' ||
			suffix == "¹" || suffix == "²" || suffix == "³"
	}
	return false
}

// foldName is the key under which FAT32 considers two names the same.
func foldName(name string) string {
	return strings.ToUpper(strings.ToLower(name))
}

func recoverTo(err *error) {
	if r := recover(); r != nil {
		*err = fmt.Errorf("image could not be read: %v", r)
	}
}

// Close releases the image file. Files opened from it stop working.
func (im *Image) Close() error { return im.c.Close() }

func (im *Image) lookup(op, name string) (*node, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: op, Path: name, Err: fs.ErrInvalid}
	}
	n, ok := im.nodes[name]
	if !ok {
		return nil, &fs.PathError{Op: op, Path: name, Err: fs.ErrNotExist}
	}
	return n, nil
}

// Open opens a file or directory by its exact name.
func (im *Image) Open(name string) (fs.File, error) {
	n, err := im.lookup("open", name)
	if err != nil {
		return nil, err
	}
	if n.dir {
		return &dirHandle{n: n}, nil
	}
	return &fileHandle{n: n, r: io.NewSectionReader(n.data, 0, n.size)}, nil
}

func (im *Image) Stat(name string) (fs.FileInfo, error) {
	n, err := im.lookup("stat", name)
	if err != nil {
		return nil, err
	}
	return info{n}, nil
}

func (im *Image) ReadDir(name string) ([]fs.DirEntry, error) {
	n, err := im.lookup("readdir", name)
	if err != nil {
		return nil, err
	}
	if !n.dir {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: errors.New("not a directory")}
	}
	return entries(n.kids), nil
}

func entries(kids []*node) []fs.DirEntry {
	out := make([]fs.DirEntry, len(kids))
	for i, k := range kids {
		out[i] = fs.FileInfoToDirEntry(info{k})
	}
	return out
}

type info struct{ n *node }

func (i info) Name() string       { return i.n.name }
func (i info) Size() int64        { return i.n.size }
func (i info) ModTime() time.Time { return i.n.mod }
func (i info) IsDir() bool        { return i.n.dir }
func (i info) Sys() any           { return nil }
func (i info) Mode() fs.FileMode {
	if i.n.dir {
		return fs.ModeDir | 0o555
	}
	return 0o444
}

// fileHandle is an open regular file. It implements io.ReaderAt and
// io.Seeker as well as fs.File.
type fileHandle struct {
	n      *node
	r      *io.SectionReader
	closed bool
}

func (h *fileHandle) Stat() (fs.FileInfo, error) { return info{h.n}, nil }

func (h *fileHandle) Read(p []byte) (n int, err error) {
	if h.closed {
		return 0, fs.ErrClosed
	}
	defer recoverTo(&err)
	return h.r.Read(p)
}

func (h *fileHandle) ReadAt(p []byte, off int64) (n int, err error) {
	if h.closed {
		return 0, fs.ErrClosed
	}
	defer recoverTo(&err)
	return h.r.ReadAt(p, off)
}

func (h *fileHandle) Seek(offset int64, whence int) (int64, error) {
	if h.closed {
		return 0, fs.ErrClosed
	}
	return h.r.Seek(offset, whence)
}

func (h *fileHandle) Close() error {
	if h.closed {
		return fs.ErrClosed
	}
	h.closed = true
	return nil
}

type dirHandle struct {
	n   *node
	pos int
}

func (d *dirHandle) Stat() (fs.FileInfo, error) { return info{d.n}, nil }

func (d *dirHandle) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.n.name, Err: errors.New("is a directory")}
}

func (d *dirHandle) Close() error { return nil }

func (d *dirHandle) ReadDir(count int) ([]fs.DirEntry, error) {
	rest := d.n.kids[d.pos:]
	if count <= 0 {
		d.pos = len(d.n.kids)
		return entries(rest), nil
	}
	if len(rest) == 0 {
		return nil, io.EOF
	}
	if count > len(rest) {
		count = len(rest)
	}
	d.pos += count
	return entries(rest[:count]), nil
}
