package fs

import (
	"context"
	"fmt"
	"io"
	iofs "io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CopyDirOptions configures directory copy behavior.
type CopyDirOptions struct {
	// Filter returns true if a file should be copied, false to skip.
	// Receives the slash-separated path within the source and its info.
	// If nil, all files are copied.
	Filter func(relPath string, info iofs.FileInfo) bool

	SyncAfter        bool          // Sync each file to disk after copy completes
	SyncEvery        int64         // Sync after N bytes written per file (0 = no intermediate syncs)
	ProgressInterval time.Duration // Throttle progress callbacks (0 = every chunk)
	BufferSize       int           // Read/write buffer size (default 2MB)
}

// CopyProgress reports copy progress.
type CopyProgress struct {
	Written int64 // Bytes written so far
	Total   int64 // Total bytes (-1 if unknown)
}

const defaultBufferSize = 2 * 1024 * 1024 // 2MB

// CopyDir copies every regular file and directory in src into dst with
// progress reporting. Anything else in src (a symlink on a mounted image,
// say) fails the copy rather than being followed.
// The onProgress callback receives cumulative progress across all files and should return true to continue, false to cancel.
// If onProgress is nil, no progress reporting occurs.
func CopyDir(ctx context.Context, src iofs.FS, dst string, opts CopyDirOptions, onProgress func(CopyProgress) bool) error {
	bufSize := opts.BufferSize
	if bufSize <= 0 {
		bufSize = defaultBufferSize
	}

	var totalBytes int64
	err := iofs.WalkDir(src, ".", func(p string, d iofs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file", p)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if opts.Filter != nil && !opts.Filter(p, info) {
			return nil
		}
		totalBytes += info.Size()
		return nil
	})
	if err != nil {
		return err
	}

	var writtenBytes int64
	var lastProgressAt time.Time
	buf := make([]byte, bufSize)

	return iofs.WalkDir(src, ".", func(p string, d iofs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		dstPath, err := destination(dst, p)
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(dstPath, 0755)
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file", p)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if opts.Filter != nil && !opts.Filter(p, info) {
			return nil
		}

		written, err := copyFile(ctx, src, p, dstPath, info.Size(), buf, opts, func(n int64) bool {
			if onProgress == nil {
				return true
			}
			done := writtenBytes + n
			if opts.ProgressInterval == 0 || time.Since(lastProgressAt) >= opts.ProgressInterval || done == totalBytes {
				if !onProgress(CopyProgress{Written: done, Total: totalBytes}) {
					return false
				}
				lastProgressAt = time.Now()
			}
			return true
		})
		if err != nil {
			return err
		}
		writtenBytes += written
		return nil
	})
}

// destination joins a source path onto dst and refuses any result that is
// not inside dst. The source is expected to have been checked already;
// this is the last line if it was not.
func destination(dst, p string) (string, error) {
	if !iofs.ValidPath(p) {
		return "", fmt.Errorf("refusing to copy %q: not a valid path", p)
	}
	if p == "." {
		return dst, nil
	}
	local := filepath.FromSlash(p)
	if !filepath.IsLocal(local) {
		return "", fmt.Errorf("refusing to copy %q: it would leave the target volume", p)
	}
	out := filepath.Join(dst, local)
	rel, err := filepath.Rel(dst, out)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("refusing to copy %q: it would leave the target volume", p)
	}
	return out, nil
}

// copyFile copies one file, reporting the bytes written so far to
// onProgress. The destination must not exist yet: on a freshly formatted
// volume an existing file means two source names landed on one FAT32 name.
func copyFile(ctx context.Context, src iofs.FS, p, dstPath string, size int64, buf []byte, opts CopyDirOptions, onProgress func(int64) bool) (int64, error) {
	in, err := src.Open(p)
	if err != nil {
		return 0, err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
		return 0, err
	}
	out, err := os.OpenFile(dstPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return 0, err
	}
	defer out.Close()

	var written, lastSyncAt int64
	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		n, readErr := in.Read(buf)
		if n > 0 {
			if _, err := out.Write(buf[:n]); err != nil {
				return written, err
			}
			written += int64(n)
			if opts.SyncEvery > 0 && written-lastSyncAt >= opts.SyncEvery {
				if err := out.Sync(); err != nil {
					return written, err
				}
				lastSyncAt = written
			}
			if !onProgress(written) {
				return written, context.Canceled
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return written, fmt.Errorf("reading %s: %w", p, readErr)
		}
	}

	if written != size {
		return written, fmt.Errorf("%s yielded %d bytes but its size is %d", p, written, size)
	}
	if opts.SyncAfter {
		if err := out.Sync(); err != nil {
			return written, err
		}
	}
	return written, out.Close()
}
