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
	writePrompt        = "FlashIt needs to write to a removable drive."
	externalFormLength = 32
)

type authz struct {
	right *C.char
}

// NewAuthorizer writes the rule for WriteRight on every start (root passes
// config.modify without a sheet) and reads it back to check that authd holds
// what was written. Anyone can create a right that does not exist yet, so a
// rule that is merely present is not trusted; the same check runs again
// before every use.
func NewAuthorizer(log *slog.Logger) (Authorizer, error) {
	cright := C.CString(WriteRight)
	cprompt := C.CString(writePrompt)
	var errbuf [256]C.char
	st := C.flashit_authz_right_create(cright, cprompt, writeRightTimeout, &errbuf[0], C.size_t(len(errbuf)))
	C.free(unsafe.Pointer(cprompt))
	if st != 0 {
		C.free(unsafe.Pointer(cright))
		return nil, fmt.Errorf("%w: set right %s: %s", ErrConfig, WriteRight, C.GoString(&errbuf[0]))
	}
	a := authz{right: cright}
	if err := a.verifyRule(); err != nil {
		C.free(unsafe.Pointer(cright))
		return nil, fmt.Errorf("%w: %v", ErrConfig, err)
	}
	log.Info("authorization right in place", "right", WriteRight)
	return a, nil
}

func (a authz) verifyRule() error {
	var r C.flashit_authz_rule
	if st := C.flashit_authz_right_read(a.right, &r); st != 0 {
		return fmt.Errorf("read right %s: OSStatus %d", WriteRight, int(st))
	}
	rule := rightRule{
		Class:            C.GoString(&r.class_[0]),
		Group:            C.GoString(&r.group[0]),
		AuthenticateUser: int(r.authenticate_user),
		AllowRoot:        int(r.allow_root),
		Shared:           int(r.shared),
		Timeout:          int64(r.timeout),
	}
	if err := checkRightRule(rule); err != nil {
		return fmt.Errorf("right %s: %w", WriteRight, err)
	}
	return nil
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
	if err := a.verifyRule(); err != nil {
		return err
	}
	if st := C.flashit_authz_check((*C.uchar)(unsafe.Pointer(&ext[0])), a.right); st != 0 {
		return fmt.Errorf("AuthorizationCopyRights(%s): %d", WriteRight, int(st))
	}
	return nil
}
