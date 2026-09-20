//go:build darwin

package priv

/*
#cgo CFLAGS: -mmacosx-version-min=13.0 -fobjc-arc
#cgo LDFLAGS: -framework Foundation -framework ServiceManagement -framework Security
#include <stdlib.h>
#include "shim_darwin.h"
*/
import "C"

import (
	"encoding/base64"
	"fmt"
	"unsafe"
)

// helperPlist names the launchd plist in Contents/Library/LaunchDaemons.
const helperPlist = "dev.kyleupton.flashit.helper.plist"

type smStatus int

const (
	smNotRegistered    smStatus = 0
	smEnabled          smStatus = 1
	smRequiresApproval smStatus = 2
	smNotFound         smStatus = 3
)

func (s smStatus) String() string {
	switch s {
	case smNotRegistered:
		return "notRegistered"
	case smEnabled:
		return "enabled"
	case smRequiresApproval:
		return "requiresApproval"
	case smNotFound:
		return "notFound"
	}
	return fmt.Sprintf("status(%d)", int(s))
}

func smDaemonStatus() smStatus {
	cplist := C.CString(helperPlist)
	defer C.free(unsafe.Pointer(cplist))
	return smStatus(C.flashit_sm_status(cplist))
}

func smRegister() error {
	cplist := C.CString(helperPlist)
	defer C.free(unsafe.Pointer(cplist))
	var errbuf [512]C.char
	if rc := C.flashit_sm_register(cplist, &errbuf[0], C.size_t(len(errbuf))); rc != 0 {
		return fmt.Errorf("register helper: %s", C.GoString(&errbuf[0]))
	}
	return nil
}

func smUnregister() error {
	cplist := C.CString(helperPlist)
	defer C.free(unsafe.Pointer(cplist))
	var errbuf [512]C.char
	if rc := C.flashit_sm_unregister(cplist, &errbuf[0], C.size_t(len(errbuf))); rc != 0 {
		return fmt.Errorf("unregister helper: %s", C.GoString(&errbuf[0]))
	}
	return nil
}

func smOpenSettings() { C.flashit_sm_open_settings() }

// authorization is an AuthorizationRef the helper will redeem for the write
// right. Token is the base64 external form that goes on the wire.
type authorization struct {
	ref   unsafe.Pointer
	Token string
}

func newAuthorization() (*authorization, error) {
	var (
		ref unsafe.Pointer
		ext [32]byte
	)
	if st := C.flashit_authz_create(&ref, (*C.uchar)(unsafe.Pointer(&ext[0]))); st != 0 {
		return nil, fmt.Errorf("AuthorizationCreate: %d", int(st))
	}
	return &authorization{ref: ref, Token: base64.StdEncoding.EncodeToString(ext[:])}, nil
}

func (a *authorization) Free() {
	if a.ref != nil {
		C.flashit_authz_free(a.ref)
		a.ref = nil
	}
}
