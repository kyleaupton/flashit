//go:build darwin

package helper

/*
#cgo CFLAGS: -mmacosx-version-min=13.0
#cgo LDFLAGS: -framework Security -framework CoreFoundation -framework DiskArbitration
#include <stdlib.h>
#include "authz_darwin.h"
*/
import "C"

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/kyleaupton/flashit/internal/proto"
)

const (
	externalFormLength = 32
	// Apple's right authopen checks for a read-write open; the suffix is the
	// path. Every such right shares one rule, so the credential the sheet
	// puts in the ref is not bound to the device (decision 006).
	rightPrefix = "sys.openfile.readwrite."
)

// Authorization Services OSStatus values.
const (
	errAuthorizationDenied                = -60005
	errAuthorizationCanceled              = -60006
	errAuthorizationInteractionNotAllowed = -60007
)

type authz struct {
	log   *slog.Logger
	probe func(raw string) error
}

// NewAuthorizer returns the Authorizer that redeems the app's ref for Apple's
// right on the raw device, raising the password sheet named after the app.
func NewAuthorizer(log *slog.Logger) Authorizer {
	return authz{log: log, probe: probeRemovableVolumes}
}

func (a authz) Authorize(op proto.Op, token string, device DeviceInfo) (Grant, error) {
	if token == "" {
		return nil, fmt.Errorf("%s carries no authorization", op)
	}
	ext, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return nil, fmt.Errorf("authorization is not base64: %v", err)
	}
	if len(ext) != externalFormLength {
		return nil, fmt.Errorf("authorization is %d bytes, not %d", len(ext), externalFormLength)
	}
	raw := rawDevicePath(device)
	if err := a.probe(raw); err != nil {
		return nil, err
	}

	g := &authzGrant{raw: raw, info: device}
	if st := C.flashit_authz_from_external((*C.uchar)(unsafe.Pointer(&ext[0])), &g.ref); st != 0 {
		return nil, fmt.Errorf("AuthorizationCreateFromExternalForm: %d", int(st))
	}
	right := rightPrefix + raw
	cright := C.CString(right)
	st := C.flashit_authz_copy_rights(g.ref, cright)
	C.free(unsafe.Pointer(cright))
	switch st {
	case 0:
	case errAuthorizationCanceled:
		g.Release()
		return nil, proto.Errorf(proto.CodeCancelled, "%s was cancelled at the authorization sheet", op)
	case errAuthorizationDenied, errAuthorizationInteractionNotAllowed:
		g.Release()
		return nil, proto.Errorf(proto.CodeUnauthorized, "%s was not authorized by the user", op)
	default:
		g.Release()
		return nil, fmt.Errorf("AuthorizationCopyRights(%s): %d", right, int(st))
	}
	if st := C.flashit_authz_external(g.ref, (*C.uchar)(unsafe.Pointer(&g.form[0]))); st != 0 {
		g.Release()
		return nil, fmt.Errorf("AuthorizationMakeExternalForm: %d", int(st))
	}
	a.log.Info("right granted", "right", right)
	return g, nil
}

// authzGrant is the authorized ref and the external form authopen reads
// from its stdin. Until Release the form opens any root-only path
// read-write, so it never leaves this process and is destroyed, not merely
// freed, as soon as the descriptor is in hand.
type authzGrant struct {
	ref  C.AuthorizationRef
	form [externalFormLength]byte
	raw  string
	info DeviceInfo
}

func (g *authzGrant) Release() {
	if g.ref != nil {
		C.flashit_authz_destroy(g.ref)
		g.ref = nil
	}
	g.form = [externalFormLength]byte{}
}

// probeRemovableVolumes tells a user who clicked Don't Allow on the Removable
// Volumes prompt apart from the normal state before any sheet is raised. The
// node is root:operator, so an allowed app gets EACCES from the kernel;
// a denied one gets EPERM from the sandbox.
func probeRemovableVolumes(raw string) error {
	fd, err := unix.Open(raw, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err == nil {
		unix.Close(fd)
	}
	return tccProbeResult(raw, err)
}

func tccProbeResult(raw string, err error) error {
	switch {
	case err == nil, errors.Is(err, unix.EACCES):
		return nil
	case errors.Is(err, unix.EPERM):
		return proto.Errorf(proto.CodeTCCDenied, "FlashIt was denied access to removable volumes (%s)", raw)
	}
	return proto.Errorf(proto.CodeInternal, "probe %s: %v", raw, err)
}
