// Package awake keeps the machine from sleeping while a drive is written.
package awake

import (
	"sync"

	"github.com/kyleaupton/flashit/internal/logger"
)

// Hold keeps the system from idle sleep until release is called. A hold
// that cannot be taken is logged and otherwise ignored: it never stops a
// flash. release may be called more than once.
func Hold(reason string) (release func()) {
	r, err := hold(reason)
	if err != nil {
		logger.Warn("could not keep the system awake", "error", err)
		return func() {}
	}
	var once sync.Once
	return func() { once.Do(r) }
}
