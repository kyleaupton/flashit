package wim

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/kyleaupton/flashit/internal/fs"
)

// Progress contains information about the current split/copy operation.
type Progress struct {
	Phase      string // "analyzing", "splitting", "copying"
	DoneBytes  uint64
	TotalBytes uint64
	Part       uint32
	TotalParts uint32
}

// SplitOptions configures the WIM split operation.
type SplitOptions struct {
	PartSizeMiB int // Size of each part in MiB (default: 3800 for FAT32 safety)
}

// splitBlob represents a blob to be written to a split WIM part.
type splitBlob struct {
	stream     StreamDescriptor // Original stream descriptor
	partNumber int              // Which part this blob is assigned to (1-based)
	newOffset  int64            // New offset in the destination part
	isMetadata bool             // Whether this is a metadata blob
}

// splitPart represents a single SWM part file.
type splitPart struct {
	blobs    []*splitBlob
	dataSize int64 // Total size of blob data in this part
}

// SplitWithProgress splits a WIM file into multiple SWM parts for FAT32 compatibility.
// Each part will be named with the pattern: <dstPrefix>.swm, <dstPrefix>2.swm, etc.
//
// The callback is called periodically with progress updates. Return false to cancel.
func SplitWithProgress(
	ctx context.Context,
	srcWIM string,
	dstPrefix string,
	opts SplitOptions,
	cb func(Progress) bool,
) error {
	if opts.PartSizeMiB <= 0 {
		opts.PartSizeMiB = 3800 // ~3.7 GB, safe for FAT32's 4GB limit
	}
	partSizeBytes := int64(opts.PartSizeMiB) * 1024 * 1024

	// Open source WIM
	srcFile, err := os.Open(srcWIM)
	if err != nil {
		return fmt.Errorf("failed to open source WIM: %w", err)
	}
	defer srcFile.Close()

	// Report analyzing phase
	if cb != nil {
		if !cb(Progress{Phase: "analyzing"}) {
			return context.Canceled
		}
	}

	// Read and parse source WIM header
	var srcHeader WimHeader
	if err := binary.Read(srcFile, binary.LittleEndian, &srcHeader); err != nil {
		return fmt.Errorf("failed to read WIM header: %w", err)
	}

	if srcHeader.ImageTag != wimImageTag {
		return fmt.Errorf("not a valid WIM file")
	}

	if srcHeader.TotalParts != 1 {
		return fmt.Errorf("source WIM is already split (part %d of %d)", srcHeader.PartNumber, srcHeader.TotalParts)
	}

	// Read all stream descriptors from offset table
	streams, err := readAllStreams(srcFile, &srcHeader)
	if err != nil {
		return fmt.Errorf("failed to read stream table: %w", err)
	}

	// Calculate total data size
	var totalDataSize int64
	for _, s := range streams {
		totalDataSize += s.stream.CompressedSize()
	}

	// Assign blobs to parts using binpacking
	parts := assignBlobsToParts(streams, partSizeBytes)
	numParts := len(parts)

	if cb != nil {
		if !cb(Progress{
			Phase:      "splitting",
			TotalBytes: uint64(totalDataSize),
			TotalParts: uint32(numParts),
		}) {
			return context.Canceled
		}
	}

	// Ensure destination directory exists
	if err := os.MkdirAll(filepath.Dir(dstPrefix), 0755); err != nil {
		return fmt.Errorf("failed to create destination directory: %w", err)
	}

	// Generate a new GUID for this split set
	var newGUID guid
	if err := binary.Read(rand.Reader, binary.LittleEndian, &newGUID); err != nil {
		return fmt.Errorf("failed to generate GUID: %w", err)
	}

	// Write each part
	var totalWritten int64
	for partNum := 1; partNum <= numParts; partNum++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		part := parts[partNum-1]

		// Generate part filename
		var partPath string
		if partNum == 1 {
			partPath = dstPrefix + ".swm"
		} else {
			partPath = fmt.Sprintf("%s%d.swm", dstPrefix, partNum)
		}

		_, err = writeSWMPart(ctx, srcFile, partPath, &srcHeader, newGUID, part, partNum, numParts, func(n int64) bool {
			totalWritten += n
			if cb != nil {
				return cb(Progress{
					Phase:      "splitting",
					DoneBytes:  uint64(totalWritten),
					TotalBytes: uint64(totalDataSize),
					Part:       uint32(partNum),
					TotalParts: uint32(numParts),
				})
			}
			return true
		})
		if err != nil {
			return fmt.Errorf("failed to write part %d: %w", partNum, err)
		}
	}

	return nil
}

