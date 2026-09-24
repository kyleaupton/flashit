// Package isocompare checks isofs against the host's own ISO mount. It backs
// cmd/isotest and the env-gated corpus tests; nothing in the app uses it.
package isocompare

import (
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"slices"
)

type entry struct {
	dir  bool
	size int64
	sum  [sha256.Size]byte
}

// Summary is what one tree held.
type Summary struct {
	Files, Dirs int
	Bytes       int64
}

// Trees walks both trees, hashing every file, and returns one line per path
// whose presence, kind, size or SHA-256 differs.
func Trees(a, b fs.FS) (Summary, []string, error) {
	type result struct {
		m   map[string]entry
		err error
	}
	ch := make(chan result, 1)
	go func() {
		m, err := hashTree(b)
		ch <- result{m, err}
	}()
	ma, err := hashTree(a)
	rb := <-ch
	if err != nil {
		return Summary{}, nil, err
	}
	if rb.err != nil {
		return Summary{}, nil, rb.err
	}
	mb := rb.m

	var sum Summary
	for _, e := range ma {
		if e.dir {
			sum.Dirs++
		} else {
			sum.Files++
			sum.Bytes += e.size
		}
	}
	var diffs []string
	for p, ea := range ma {
		eb, ok := mb[p]
		switch {
		case !ok:
			diffs = append(diffs, "only in first: "+p)
		case ea.dir != eb.dir:
			diffs = append(diffs, "kind differs: "+p)
		case ea.size != eb.size:
			diffs = append(diffs, fmt.Sprintf("size differs: %s (%d vs %d)", p, ea.size, eb.size))
		case ea.sum != eb.sum:
			diffs = append(diffs, "SHA-256 differs: "+p)
		}
	}
	for p := range mb {
		if _, ok := ma[p]; !ok {
			diffs = append(diffs, "only in second: "+p)
		}
	}
	slices.Sort(diffs)
	return sum, diffs, nil
}

func hashTree(fsys fs.FS) (map[string]entry, error) {
	m := map[string]entry{}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == "." {
			return nil
		}
		if d.IsDir() {
			m[p] = entry{dir: true}
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file", p)
		}
		f, err := fsys.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		h := sha256.New()
		n, err := io.Copy(h, f)
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		e := entry{size: n}
		h.Sum(e.sum[:0])
		if info, err := d.Info(); err != nil || info.Size() != n {
			return fmt.Errorf("%s: stat size disagrees with the %d bytes read", p, n)
		}
		m[p] = e
		return nil
	})
	return m, err
}
