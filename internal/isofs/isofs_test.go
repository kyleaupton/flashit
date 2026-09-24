package isofs

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

func pattern(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*7 + i/251)
	}
	return b
}

// windowsTree is a small stand-in for a Windows ISO: an embedded file at
// the top, install.wim split over three extents, and an empty file.
func windowsTree() *tnode {
	wim := &tnode{name: "install.wim", data: pattern(5000), extents: []int{2048, 2048, 904}}
	return dir("",
		&tnode{name: "README.TXT", data: []byte("hello"), embed: true},
		dir("sources", wim, file("boot.wim", "boot"), dir("en-us", file("empty.dat", ""))),
		file("setup.exe", "MZ"),
	)
}

func openTree(t *testing.T, root *tnode) (*Image, error) {
	t.Helper()
	im, err := Open(writeImage(t, build(root)))
	if err == nil {
		t.Cleanup(func() { im.Close() })
	}
	return im, err
}

func TestOpen_Tree(t *testing.T) {
	im, err := openTree(t, windowsTree())
	if err != nil {
		t.Fatal(err)
	}
	if err := fstest.TestFS(im, "README.TXT", "setup.exe", "sources/install.wim", "sources/boot.wim", "sources/en-us/empty.dat"); err != nil {
		t.Fatal(err)
	}

	want := map[string][]byte{
		"README.TXT":              []byte("hello"),
		"setup.exe":               []byte("MZ"),
		"sources/install.wim":     pattern(5000),
		"sources/boot.wim":        []byte("boot"),
		"sources/en-us/empty.dat": {},
	}
	for name, data := range want {
		got, err := fs.ReadFile(im, name)
		if err != nil || !bytes.Equal(got, data) {
			t.Errorf("%s: got %d bytes, err %v", name, len(got), err)
		}
		st, err := fs.Stat(im, name)
		if err != nil || st.Size() != int64(len(data)) {
			t.Errorf("%s: stat %v, %v", name, st, err)
		}
	}
}

func TestOpen_ReaderAtAndSeeker(t *testing.T) {
	im, err := openTree(t, windowsTree())
	if err != nil {
		t.Fatal(err)
	}
	f, err := im.Open("sources/install.wim")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	ra, ok := f.(io.ReaderAt)
	if !ok {
		t.Fatal("file is not an io.ReaderAt")
	}
	// A read across the boundary between the first and second extents.
	buf := make([]byte, 100)
	if _, err := ra.ReadAt(buf, 2000); err != nil || !bytes.Equal(buf, pattern(5000)[2000:2100]) {
		t.Fatalf("ReadAt: %v", err)
	}
	sk, ok := f.(io.Seeker)
	if !ok {
		t.Fatal("file is not an io.Seeker")
	}
	if _, err := sk.Seek(4990, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	rest, err := io.ReadAll(f)
	if err != nil || !bytes.Equal(rest, pattern(5000)[4990:]) {
		t.Fatalf("read after seek: %d bytes, %v", len(rest), err)
	}
}

func TestOpen_NotUDF(t *testing.T) {
	_, err := Open(writeImage(t, make([]byte, 300*bs)))
	if !errors.Is(err, ErrNoUDF) {
		t.Fatalf("got %v, want ErrNoUDF", err)
	}
}

func TestOpen_EmptyNameIsDropped(t *testing.T) {
	// golift skips identifiers whose name is empty or does not decode, so
	// such an entry never reaches checkName. Nothing is written for it.
	root := dir("", file("a", "x"), &tnode{name: "b", data: []byte("y"), rawName: []byte{}})
	im, err := openTree(t, root)
	if err != nil {
		t.Fatal(err)
	}
	ents, _ := im.ReadDir(".")
	if len(ents) != 1 || ents[0].Name() != "a" {
		t.Fatalf("got %v", ents)
	}
}

func TestOpen_Rejects(t *testing.T) {
	yes, no := true, false
	shared := file("a", "x")
	deep := dir("d")
	d := deep
	for range maxDepth {
		n := dir("d")
		d.kids = []*tnode{n}
		d = n
	}

	cases := []struct {
		name string
		root *tnode
		want string
	}{
		{"dotdot", dir("", file("..", "x")), `file named ".."`},
		{"dot", dir("", dir(".")), `file named "."`},
		{"slash", dir("", file("a/b", "x")), `file named "a/b"`},
		{"backslash", dir("", file(`..\evil`, "x")), `cannot store '\\'`},
		{"nul", dir("", file("a\x00b", "x")), "control character"},
		{"control", dir("", file("a\x1bb", "x")), "control character"},
		{"colon", dir("", dir("sources", file("C:x", "x"))), `named "C:x" in sources`},
		{"star", dir("", file("a*", "x")), "cannot store '*'"},
		{"question", dir("", file("a?", "x")), "cannot store '?'"},
		{"pipe", dir("", file("a|b", "x")), "cannot store '|'"},
		{"lt", dir("", file("a<b", "x")), "cannot store '<'"},
		{"quote", dir("", file(`a"b`, "x")), `cannot store '"'`},
		{"trailing dot", dir("", file("a.", "x")), "ends in a dot"},
		{"trailing space", dir("", file("a ", "x")), "ends in a dot or space"},
		{"device", dir("", file("nul.txt", "x")), "Windows device"},
		{"com port", dir("", file("COM1", "x")), "Windows device"},
		{"case", dir("", file("Setup.exe", "a"), file("SETUP.EXE", "b")), "FAT32 cannot tell apart"},
		{"case dirs", dir("", dir("sources"), dir("Sources")), "FAT32 cannot tell apart"},
		{"symlink", dir("", &tnode{name: "link", data: []byte("/etc/passwd"), fileType: 12}), "symbolic link at link"},
		{"block device", dir("", &tnode{name: "sda", fileType: 6}), "device node at sda"},
		{"char device", dir("", &tnode{name: "tty", fileType: 7}), "device node at tty"},
		{"fifo", dir("", &tnode{name: "pipe", fileType: 9}), "UDF file type 9"},
		{"stream dir", dir("", &tnode{name: "s", fileType: 13}), "UDF file type 13"},
		{"cycle to root", dir("", dir("loop", &tnode{name: "up", dir: true})), "same file"},
		{"hard link", dir("", shared, &tnode{name: "b", same: shared}), "same file as a and b"},
		{"too deep", dir("", deep), "levels deep"},
		{"size lie", dir("", &tnode{name: "f", data: pattern(3000), sizeLie: 1}), "data ends early"},
		{"size lie embedded", dir("", &tnode{name: "f", data: []byte("abc"), embed: true, sizeLie: 1}), "is 4 bytes but holds 3"},
		{"past end", dir("", &tnode{name: "f", data: pattern(3000), pastEnd: true}), "data ends early"},
		{"chained", dir("", &tnode{name: "f", data: []byte("x"), chained: true}), "chained allocation descriptors for f"},
		{"fid says dir", dir("", &tnode{name: "f", data: []byte("x"), fidDir: &yes}), "whether f is a directory"},
		{"fid says file", dir("", &tnode{name: "d", dir: true, fidDir: &no}), "whether d is a directory"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.name == "cycle to root" {
				// "up" points at the root's file entry.
				c.root.kids[0].kids[0].same = c.root
			}
			_, err := openTree(t, c.root)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want an error containing %q", err, c.want)
			}
			if !strings.HasPrefix(err.Error(), "image ") {
				t.Fatalf("error %q does not read like a probe reason", err)
			}
		})
	}
}

