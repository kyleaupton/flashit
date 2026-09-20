package helper

import (
	"context"
	"errors"
	"io"
	"os"
	"syscall"
	"time"

	"github.com/kyleaupton/flashit/internal/helper/validate"
	"github.com/kyleaupton/flashit/internal/proto"
)

func (sess *session) run(ctx context.Context, req proto.Request, image *os.File) (any, error) {
	switch req.Op {
	case proto.OpWriteImage:
		var p proto.WriteImageParams
		if err := decodeParams(req, &p); err != nil {
			return nil, err
		}
		return nil, sess.writeImage(ctx, req.ID, p, image)
	case proto.OpFormatDisk:
		var p proto.FormatDiskParams
		if err := decodeParams(req, &p); err != nil {
			return nil, err
		}
		return sess.formatDisk(ctx, p)
	case proto.OpUnmount:
		var p proto.UnmountParams
		if err := decodeParams(req, &p); err != nil {
			return nil, err
		}
		return nil, sess.unmount(p)
	case proto.OpEject:
		var p proto.EjectParams
		if err := decodeParams(req, &p); err != nil {
			return nil, err
		}
		return nil, sess.eject(p)
	case proto.OpMountISO:
		return nil, proto.NewError(proto.CodeInvalidRequest, "mount_iso is not implemented")
	default:
		return nil, proto.Errorf(proto.CodeInvalidRequest, "unknown op %q", req.Op)
	}
}

// resolveTarget is the gate every destructive op goes through. It returns the
// canonical device only if it is a whole, removable, non-system block device.
func (sess *session) resolveTarget(op proto.Op, requested string) (DeviceInfo, error) {
	path, err := validate.DevicePath(requested)
	if err != nil {
		return DeviceInfo{}, err
	}
	info, err := sess.s.disk.Stat(path)
	if err != nil {
		return DeviceInfo{}, proto.Errorf(proto.CodeInvalidDevice, "stat %s: %v", path, err)
	}
	system, err := sess.s.disk.SystemDisks()
	if err != nil {
		return DeviceInfo{}, proto.Errorf(proto.CodeInternal, "resolve system disk: %v", err)
	}
	if err := validate.Target(info, system); err != nil {
		return DeviceInfo{}, err
	}
	sess.s.opts.Logger.Info("resolved target", "op", op, "requested", requested, "device", info.Path,
		"size", info.Size, "system", system)
	return info, nil
}

// authorize asks the host's Authorizer, if any, whether the user approved op.
// Any failure is reported as unauthorized; the reason stays in the log. The
// authorizer may block on a sheet it cannot abandon, so a cancel that
// arrived meanwhile wins over an approval, and nothing is unmounted for it.
func (sess *session) authorize(ctx context.Context, op proto.Op, token string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	az := sess.s.opts.Authorizer
	if az == nil {
		return nil
	}
	err := az.Authorize(op, token)
	if ctxErr := ctx.Err(); ctxErr != nil {
		sess.s.opts.Logger.Info("cancelled while authorizing", "op", op)
		return ctxErr
	}
	if err != nil {
		sess.s.opts.Logger.Warn("user authorization refused", "op", op, "uid", sess.peer.UID, "error", err)
		return proto.Errorf(proto.CodeUnauthorized, "%s was not authorized by the user", op)
	}
	sess.s.opts.Logger.Info("user authorized", "op", op, "uid", sess.peer.UID)
	return nil
}

func (sess *session) unmountAll(info DeviceInfo) error {
	parts, err := sess.s.disk.Partitions(info.Path)
	if err != nil {
		return proto.Errorf(proto.CodeInternal, "list partitions of %s: %v", info.Path, err)
	}
	for _, p := range append(parts, info.Path) {
		if err := sess.s.disk.Unmount(p); err != nil {
			return proto.Errorf(proto.CodeDeviceBusy, "unmount %s: %v", p, err)
		}
	}
	return nil
}

// writeImage streams the file the app passed with the request onto the
// device. The helper never opens the image by path: whatever the app could
// open is what gets written, and nothing else.
func (sess *session) writeImage(ctx context.Context, id string, p proto.WriteImageParams, src *os.File) error {
	if src == nil {
		return proto.NewError(proto.CodeInvalidRequest, "write_image must carry the image as a passed file descriptor")
	}
	info, err := sess.resolveTarget(proto.OpWriteImage, p.Device)
	if err != nil {
		return err
	}
	fi, err := src.Stat()
	if err != nil {
		return proto.Errorf(proto.CodeInvalidSource, "stat image: %v", err)
	}
	if err := validate.Source(fi, p.Size); err != nil {
		return err
	}
	if err := validate.Capacity(p.Size, info); err != nil {
		return err
	}
	if err := sess.authorize(ctx, proto.OpWriteImage, p.Authorization); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := sess.unmountAll(info); err != nil {
		return err
	}
	dst, err := sess.s.disk.OpenRaw(info.Path)
	if err != nil {
		if errors.Is(err, syscall.EBUSY) {
			return proto.Errorf(proto.CodeDeviceBusy, "%s is in use: %v", info.Path, err)
		}
		return proto.Errorf(proto.CodeInternal, "open %s: %v", info.Path, err)
	}

	sess.s.opts.Logger.Info("writing image", "device", info.Path, "bytes", p.Size)
	if err := sess.stream(ctx, id, src, dst, p.Size); err != nil {
		dst.Close()
		return err
	}
	if err := dst.Sync(); err != nil {
		dst.Close()
		return proto.Errorf(proto.CodeInternal, "sync %s: %v", info.Path, err)
	}
	if err := dst.Close(); err != nil {
		return proto.Errorf(proto.CodeInternal, "close %s: %v", info.Path, err)
	}
	sess.write(proto.ProgressResponse(id, uint64(p.Size), uint64(p.Size)))
	sess.s.opts.Logger.Info("write complete", "device", info.Path, "bytes", p.Size)
	return nil
}

