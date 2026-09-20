//go:build darwin

package helper

/*
#cgo CFLAGS: -mmacosx-version-min=13.0
#cgo LDFLAGS: -framework Security -framework CoreFoundation -framework DiskArbitration -lbsm
#include <stdlib.h>
#include "peer_darwin.h"
*/
import "C"

import (
	"fmt"
	"net"
	"unsafe"
)

type codeAuth struct {
	requirement *C.char
}

// NewAuth accepts only peers whose code signature satisfies requirement (see
// Requirement). The string must parse or the helper does not start.
func NewAuth(requirement string) (Auth, error) {
	creq := C.CString(requirement)
	var errbuf [512]C.char
	if rc := C.flashit_requirement_validate(creq, &errbuf[0], C.size_t(len(errbuf))); rc != 0 {
		C.free(unsafe.Pointer(creq))
		return nil, fmt.Errorf("code requirement %q: %s (%d)", requirement, C.GoString(&errbuf[0]), int(rc))
	}
	return codeAuth{requirement: creq}, nil
}

func (a codeAuth) Authenticate(conn net.Conn) (Peer, error) {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return Peer{}, fmt.Errorf("%w: not a unix socket", ErrUnauthorized)
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return Peer{}, fmt.Errorf("%w: %v", ErrUnauthorized, err)
	}
	var (
		uid, pid C.int
		rc       C.int
		errbuf   [512]C.char
	)
	if err := raw.Control(func(fd uintptr) {
		rc = C.flashit_peer_check(C.int(fd), a.requirement, &uid, &pid, &errbuf[0], C.size_t(len(errbuf)))
	}); err != nil {
		return Peer{}, fmt.Errorf("%w: %v", ErrUnauthorized, err)
	}
	if rc != 0 {
		return Peer{}, fmt.Errorf("%w: pid %d uid %d: %s (%d)", ErrUnauthorized, int(pid), int(uid), C.GoString(&errbuf[0]), int(rc))
	}
	if uid < 0 {
		return Peer{}, fmt.Errorf("%w: no peer uid", ErrUnauthorized)
	}
	return Peer{UID: int(uid)}, nil
}
