// Package privtest holds a fake PrivilegedService for installer step tests.
package privtest

import (
	"context"
	"sync"

	"github.com/kyleaupton/flashit/internal/priv"
)

// Service is a PrivilegedService whose Disk is always Ops.
type Service struct {
	Ops Ops

	mu       sync.Mutex
	held     int
	released int
}

func (s *Service) EnsureReady(context.Context) error { return nil }
func (s *Service) Disk() priv.DiskOps                { return &s.Ops }
func (s *Service) Shutdown(context.Context) error    { return nil }

func (s *Service) Hold() func() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.held++
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.released++
		})
	}
}

// Holds reports how many holds were taken and released.
func (s *Service) Holds() (held, released int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.held, s.released
}

// Ops records Eject and Unmount and answers them with EjectErr and
// UnmountErr. OnUnmount, when set, runs before Unmount records its call.
type Ops struct {
	mu         sync.Mutex
	EjectErr   error
	UnmountErr error
	OnUnmount  func(device string)
	Ejected    []string
	Unmounted  []string
}

func (o *Ops) WriteISO(context.Context, string, string, priv.ProgressFunc) error { return nil }
func (o *Ops) FormatDisk(context.Context, string, string, string) error          { return nil }

func (o *Ops) Eject(_ context.Context, device string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.Ejected = append(o.Ejected, device)
	return o.EjectErr
}

func (o *Ops) Unmount(_ context.Context, device string) error {
	if o.OnUnmount != nil {
		o.OnUnmount(device)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.Unmounted = append(o.Unmounted, device)
	return o.UnmountErr
}
