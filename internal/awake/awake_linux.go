package awake

import (
	"os/exec"
	"syscall"
)

// The inhibitor lasts as long as the cat under it, which reads a pipe only
// FlashIt holds open: release closes it, and so does FlashIt dying.
func hold(reason string) (func(), error) {
	cmd := exec.Command("systemd-inhibit", "--what=idle:sleep", "--who=FlashIt", "--why="+reason, "--mode=block", "cat")
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	}, nil
}
