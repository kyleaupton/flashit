package sources

import (
	"errors"
	"io"
)

// extent is a run of a file's bytes in the image. A negative off is a
// run the file system recorded as unwritten, which reads as zeros.
type extent struct {
	off, n int64
}

// file is one file found by a lookup: its size and where its bytes are.
type file struct {
	r       io.ReaderAt
	image   int64 // size of the image the extents point into
	size    int64
	extents []extent
	inline  []byte // UDF files small enough to live in their file entry
	// err is why the extents could not be worked out; a lookup that only
	// needs the size still succeeds.
	err error
}

var errShortFile = errors.New("file extents end before its recorded size")

// ReadAt reads the file's bytes at off, stopping at its recorded size.
func (f *file) ReadAt(p []byte, off int64) (int, error) {
	if f.err != nil {
		return 0, f.err
	}
	if off < 0 {
		return 0, errors.New("negative offset")
	}
	if off >= f.size {
		return 0, io.EOF
	}
	if rem := f.size - off; int64(len(p)) > rem {
		p = p[:rem]
		n, err := f.readAt(p, off)
		if err == nil {
			err = io.EOF
		}
		return n, err
	}
	return f.readAt(p, off)
}

func (f *file) readAt(p []byte, off int64) (int, error) {
	if f.inline != nil {
		if off >= int64(len(f.inline)) {
			return 0, errShortFile
		}
		n := copy(p, f.inline[off:])
		if n < len(p) {
			return n, errShortFile
		}
		return n, nil
	}
	done := 0
	var pos int64
	for _, e := range f.extents {
		if done == len(p) {
			break
		}
		want := off + int64(done)
		if want >= pos+e.n {
			pos += e.n
			continue
		}
		within := want - pos
		chunk := p[done:]
		if int64(len(chunk)) > e.n-within {
			chunk = chunk[:e.n-within]
		}
		if e.off >= 0 && e.off+e.n > f.image {
			return done, errors.New("file extent lies past the end of the image")
		}
		if e.off < 0 {
			clear(chunk)
		} else if _, err := f.r.ReadAt(chunk, e.off+within); err != nil {
			return done, err
		}
		done += len(chunk)
		pos += e.n
	}
	if done < len(p) {
		return done, errShortFile
	}
	return done, nil
}

// readHead reads at most max bytes from the start of the file.
func (f *file) readHead(max int64) ([]byte, error) {
	n := min(f.size, max)
	buf := make([]byte, n)
	got, err := f.ReadAt(buf, 0)
	if err == io.EOF && int64(got) == n {
		err = nil
	}
	return buf[:got], err
}
