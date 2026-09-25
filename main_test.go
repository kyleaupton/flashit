package main

import "testing"

func TestUpdaterEnabled(t *testing.T) {
	for _, tc := range []struct {
		goos, version string
		want          bool
	}{
		{"darwin", "0.2.1", true},
		{"darwin", "10.20.30", true},
		{"darwin", "dev", false},
		{"darwin", "", false},
		{"darwin", "v0.2.1", false},
		{"darwin", "0.2.1-rc.1", false},
		{"darwin", "0.0.0-dev.3", false},
		{"darwin", "0.2.1-4-gfc6a311", false},
		{"darwin", "0.2", false},
		{"linux", "0.2.1", false},
		{"windows", "0.2.1", false},
	} {
		if got := updaterEnabled(tc.goos, tc.version); got != tc.want {
			t.Errorf("updaterEnabled(%q, %q) = %v, want %v", tc.goos, tc.version, got, tc.want)
		}
	}
}
