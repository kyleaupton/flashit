package isofs

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// tnode describes one entry of a UDF image built by build. Only the fields
// a test sets differ from a plain file or directory.
type tnode struct {
	name     string
	dir      bool
	kids     []*tnode
	data     []byte
	extents  []int  // split data into these extent lengths, each on its own run of blocks
	embed    bool   // store data inside the file entry
	fileType byte   // ICB file type; 0 means directory or regular file
	sizeLie  int64  // added to the recorded information length
	fidDir   *bool  // override the FID's directory bit
	chained  bool   // add a next-extent allocation descriptor
	same     *tnode // point this FID at another node's file entry
	rawName  []byte // FID name bytes as written, compression ID included
	pastEnd  bool   // put the data extent past the end of the image
	block    uint32 // assigned file entry block
	dataAt   []uint32
}

const (
	bs        = 2048
	partStart = 5
)

// build lays out a minimal UDF volume the way golift and rootICB expect it:
// partition descriptor, logical volume and terminator at sectors 1-3, a
// spare sector 4 inside the sequence, the partition from sector 5, and the
// only anchor in the last sector so the image stays small enough to fuzz.
func build(root *tnode) []byte {
	next := uint32(1) // block 0 is the file set descriptor
	var assign func(n *tnode)
	assign = func(n *tnode) {
		if n.same != nil {
			return
		}
		n.block = next
		next++
		if n.dir {
			for _, k := range n.kids {
				assign(k)
			}
			blocks := (len(dirData(n)) + bs - 1) / bs
			n.dataAt = []uint32{next}
			next += uint32(blocks)
			return
		}
		if n.embed {
			return
		}
		for _, l := range extentLens(n) {
			next++ // leave a gap so extents are not contiguous
			n.dataAt = append(n.dataAt, next)
			next += uint32((l + bs - 1) / bs)
		}
	}
	root.name = ""
	assign(root)
	partLen := next
	sectors := partStart + int(partLen) + 1
	img := make([]byte, sectors*bs)
	sec := func(n int) []byte { return img[n*bs : (n+1)*bs] }
	blk := func(b uint32) []byte { return sec(partStart + int(b)) }

	pd := sec(1)
	binary.LittleEndian.PutUint16(pd, tagPartition)
	binary.LittleEndian.PutUint32(pd[188:], partStart)
	binary.LittleEndian.PutUint32(pd[192:], partLen)

	lvd := sec(2)
	binary.LittleEndian.PutUint16(lvd, tagLogicalVol)
	binary.LittleEndian.PutUint32(lvd[212:], bs)
	putLongAD(lvd[248:], bs, 0)
	binary.LittleEndian.PutUint32(lvd[264:], 6)
	binary.LittleEndian.PutUint32(lvd[268:], 1)
	lvd[440], lvd[441] = 1, 6

	binary.LittleEndian.PutUint16(sec(3), tagTerminating)

	anchor := sec(sectors - 1)
	binary.LittleEndian.PutUint16(anchor, tagAnchor)
	binary.LittleEndian.PutUint32(anchor[16:], 4*bs)
	binary.LittleEndian.PutUint32(anchor[20:], 1)

	fsd := blk(0)
	binary.LittleEndian.PutUint16(fsd, tagFileSet)
	putLongAD(fsd[400:], bs, root.block)

	var write func(n *tnode)
	pastEnd := false
	write = func(n *tnode) {
		if n.same != nil {
			return
		}
		fe := blk(n.block)
		binary.LittleEndian.PutUint16(fe, 261)
		switch {
		case n.fileType != 0:
			fe[27] = n.fileType
		case n.dir:
			fe[27] = typeDirectory
		default:
			fe[27] = typeFile
		}
		data := n.data
		if n.dir {
			data = dirData(n)
			for _, k := range n.kids {
				write(k)
			}
		}
		binary.LittleEndian.PutUint64(fe[56:], uint64(int64(len(data))+n.sizeLie))
		if n.embed {
			fe[34] = 3
			binary.LittleEndian.PutUint32(fe[172:], uint32(len(data)))
			copy(fe[176:], data)
			return
		}
		ads := fe[176:176]
		off := 0
		lens := extentLens(n)
		if n.dir {
			lens = []int{len(data)}
		}
		for i, l := range lens {
			loc := n.dataAt[i]
			if n.pastEnd {
				loc = partLen + 1000
				pastEnd = true
			} else {
				copy(img[(partStart+int(loc))*bs:], data[off:off+l])
			}
			ads = binary.LittleEndian.AppendUint32(ads, uint32(l))
			ads = binary.LittleEndian.AppendUint32(ads, loc)
			off += l
		}
		if n.chained {
			ads = binary.LittleEndian.AppendUint32(ads, 3<<30|bs)
			ads = binary.LittleEndian.AppendUint32(ads, 0)
		}
		binary.LittleEndian.PutUint32(fe[172:], uint32(len(ads)))
	}
	write(root)
	if pastEnd {
		// The partition claims blocks the image does not have.
		binary.LittleEndian.PutUint32(pd[192:], partLen+2000)
	}
	return img
}

func extentLens(n *tnode) []int {
	if len(n.extents) > 0 {
		return n.extents
	}
	if len(n.data) == 0 {
		return nil
	}
	return []int{len(n.data)}
}

func putLongAD(b []byte, length, block uint32) {
	binary.LittleEndian.PutUint32(b[0:], length)
	binary.LittleEndian.PutUint32(b[4:], block)
}

func dirData(n *tnode) []byte {
	out := fid(nil, 0x0a, n.block) // parent entry; golift skips it
	for _, k := range n.kids {
		target := k
		if k.same != nil {
			target = k.same
		}
		chars := byte(0)
		if target.dir {
			chars = fidDirectory
		}
		if k.fidDir != nil {
			chars = 0
			if *k.fidDir {
				chars = fidDirectory
			}
		}
		name := k.rawName
		if name == nil {
			name = append([]byte{8}, k.name...)
		}
		out = append(out, fid(name, chars, target.block)...)
	}
	return out
}

func fid(name []byte, chars byte, block uint32) []byte {
	n := 38 + len(name)
	n += (4 - n%4) % 4
	b := make([]byte, n)
	binary.LittleEndian.PutUint16(b, 257)
	b[18] = chars
	b[19] = byte(len(name))
	putLongAD(b[20:], bs, block)
	copy(b[38:], name)
	return b
}

func dir(name string, kids ...*tnode) *tnode { return &tnode{name: name, dir: true, kids: kids} }

func file(name, data string) *tnode { return &tnode{name: name, data: []byte(data)} }

func writeImage(t testing.TB, img []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "test.iso")
	if err := os.WriteFile(p, img, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}