// readAllStreams reads all stream descriptors from the WIM's offset table.
func readAllStreams(f *os.File, hdr *WimHeader) ([]*splitBlob, error) {
	// Seek to offset table
	tableOffset := hdr.OffsetTable.Offset
	tableSize := hdr.OffsetTable.CompressedSize()

	// Read the offset table (it may be compressed, but typically isn't for the offset table itself)
	// The offset table resource descriptor tells us if it's compressed
	var tableData []byte

	if hdr.OffsetTable.Flags()&resFlagCompressed != 0 {
		// Compressed offset table - need to decompress
		section := io.NewSectionReader(f, tableOffset, tableSize)
		cr, err := newCompressedReader(section, hdr.OffsetTable.OriginalSize, 0)
		if err != nil {
			return nil, fmt.Errorf("failed to create decompressor for offset table: %w", err)
		}
		defer cr.Close()
		tableData, err = io.ReadAll(cr)
		if err != nil {
			return nil, fmt.Errorf("failed to decompress offset table: %w", err)
		}
	} else {
		// Uncompressed - read directly
		tableData = make([]byte, tableSize)
		if _, err := f.ReadAt(tableData, tableOffset); err != nil {
			return nil, fmt.Errorf("failed to read offset table: %w", err)
		}
	}

	// Parse stream descriptors
	var blobs []*splitBlob
	br := bytes.NewReader(tableData)
	for {
		var sd StreamDescriptor
		if err := binary.Read(br, binary.LittleEndian, &sd); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("failed to read stream descriptor: %w", err)
		}

		blob := &splitBlob{
			stream:     sd,
			isMetadata: sd.Flags()&resFlagMetadata != 0,
		}
		blobs = append(blobs, blob)
	}

	// Sort by offset for sequential reading
	sort.Slice(blobs, func(i, j int) bool {
		return blobs[i].stream.Offset < blobs[j].stream.Offset
	})

	return blobs, nil
}

// assignBlobsToParts assigns blobs to parts using a binpacking algorithm.
// Metadata blobs always go in part 1.
func assignBlobsToParts(blobs []*splitBlob, maxPartSize int64) []*splitPart {
	// First pass: separate metadata from regular blobs
	var metadataBlobs []*splitBlob
	var regularBlobs []*splitBlob

	for _, blob := range blobs {
		if blob.isMetadata {
			metadataBlobs = append(metadataBlobs, blob)
		} else {
			regularBlobs = append(regularBlobs, blob)
		}
	}

	// Start with part 1 containing all metadata
	parts := []*splitPart{{}}
	currentPart := parts[0]

	// Add metadata blobs to part 1
	for _, blob := range metadataBlobs {
		blob.partNumber = 1
		currentPart.blobs = append(currentPart.blobs, blob)
		currentPart.dataSize += blob.stream.CompressedSize()
	}

	// Add regular blobs, starting new parts as needed
	for _, blob := range regularBlobs {
		blobSize := blob.stream.CompressedSize()

		// Check if adding this blob exceeds the part size limit
		// Exception: if current part is empty (or only has metadata), always add
		if currentPart.dataSize+blobSize > maxPartSize && len(currentPart.blobs) > len(metadataBlobs) {
			// Start a new part
			currentPart = &splitPart{}
			parts = append(parts, currentPart)
		}

		blob.partNumber = len(parts)
		currentPart.blobs = append(currentPart.blobs, blob)
		currentPart.dataSize += blobSize
	}

	return parts
}

