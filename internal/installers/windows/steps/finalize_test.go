package steps

import (
	"context"
	"errors"
	"testing"

	"github.com/kyleaupton/flashit/internal/core"
	"github.com/kyleaupton/flashit/internal/priv/privtest"
	"github.com/kyleaupton/flashit/internal/proto"
)

type events []core.Event

func (ev *events) Emit(e core.Event) { *ev = append(*ev, e) }

func (ev events) warnings() []string {
	var out []string
	for _, e := range ev {
		if e.Type == core.EventWarning {
			out = append(out, e.Message)
		}
	}
	return out
}

func TestFinalizeEjectsThroughHelper(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		warn bool
	}{
		{"ejected", nil, false},
		{"busy", proto.NewError(proto.CodeDeviceBusy, "unmount /dev/sdb1: device or resource busy"), true},
		{"other", errors.New("helper connection lost"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &privtest.Service{Ops: privtest.Ops{EjectErr: tc.err}}
			state := &FlashContext{TargetDisk: "/dev/sdb", PrivService: svc, HelperMounts: true}
			var ev events
			if err := (Finalize{}).Run(context.Background(), state, &ev); err != nil {
				t.Fatalf("Finalize failed: %v", err)
			}
			if len(svc.Ops.Ejected) != 1 || svc.Ops.Ejected[0] != "/dev/sdb" {
				t.Fatalf("ejected %v", svc.Ops.Ejected)
			}
			w := ev.warnings()
			if tc.warn != (len(w) == 1) || len(w) > 1 {
				t.Fatalf("warnings %q", w)
			}
			if tc.warn && w[0] != busyWarning {
				t.Fatalf("warning %q", w[0])
			}
		})
	}
}

func TestFormatUSBCleanupUnmounts(t *testing.T) {
	svc := &privtest.Service{}
	state := &FlashContext{TargetDisk: "/dev/sdb", PrivService: svc, HelperMounts: true}
	var ev events
	if err := (FormatUSB{}).Cleanup(context.Background(), state, &ev); err != nil {
		t.Fatal(err)
	}
	if len(svc.Ops.Unmounted) != 1 || svc.Ops.Unmounted[0] != "/dev/sdb" {
		t.Fatalf("unmounted %v", svc.Ops.Unmounted)
	}

	svc.Ops.UnmountErr = proto.NewError(proto.CodeDeviceBusy, "busy")
	if err := (FormatUSB{}).Cleanup(context.Background(), state, &ev); err == nil {
		t.Fatal("a failed unmount was not reported")
	}

	// Where the OS mounted the volume for the user, the helper leaves it be.
	svc = &privtest.Service{}
	state = &FlashContext{TargetDisk: "/dev/disk4", PrivService: svc}
	if err := (FormatUSB{}).Cleanup(context.Background(), state, &ev); err != nil {
		t.Fatal(err)
	}
	if len(svc.Ops.Unmounted) != 0 {
		t.Fatalf("unmounted %v without HelperMounts", svc.Ops.Unmounted)
	}
}
