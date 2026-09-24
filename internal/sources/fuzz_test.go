package sources

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// seedImages are the synthetic images the unit tests build, used as the
// fuzz corpus. FLASHIT_WRITE_FUZZ_SEEDS=1 go test -run TestFuzzSeeds
// regenerates testdata/fuzz from them.
func seedImages() map[string]*image {
	plain := newImage()
	plain.pvd("WIN_TEST", plain.tree(false, entry{name: "INSTALL.WIM;1", sizes: []uint32{123456}}))
	plain.terminator(17)

	joliet := newImage()
	joliet.pvd("PRIMARY", joliet.tree(false, entry{name: "INSTALL.WIM;1", sizes: []uint32{1}}))
	joliet.joliet(joliet.tree(true, entry{name: "install.wim", sizes: []uint32{2}}))
	joliet.terminator(18)

	multi := newImage()
	root := multi.tree(true, entry{name: "install.wim", sizes: []uint32{0xFFFFF800, 0xFFFFF800, 4096}})
	multi.pvd("BIG", root)
	multi.joliet(root)
	multi.terminator(18)

	hybrid := newImage()
	root = hybrid.alloc()
	hybrid.dir(root, root, root, nil)
	hybrid.pvd("alpine-virt 3.22.1 x86_64", root)
	hybrid.terminator(17)
	hybrid.mbr()

	data := newImage()
	root = data.alloc()
	data.dir(root, root, root, []entry{{name: "README.TXT;1", sizes: []uint32{10}}})
	data.pvd("DATA", root)
	data.terminator(17)

	return map[string]*image{
		"plain":   plain,
		"joliet":  joliet,
		"multi":   multi,
		"hybrid":  hybrid,
		"data":    data,
		"windows": windowsUDF(6017925238),
	}
}

func TestFuzzSeeds(t *testing.T) {
	if os.Getenv("FLASHIT_WRITE_FUZZ_SEEDS") == "" {
		t.Skip("set FLASHIT_WRITE_FUZZ_SEEDS=1 to regenerate testdata/fuzz")
	}
	for _, fuzz := range []string{"FuzzProbe", "FuzzUDF"} {
		dir := filepath.Join("testdata", "fuzz", fuzz)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, im := range seedImages() {
			corpus := fmt.Sprintf("go test fuzz v1\n[]byte(%q)\n", im.bytes())
			if err := os.WriteFile(filepath.Join(dir, name), []byte(corpus), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func FuzzProbe(f *testing.F) {
	for _, im := range seedImages() {
		f.Add(im.bytes())
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		probe(bytes.NewReader(data), int64(len(data)), "fuzz.iso")
	})
}

func FuzzUDF(f *testing.F) {
	for _, im := range seedImages() {
		f.Add(im.bytes())
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		v, err := readUDF(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return
		}
		v.lookup("sources", "install.wim")
	})
}