// writeSWMPart writes a single SWM part file.
func writeSWMPart(
	ctx context.Context,
	srcFile *os.File,
	dstPath string,
	srcHeader *WimHeader,
	newGUID guid,
	part *splitPart,
	partNum int,
	totalParts int,
	onProgress func(n int64) bool,
) (int64, error) {
	dstFile, err := os.Create(dstPath)
	if err != nil {
		return 0, err
	}
	defer dstFile.Close()

	// Write placeholder header (we'll update it at the end)
	header := *srcHeader
	header.WIMGuid = newGUID
	header.PartNumber = uint16(partNum)
	header.TotalParts = uint16(totalParts)
	header.Flags |= hdrFlagSpanned // Mark as spanned/split

	// For non-first parts, we don't include metadata
	if partNum > 1 {
		header.ImageCount = 0
		header.BootIndex = 0
		header.BootMetadata = ResourceDescriptor{}
	}

	if err := binary.Write(dstFile, binary.LittleEndian, &header); err != nil {
		return 0, fmt.Errorf("failed to write header: %w", err)
	}

	// Current write position (after header)
	currentOffset := int64(binary.Size(header))

	// Copy blob data and track new offsets
	buf := make([]byte, 2*1024*1024) // 2MB buffer
	var totalCopied int64

	for _, blob := range part.blobs {
		select {
		case <-ctx.Done():
			return totalCopied, ctx.Err()
		default:
		}

		// Record new offset
		blob.newOffset = currentOffset

		// Copy blob data from source with chunk-level progress
		srcOffset := blob.stream.Offset
		blobSize := blob.stream.CompressedSize()

		_, err := copyBlobData(ctx, srcFile, dstFile, srcOffset, blobSize, buf, func(n int64) bool {
			currentOffset += n
			totalCopied += n
			if onProgress != nil {
				return onProgress(n)
			}
			return true
		})
		if err != nil {
			return totalCopied, fmt.Errorf("failed to copy blob: %w", err)
		}
	}

	// Write offset table for this part
	offsetTableStart := currentOffset
	offsetTableBuf := new(bytes.Buffer)

	for _, blob := range part.blobs {
		// Create updated stream descriptor with new offset
		sd := blob.stream
		// Update offset to new location
		sd.Offset = blob.newOffset
		sd.PartNumber = uint16(partNum)

		if err := binary.Write(offsetTableBuf, binary.LittleEndian, &sd); err != nil {
			return totalCopied, fmt.Errorf("failed to write stream descriptor: %w", err)
		}
	}

	if _, err := dstFile.Write(offsetTableBuf.Bytes()); err != nil {
		return totalCopied, fmt.Errorf("failed to write offset table: %w", err)
	}
	currentOffset += int64(offsetTableBuf.Len())

	// Copy XML data (same for all parts) - no progress needed for small XML
	xmlStart := currentOffset
	xmlSize := srcHeader.XMLData.CompressedSize()
	if xmlSize > 0 {
		if _, err := copyBlobData(ctx, srcFile, dstFile, srcHeader.XMLData.Offset, xmlSize, buf, nil); err != nil {
			return totalCopied, fmt.Errorf("failed to copy XML data: %w", err)
		}
		currentOffset += xmlSize
	}

	// Update header with final resource locations
	header.OffsetTable = ResourceDescriptor{
		FlagsAndCompressedSize: uint64(offsetTableBuf.Len()), // No compression flags, just size
		Offset:                 offsetTableStart,
		OriginalSize:           int64(offsetTableBuf.Len()),
	}

	if xmlSize > 0 {
		header.XMLData = ResourceDescriptor{
			FlagsAndCompressedSize: srcHeader.XMLData.FlagsAndCompressedSize,
			Offset:                 xmlStart,
			OriginalSize:           srcHeader.XMLData.OriginalSize,
		}
	}

	// Clear integrity table (we're not computing it)
	header.Integrity = ResourceDescriptor{}

	// Seek back and rewrite header with correct offsets
	if _, err := dstFile.Seek(0, io.SeekStart); err != nil {
		return totalCopied, fmt.Errorf("failed to seek to header: %w", err)
	}
	if err := binary.Write(dstFile, binary.LittleEndian, &header); err != nil {
		return totalCopied, fmt.Errorf("failed to rewrite header: %w", err)
	}

	return totalCopied, dstFile.Sync()
}

// copyBlobData copies raw blob data from source to destination with chunk-level progress.
// onChunk is called after each chunk is written with the number of bytes just written.
// Return false from onChunk to cancel the operation.
func copyBlobData(ctx context.Context, src *os.File, dst *os.File, srcOffset int64, size int64, buf []byte, onChunk func(int64) bool) (int64, error) {
	var copied int64
	for copied < size {
		// Check for cancellation
		select {
		case <-ctx.Done():
			return copied, ctx.Err()
		default:
		}

		toRead := int64(len(buf))
		if size-copied < toRead {
			toRead = size - copied
		}

		n, err := src.ReadAt(buf[:toRead], srcOffset+copied)
		if err != nil && err != io.EOF {
			return copied, err
		}
		if n == 0 {
			break
		}

		if _, err := dst.Write(buf[:n]); err != nil {
			return copied, err
		}
		copied += int64(n)

		// Report chunk progress
		if onChunk != nil && !onChunk(int64(n)) {
			return copied, context.Canceled
		}
	}
	return copied, nil
}

// CopySWMs copies install*.swm files from swmDir to <usbRoot>/sources/.
// The callback receives (bytesWritten, totalBytes) and should return false to cancel.
func CopySWMs(ctx context.Context, swmDir string, usbRoot string, cb func(done, total int64) bool) error {
	srcs, err := filepath.Glob(filepath.Join(swmDir, "install*.swm"))
	if err != nil {
		return err
	}
	if len(srcs) == 0 {
		return fmt.Errorf("no .swm parts found in %s", swmDir)
	}

	dstDir := filepath.Join(usbRoot, "sources")
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return err
	}

	// Calculate total size
	var total int64
	for _, s := range srcs {
		fi, err := os.Stat(s)
		if err != nil {
			return err
		}
		total += fi.Size()
	}

	var done int64
	for _, s := range srcs {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		base := filepath.Base(s)
		dst := filepath.Join(dstDir, base)

		fileInfo, err := os.Stat(s)
		if err != nil {
			return err
		}
		fileSize := fileInfo.Size()

		err = fs.CopyFile(ctx, s, dst, fs.CopyFileOptions{
			SyncAfter:        true,
			ProgressInterval: 250 * time.Millisecond,
		}, func(p fs.CopyProgress) bool {
			if cb != nil {
				return cb(done+p.Written, total)
			}
			return true
		})
		if err != nil {
			return err
		}
		done += fileSize
	}
	return nil
}