// stream copies exactly size bytes, checking for cancellation before every
// chunk. A source that ends early is a size mismatch, not a short write.
func (sess *session) stream(ctx context.Context, id string, src io.Reader, dst io.Writer, size int64) error {
	buf := make([]byte, sess.s.opts.WriteBufferSize)
	th := throttle{interval: sess.s.opts.ProgressInterval}
	total := uint64(size)
	var written uint64
	remaining := size

	for remaining > 0 {
		if ctx.Err() != nil {
			return proto.Errorf(proto.CodeCancelled, "cancelled after %d bytes", written)
		}
		n := len(buf)
		if int64(n) > remaining {
			n = int(remaining)
		}
		rn, rerr := io.ReadFull(src, buf[:n])
		if rn > 0 {
			wn, werr := dst.Write(buf[:rn])
			written += uint64(wn)
			remaining -= int64(wn)
			if werr != nil {
				return proto.Errorf(proto.CodeInternal, "write failed at %d bytes: %v", written, werr)
			}
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) || errors.Is(rerr, io.ErrUnexpectedEOF) {
				return proto.Errorf(proto.CodeSizeMismatch, "source ended after %d of %d bytes", written, size)
			}
			return proto.Errorf(proto.CodeInternal, "read failed at %d bytes: %v", written, rerr)
		}
		if th.allow(time.Now()) {
			sess.write(proto.ProgressResponse(id, written, total))
		}
	}
	return nil
}

func (sess *session) formatDisk(ctx context.Context, p proto.FormatDiskParams) (any, error) {
	info, err := sess.resolveTarget(proto.OpFormatDisk, p.Device)
	if err != nil {
		return nil, err
	}
	fs, err := validate.Filesystem(p.Filesystem)
	if err != nil {
		return nil, err
	}
	if err := validate.Label(p.Label); err != nil {
		return nil, err
	}
	if err := sess.authorize(ctx, proto.OpFormatDisk, p.Authorization); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := sess.unmountAll(info); err != nil {
		return nil, err
	}
	sess.s.opts.Logger.Info("formatting", "device", info.Path, "fs", fs, "label", p.Label)
	mountpoint, err := sess.s.disk.Format(ctx, info.Path, fs, p.Label)
	if err != nil {
		switch {
		case ctx.Err() != nil:
			return nil, proto.NewError(proto.CodeCancelled, "format cancelled")
		case errors.Is(err, syscall.EBUSY):
			return nil, proto.Errorf(proto.CodeDeviceBusy, "format %s: %v", info.Path, err)
		}
		return nil, proto.Errorf(proto.CodeInternal, "format %s: %v", info.Path, err)
	}
	return proto.FormatDiskResult{Mountpoint: mountpoint}, nil
}

func (sess *session) unmount(p proto.UnmountParams) error {
	switch {
	case p.Device != "" && p.Mountpoint != "":
		return proto.NewError(proto.CodeInvalidRequest, "unmount takes a device or a mountpoint, not both")
	case p.Device != "":
		info, err := sess.resolveTarget(proto.OpUnmount, p.Device)
		if err != nil {
			return err
		}
		return sess.unmountAll(info)
	case p.Mountpoint != "":
		mp, err := validate.Mountpoint(p.Mountpoint)
		if err != nil {
			return err
		}
		sess.s.opts.Logger.Info("unmounting", "mountpoint", mp)
		if err := sess.s.disk.Unmount(mp); err != nil {
			return proto.Errorf(proto.CodeDeviceBusy, "unmount %s: %v", mp, err)
		}
		return nil
	default:
		return proto.NewError(proto.CodeInvalidRequest, "unmount needs a device or a mountpoint")
	}
}

func (sess *session) eject(p proto.EjectParams) error {
	info, err := sess.resolveTarget(proto.OpEject, p.Device)
	if err != nil {
		return err
	}
	if err := sess.s.disk.Eject(info.Path); err != nil {
		return proto.Errorf(proto.CodeInternal, "eject %s: %v", info.Path, err)
	}
	return nil
}
