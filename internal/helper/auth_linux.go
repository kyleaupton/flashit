//go:build linux

package helper

import (
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

type peerAuth struct {
	uid int
}

// NewAuth accepts only peers whose SO_PEERCRED uid equals uid. A negative uid
// (PKEXEC_UID unset) refuses every connection.
func NewAuth(uid int) (Auth, error) {
	return peerAuth{uid: uid}, nil
}

func (a peerAuth) Authenticate(conn net.Conn) (Peer, error) {
	if a.uid < 0 {
		return Peer{}, fmt.Errorf("%w: no expected caller uid", ErrUnauthorized)
	}
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return Peer{}, fmt.Errorf("%w: not a unix socket", ErrUnauthorized)
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return Peer{}, fmt.Errorf("%w: %v", ErrUnauthorized, err)
	}
	var cred *unix.Ucred
	var cerr error
	if err := raw.Control(func(fd uintptr) {
		cred, cerr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return Peer{}, fmt.Errorf("%w: %v", ErrUnauthorized, err)
	}
	if cerr != nil {
		return Peer{}, fmt.Errorf("%w: SO_PEERCRED: %v", ErrUnauthorized, cerr)
	}
	if int(cred.Uid) != a.uid {
		return Peer{}, fmt.Errorf("%w: peer uid %d, expected %d", ErrUnauthorized, cred.Uid, a.uid)
	}
	return Peer{UID: int(cred.Uid)}, nil
}
