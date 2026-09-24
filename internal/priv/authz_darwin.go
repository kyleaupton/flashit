//go:build darwin

package priv

/*
#cgo CFLAGS: -mmacosx-version-min=13.0
#cgo LDFLAGS: -framework Security
#include "authz_darwin.h"
*/
import "C"

import (
	"encoding/base64"
	"fmt"
	"unsafe"
)

// authorization is an AuthorizationRef the helper redeems for the right on
// the raw device. Token is the base64 external form that goes on the wire.
type authorization struct {
	ref   C.AuthorizationRef
	Token string
}

func newAuthorization() (*authorization, error) {
	a := &authorization{}
	var ext [32]byte
	if st := C.flashit_app_authz_create(&a.ref, (*C.uchar)(unsafe.Pointer(&ext[0]))); st != 0 {
		return nil, fmt.Errorf("AuthorizationCreate: %d", int(st))
	}
	a.Token = base64.StdEncoding.EncodeToString(ext[:])
	return a, nil
}

func (a *authorization) Free() {
	if a.ref != nil {
		C.flashit_app_authz_free(a.ref)
		a.ref = nil
	}
}
