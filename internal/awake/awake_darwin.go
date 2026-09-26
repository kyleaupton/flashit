package awake

import (
	"os"
	"os/exec"
	"strconv"
)

// caffeinate -w exits by itself if FlashIt dies without releasing.
func hold(string) (func(), error) {
	cmd := exec.Command("/usr/bin/caffeinate", "-i", "-w", strconv.Itoa(os.Getpid()))
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}, nil
}
