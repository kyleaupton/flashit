package priv

import "context"

// unavailableDiskOps is what Disk returns before EnsureReady has succeeded.
type unavailableDiskOps struct{}

func (unavailableDiskOps) WriteISO(context.Context, string, string, ProgressFunc) error {
	return ErrHelperNotRunning
}

func (unavailableDiskOps) FormatDisk(context.Context, string, string, string) error {
	return ErrHelperNotRunning
}

func (unavailableDiskOps) Eject(context.Context, string) error { return ErrHelperNotRunning }

func (unavailableDiskOps) Unmount(context.Context, string) error { return ErrHelperNotRunning }
