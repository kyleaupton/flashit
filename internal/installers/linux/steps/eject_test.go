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

func TestEjectBusyIsAWarning(t *testing.T) {
	for _, tc := range []struct {
		err  error
		warn bool
	}{
		{nil, false},
		{proto.NewError(proto.CodeDeviceBusy, "busy"), true},
		{errors.New("eject failed"), false},
	} {
		svc := &privtest.Service{Ops: privtest.Ops{EjectErr: tc.err}}
		var ev events
		if err := (Eject{}).Run(context.Background(), &FlashContext{TargetDisk: "/dev/sdb", PrivService: svc}, &ev); err != nil {
			t.Fatalf("%v: Eject failed: %v", tc.err, err)
		}
		warned := false
		for _, e := range ev {
			if e.Type == core.EventWarning {
				warned = e.Message == busyWarning
			}
		}
		if warned != tc.warn {
			t.Fatalf("%v: warned %v", tc.err, warned)
		}
	}
}
