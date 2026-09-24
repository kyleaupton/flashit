package sources

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf16"
)

// Microsoft's ISOs are UDF images whose ISO 9660 side holds only a README
// saying so, so Windows sources are found through the UDF tree (ECMA-167
// part 3 and 4). This reads what oscdimg writes: one type 1 partition map,
// short or long allocation descriptors, files listed by File Entries or
// Extended File Entries.

const (
	udfAnchorSector = 256

	tagAnchor            = 2
	tagPartition         = 5
	tagLogicalVolume     = 6
	tagTerminating       = 8
	tagFileSet           = 256
	tagFileIdentifier    = 257
	tagFileEntry         = 261
	tagExtendedFileEntry = 266

	adShort    = 0
	adLong     = 1
	adExtended = 2
	adEmbedded = 3

	fidDirectory = 0x02
	fidDeleted   = 0x04
	fidParent    = 0x08

	udfMaxDescriptors = 64
)

var errNoUDF = errors.New("no UDF anchor")

type udfVolume struct {
	r         io.ReaderAt
	size      int64
	blockSize int64
	partStart map[uint16]uint32 // partition reference number to starting sector
	rootICB   longAD
}

type longAD struct {
	length uint32
	block  uint32
	part   uint16
}

func (a longAD) recorded() bool { return a.length>>30 == 0 && a.length&0x3fffffff > 0 }
func (a longAD) bytes() uint32  { return a.length & 0x3fffffff }

// readUDF locates the volume's root directory. errNoUDF means the image
// has no UDF file system at all; any other error means it has one that
// could not be read.
func readUDF(r io.ReaderAt, size int64) (*udfVolume, error) {
	sectors := size / sectorSize
	var anchor []byte
	for _, s := range []int64{udfAnchorSector, sectors - 257, sectors - 1} {
		if s <= 0 || s >= sectors {
			continue
		}
		buf := make([]byte, sectorSize)
		if _, err := r.ReadAt(buf, s*sectorSize); err != nil {
			return nil, err
		}
		if binary.LittleEndian.Uint16(buf[0:2]) == tagAnchor {
			anchor = buf
			break
		}
	}
	if anchor == nil {
		return nil, errNoUDF
	}

	vdsLen := binary.LittleEndian.Uint32(anchor[16:20])
	vdsLoc := binary.LittleEndian.Uint32(anchor[20:24])
	if vdsLen == 0 || int64(vdsLoc) >= sectors {
		return nil, errors.New("udf: anchor points outside the image")
	}

	v := &udfVolume{r: r, size: size, blockSize: sectorSize, partStart: map[uint16]uint32{}}
	partNumbers := map[uint16]uint32{} // partition number to starting sector
	var maps []uint16                  // partition reference number to partition number
	var fsd longAD
	buf := make([]byte, sectorSize)
	for i := int64(0); i < udfMaxDescriptors && i*sectorSize < int64(vdsLen); i++ {
		if _, err := r.ReadAt(buf, (int64(vdsLoc)+i)*sectorSize); err != nil {
			return nil, err
		}
		switch binary.LittleEndian.Uint16(buf[0:2]) {
		case tagPartition:
			num := binary.LittleEndian.Uint16(buf[22:24])
			partNumbers[num] = binary.LittleEndian.Uint32(buf[188:192])
		case tagLogicalVolume:
			if bs := binary.LittleEndian.Uint32(buf[212:216]); bs != 0 {
				v.blockSize = int64(bs)
			}
			fsd = parseLongAD(buf[248:264])
			n := binary.LittleEndian.Uint32(buf[268:272])
			pos := 440
			for j := uint32(0); j < n && pos+2 <= len(buf); j++ {
				mapType, mapLen := buf[pos], int(buf[pos+1])
				if mapLen == 0 || pos+mapLen > len(buf) {
					break
				}
				if mapType != 1 {
					return nil, fmt.Errorf("udf: partition map type %d is not supported", mapType)
				}
				maps = append(maps, binary.LittleEndian.Uint16(buf[pos+4:pos+6]))
				pos += mapLen
			}
		case tagTerminating:
			i = udfMaxDescriptors
		}
	}
	for ref, num := range maps {
		start, ok := partNumbers[num]
		if !ok {
			return nil, fmt.Errorf("udf: partition map %d names unknown partition %d", ref, num)
		}
		v.partStart[uint16(ref)] = start
	}
	if len(v.partStart) == 0 || !fsd.recorded() {
		return nil, errors.New("udf: no usable partition or file set")
	}

	fsdBuf, err := v.readExtent(fsd)
	if err != nil {
		return nil, err
	}
	if len(fsdBuf) < 416 || binary.LittleEndian.Uint16(fsdBuf[0:2]) != tagFileSet {
		return nil, errors.New("udf: file set descriptor missing")
	}
	v.rootICB = parseLongAD(fsdBuf[400:416])
	return v, nil
}

func parseLongAD(b []byte) longAD {
	return longAD{
		length: binary.LittleEndian.Uint32(b[0:4]),
		block:  binary.LittleEndian.Uint32(b[4:8]),
		part:   binary.LittleEndian.Uint16(b[8:10]),
	}
}

func (v *udfVolume) offset(a longAD) (int64, error) {
	start, ok := v.partStart[a.part]
	if !ok {
		return 0, fmt.Errorf("udf: reference to unknown partition %d", a.part)
	}
	off := (int64(start) + int64(a.block)) * v.blockSize
	if off+int64(a.bytes()) > v.size {
		return 0, errors.New("udf: extent lies past the end of the image")
	}
	return off, nil
}

func (v *udfVolume) readExtent(a longAD) ([]byte, error) {
	if a.bytes() > maxDirSize {
		return nil, fmt.Errorf("udf: extent of %d bytes is implausible", a.bytes())
	}
	off, err := v.offset(a)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, a.bytes())
	_, err = v.r.ReadAt(buf, off)
	return buf, err
}

