package helper

import (
	"testing"
	"time"
)

func TestThrottle(t *testing.T) {
	th := throttle{interval: 100 * time.Millisecond}
	t0 := time.Unix(0, 0)
	steps := []struct {
		at   time.Duration
		want bool
	}{
		{0, true},
		{10 * time.Millisecond, false},
		{99 * time.Millisecond, false},
		{100 * time.Millisecond, true},
		{150 * time.Millisecond, false},
		{250 * time.Millisecond, true},
	}
	for _, s := range steps {
		if got := th.allow(t0.Add(s.at)); got != s.want {
			t.Fatalf("allow at %v = %v, want %v", s.at, got, s.want)
		}
	}
}
