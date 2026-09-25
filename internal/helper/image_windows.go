package helper

import (
	"fmt"
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/kyleaupton/flashit/internal/proto"
)

// parentImages duplicates write_image's handle out of the parent, the only
// process the helper serves, through a PROCESS_DUP_HANDLE-only handle to it.
// The helper never opens the image by path: it reads what the app could
// already read, and nothing else.
type parentImages struct {
	parent windows.Handle
}

const (
	imageAccess  = windows.FILE_READ_DATA | windows.FILE_READ_ATTRIBUTES | windows.SYNCHRONIZE
	volumeNameNT = 0x2
)

func (p parentImages) Image(handle uint64, size int64) (*os.File, error) {
	h, err := dupFromParent(p.parent, handle, imageAccess, 0)
	if err != nil {
		return nil, proto.Errorf(proto.CodeInvalidSource, "image handle: %v", err)
	}
	if err := checkRegularFile(h, size); err != nil {
		windows.CloseHandle(h)
		return nil, err
	}
	return os.NewFile(uintptr(h), "image"), nil
}

func dupFromParent(parent windows.Handle, handle uint64, access, options uint32) (windows.Handle, error) {
	if handle == 0 || handle > 1<<32 || handle%4 != 0 {
		return 0, fmt.Errorf("%#x is not a handle value", handle)
	}
	var h windows.Handle
	if err := windows.DuplicateHandle(parent, windows.Handle(handle), windows.CurrentProcess(), &h, access, false, options); err != nil {
		return 0, fmt.Errorf("duplicate %#x from the app: %w", handle, err)
	}
	return h, nil
}

// checkRegularFile accepts only a file on a disk volume: GetFileType is
// FILE_TYPE_DISK (not a pipe, socket or console), it is not a directory or
// device, it has a path below a volume root (a raw volume or disk opened as
// \\.\X: has none), and its size is exactly what the request claims.
func checkRegularFile(h windows.Handle, size int64) error {
	t, err := windows.GetFileType(h)
	if err != nil {
		return proto.Errorf(proto.CodeInvalidSource, "GetFileType: %v", err)
	}
	if t != windows.FILE_TYPE_DISK {
		return proto.Errorf(proto.CodeInvalidSource, "the image is not a file (type %d)", t)
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return proto.Errorf(proto.CodeInvalidSource, "the image is not a file: %v", err)
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_ATTRIBUTE_DEVICE) != 0 {
		return proto.NewError(proto.CodeInvalidSource, "the image is a directory or device")
	}
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), volumeNameNT)
	if err != nil || n == 0 || int(n) >= len(buf) {
		return proto.Errorf(proto.CodeInvalidSource, "the image has no path on a volume: %v", err)
	}
	if !fileBelowDevice(windows.UTF16ToString(buf[:n])) {
		return proto.NewError(proto.CodeInvalidSource, "the image is a volume or device, not a file")
	}
	var std fileStandardInfo
	if err := windows.GetFileInformationByHandleEx(h, windows.FileStandardInfo, (*byte)(unsafe.Pointer(&std)), uint32(unsafe.Sizeof(std))); err != nil {
		return proto.Errorf(proto.CodeInvalidSource, "size of the image: %v", err)
	}
	if std.Directory != 0 {
		return proto.NewError(proto.CodeInvalidSource, "the image is a directory")
	}
	if size <= 0 || std.EndOfFile != size {
		return proto.NewError(proto.CodeSizeMismatch, "the image does not have the size the request claims")
	}
	return nil
}

// fileBelowDevice reports whether an NT path names something below a
// device (\Device\HarddiskVolume3\x.iso, \Device\Mup\server\share\x.iso)
// rather than the device itself or a root.
func fileBelowDevice(nt string) bool {
	rest, ok := strings.CutPrefix(nt, `\Device\`)
	if !ok {
		return false
	}
	_, file, ok := strings.Cut(rest, `\`)
	return ok && file != "" && !strings.HasSuffix(file, `\`)
}

// fileStandardInfo is FILE_STANDARD_INFO.
type fileStandardInfo struct {
	AllocationSize int64
	EndOfFile      int64
	NumberOfLinks  uint32
	DeletePending  byte
	Directory      byte
}