func TestOpen_TooManyEntries(t *testing.T) {
	defer func(n int) { maxEntries = n }(maxEntries)
	maxEntries = 4
	if _, err := openTree(t, dir("", file("a", ""), file("b", ""), dir("c", file("d", "")))); err != nil {
		t.Fatalf("four entries: %v", err)
	}
	_, err := openTree(t, dir("", file("a", ""), file("b", ""), dir("c", file("d", ""), file("e", ""))))
	if err == nil || !strings.Contains(err.Error(), "more than 4 files") {
		t.Fatalf("got %v", err)
	}
}

func TestOpen_DepthLimitIsInclusive(t *testing.T) {
	root := dir("")
	d := root
	for range maxDepth {
		n := dir("d")
		d.kids = []*tnode{n}
		d = n
	}
	if _, err := openTree(t, root); err != nil {
		t.Fatalf("%d levels: %v", maxDepth, err)
	}
}

func TestOpen_RejectsTwoPartitions(t *testing.T) {
	img := build(windowsTree())
	// A second partition descriptor where the terminator was, and the
	// terminator moved to the spare sector.
	copy(img[3*bs:4*bs], img[1*bs:2*bs])
	img[4*bs] = tagTerminating
	_, err := Open(writeImage(t, img))
	if err == nil || !strings.Contains(err.Error(), "partitions") {
		t.Fatalf("got %v", err)
	}
}

func TestCheckName(t *testing.T) {
	for _, ok := range []string{"install.wim", "boot", "BOOTX64.EFI", "en-us", "español", "a b", ".hidden", "CONSOLE", "COM10", "LPT"} {
		if err := checkName(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", ".", "..", "a\x7f", "\xff", "CON", "con.txt", "Aux", "LPT9.log", "COM¹", strings.Repeat("a", 256)} {
		if err := checkName(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestClosedFile(t *testing.T) {
	im, err := openTree(t, windowsTree())
	if err != nil {
		t.Fatal(err)
	}
	f, _ := im.Open("setup.exe")
	f.Close()
	if _, err := f.Read(make([]byte, 1)); !errors.Is(err, fs.ErrClosed) {
		t.Fatalf("read after close: %v", err)
	}
}
