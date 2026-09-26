package awake

import (
	"runtime"

	"golang.org/x/sys/windows"
)

var setThreadExecutionState = windows.NewLazySystemDLL("kernel32.dll").NewProc("SetThreadExecutionState")

const (
	esContinuous     = 0x80000000
	esSystemRequired = 0x00000001
)

// The execution state belongs to the calling thread, so one goroutine
// pinned to its thread sets it and clears it.
func hold(string) (func(), error) {
	done := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if r, _, err := setThreadExecutionState.Call(esContinuous | esSystemRequired); r == 0 {
			result <- err
			return
		}
		result <- nil
		<-done
		setThreadExecutionState.Call(esContinuous)
	}()
	if err := <-result; err != nil {
		return nil, err
	}
	return func() { close(done) }, nil
}
