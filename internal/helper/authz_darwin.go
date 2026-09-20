//go:build darwin

package helper

/*
#include <stdlib.h>
#include "peer_darwin.h"
*/
import "C"

import (
	"encoding/base64"
	"fmt"
	"log/slog"
	"unsafe"

	"github.com/kyleaupton/flashit/internal/proto"
)

const (
	// WriteRight is the Authorization Services right every write_image and
	// format_disk must hold.
	WriteRight = "dev.kyleupton.flashit.write"

	writePrompt = "FlashIt needs to write to a removable drive."
	// Window between the sheet and the helper's check, not a session cache;
	// the helper prompts itself, so it only has to be non-zero.
	writeRightTimeout = 30

	externalFormLength     = 32
	errAuthorizationDenied = -60005
)

type authz struct {
	right *C.char
}

// NewAuthorizer creates WriteRight in the authorization database the first
// time the helper runs (it is root, so no sheet) and returns an Authorizer
// that redeems the external form a request carries. The prompt for the
// right is raised by authd in the user's session when the ref has not been
// authorized yet.
func NewAuthorizer(log *slog.Logger) (Authorizer, error) {
	cright := C.CString(WriteRight)
	if st := C.flashit_authz_right_exists(cright); st != 0 {
		if st != errAuthorizationDenied {
			C.free(unsafe.Pointer(cright))
			return nil, fmt.Errorf("AuthorizationRightGet(%s): %d", WriteRight, int(st))
		}
		cprompt := C.CString(writePrompt)
		var errbuf [256]C.char
		st := C.flashit_authz_right_create(cright, cprompt, writeRightTimeout, &errbuf[0], C.size_t(len(errbuf)))
		C.free(unsafe.Pointer(cprompt))
		if st != 0 {
			C.free(unsafe.Pointer(cright))
			return nil, fmt.Errorf("create right %s: %s", WriteRight, C.GoString(&errbuf[0]))
		}
		log.Info("created authorization right", "right", WriteRight)
	}
	return authz{right: cright}, nil
}

func (a authz) Authorize(op proto.Op, token string) error {
	if token == "" {
		return fmt.Errorf("%s carries no authorization", op)
	}
	ext, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return fmt.Errorf("authorization is not base64: %v", err)
	}
	if len(ext) != externalFormLength {
		return fmt.Errorf("authorization is %d bytes, not %d", len(ext), externalFormLength)
	}
	if st := C.flashit_authz_check((*C.uchar)(unsafe.Pointer(&ext[0])), a.right); st != 0 {
		return fmt.Errorf("AuthorizationCopyRights(%s): %d", WriteRight, int(st))
	}
	return nil
}