// fileEntry is what the probe needs from a File Entry: the file's size and,
// for a directory, where its listing lives.
type fileEntry struct {
	size int64
	data []byte // directory listing, once read
}

func (v *udfVolume) readFileEntry(icb longAD, wantData bool) (fileEntry, error) {
	if icb.bytes() < 176 {
		icb.length = uint32(v.blockSize)
	}
	buf, err := v.readExtent(icb)
	if err != nil {
		return fileEntry{}, err
	}
	var eaLen, adLen uint32
	var body []byte
	switch binary.LittleEndian.Uint16(buf[0:2]) {
	case tagFileEntry:
		if len(buf) < 176 {
			return fileEntry{}, errors.New("udf: short file entry")
		}
		eaLen = binary.LittleEndian.Uint32(buf[168:172])
		adLen = binary.LittleEndian.Uint32(buf[172:176])
		body = buf[176:]
	case tagExtendedFileEntry:
		if len(buf) < 216 {
			return fileEntry{}, errors.New("udf: short extended file entry")
		}
		eaLen = binary.LittleEndian.Uint32(buf[208:212])
		adLen = binary.LittleEndian.Uint32(buf[212:216])
		body = buf[216:]
	default:
		return fileEntry{}, fmt.Errorf("udf: block %d is not a file entry", icb.block)
	}
	fe := fileEntry{size: int64(binary.LittleEndian.Uint64(buf[56:64]))}
	if !wantData {
		return fe, nil
	}
	if uint64(eaLen)+uint64(adLen) > uint64(len(body)) {
		return fileEntry{}, errors.New("udf: allocation descriptors overrun the file entry")
	}
	ads := body[eaLen : eaLen+adLen]
	if fe.size > maxDirSize {
		return fileEntry{}, fmt.Errorf("udf: directory of %d bytes is implausible", fe.size)
	}

	switch binary.LittleEndian.Uint16(buf[34:36]) & 7 {
	case adEmbedded:
		fe.data = ads
	case adShort:
		for pos := 0; pos+8 <= len(ads); pos += 8 {
			ext := longAD{length: binary.LittleEndian.Uint32(ads[pos:]), block: binary.LittleEndian.Uint32(ads[pos+4:]), part: icb.part}
			if err := v.appendExtent(&fe, ext); err != nil {
				return fileEntry{}, err
			}
		}
	case adLong:
		for pos := 0; pos+16 <= len(ads); pos += 16 {
			if err := v.appendExtent(&fe, parseLongAD(ads[pos:])); err != nil {
				return fileEntry{}, err
			}
		}
	default:
		return fileEntry{}, errors.New("udf: extended allocation descriptors are not supported")
	}
	if int64(len(fe.data)) > fe.size {
		fe.data = fe.data[:fe.size]
	}
	return fe, nil
}

func (v *udfVolume) appendExtent(fe *fileEntry, ext longAD) error {
	if ext.length>>30 == 3 {
		return errors.New("udf: chained allocation descriptors are not supported")
	}
	if !ext.recorded() {
		return nil
	}
	if int64(len(fe.data))+int64(ext.bytes()) > maxDirSize {
		return errors.New("udf: directory listing is implausibly large")
	}
	b, err := v.readExtent(ext)
	if err != nil {
		return err
	}
	fe.data = append(fe.data, b...)
	return nil
}

// lookup walks the UDF tree from the root. The second result is false when
// the path does not exist.
func (v *udfVolume) lookup(path ...string) (int64, bool, error) {
	icb := v.rootICB
	for i, name := range path {
		dir, err := v.readFileEntry(icb, true)
		if err != nil {
			return 0, false, err
		}
		child, found, err := findFID(dir.data, name)
		if err != nil {
			return 0, false, err
		}
		if !found {
			return 0, false, nil
		}
		last := i == len(path)-1
		if last {
			if child.dir {
				return 0, false, nil
			}
			fe, err := v.readFileEntry(child.icb, false)
			if err != nil {
				return 0, false, err
			}
			return fe.size, true, nil
		}
		if !child.dir {
			return 0, false, nil
		}
		icb = child.icb
	}
	return 0, false, nil
}

type fid struct {
	name string
	dir  bool
	icb  longAD
}

func findFID(data []byte, name string) (fid, bool, error) {
	pos := 0
	for pos+38 <= len(data) {
		if binary.LittleEndian.Uint16(data[pos:pos+2]) != tagFileIdentifier {
			return fid{}, false, fmt.Errorf("udf: bad file identifier at %d", pos)
		}
		chars := data[pos+18]
		nameLen := int(data[pos+19])
		iuLen := int(binary.LittleEndian.Uint16(data[pos+36 : pos+38]))
		total := 38 + iuLen + nameLen
		total += (4 - total%4) % 4
		if pos+total > len(data) {
			break
		}
		if chars&(fidDeleted|fidParent) == 0 {
			got := decodeDString(data[pos+38+iuLen : pos+38+iuLen+nameLen])
			if strings.EqualFold(got, name) {
				return fid{name: got, dir: chars&fidDirectory != 0, icb: parseLongAD(data[pos+20 : pos+36])}, true, nil
			}
		}
		pos += total
	}
	return fid{}, false, nil
}

// decodeDString decodes an OSTA compressed unicode identifier.
func decodeDString(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	switch b[0] {
	case 8:
		return string(b[1:])
	case 16:
		u := make([]uint16, (len(b)-1)/2)
		for i := range u {
			u[i] = binary.BigEndian.Uint16(b[1+2*i:])
		}
		return string(utf16.Decode(u))
	}
	return ""
}
