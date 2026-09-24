package isocompare

import (
	"reflect"
	"testing"
	"testing/fstest"
)

func TestTrees(t *testing.T) {
	a := fstest.MapFS{
		"same":      {Data: []byte("x")},
		"size":      {Data: []byte("xy")},
		"sum":       {Data: []byte("ab")},
		"onlyA":     {Data: nil},
		"kind":      {Data: nil},
		"dir/inner": {Data: []byte("1")},
	}
	b := fstest.MapFS{
		"same":      {Data: []byte("x")},
		"size":      {Data: []byte("x")},
		"sum":       {Data: []byte("ba")},
		"onlyB":     {Data: nil},
		"kind/x":    {Data: nil},
		"dir/inner": {Data: []byte("1")},
	}
	sum, diffs, err := Trees(a, b)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"SHA-256 differs: sum",
		"kind differs: kind",
		"only in first: onlyA",
		"only in second: kind/x",
		"only in second: onlyB",
		"size differs: size (2 vs 1)",
	}
	if !reflect.DeepEqual(diffs, want) {
		t.Fatalf("got %q", diffs)
	}
	if sum.Files != 6 || sum.Dirs != 1 {
		t.Fatalf("summary %+v", sum)
	}
}
