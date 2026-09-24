package isofs

import (
	"bytes"
	"io"
	"io/fs"
	"testing"
	"time"
)

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

func FuzzOpen(f *testing.F) {
	shared := file("a", "x")
	for _, root := range []*tnode{
		windowsTree(),
		dir("", file("..", "x")),
		dir("", shared, &tnode{name: "b", same: shared}),
		dir("", &tnode{name: "f", data: pattern(3000), sizeLie: 1}),
		dir("", &tnode{name: "f", data: []byte("x"), chained: true}),
		dir("", dir("d", dir("e", &tnode{name: "f", data: pattern(9000), extents: []int{4096, 4096, 808}}))),
	} {
		f.Add(build(root))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		done := make(chan struct{})
		go func() {
			defer close(done)
			walkAll(t, data)
		}()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatalf("Open and walk ran longer than 3s on a %d byte image", len(data))
		}
	})
}

// walkAll opens the image and, if Open accepted it, reads every file up to
// 1 MiB. An accepted file must read without error: Open has already checked
// that its data is all there.
func walkAll(t *testing.T, data []byte) {
	im, err := newImage(bytes.NewReader(data), int64(len(data)), nopCloser{})
	if err != nil {
		return
	}
	err = fs.WalkDir(im, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		f, err := im.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		st, _ := f.Stat()
		want := min(st.Size(), 1<<20)
		n, err := io.Copy(io.Discard, io.LimitReader(f, 1<<20))
		if err != nil || n != want {
			t.Errorf("%s: read %d of %d bytes: %v", p, n, want, err)
		}
		return nil
	})
	if err != nil {
		t.Errorf("walk: %v", err)
	}
}
