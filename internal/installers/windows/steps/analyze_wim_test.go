package steps

import (
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestFindInstallWim(t *testing.T) {
	src := fstest.MapFS{
		"install.wim":         {},
		"SOURCES/boot.wim":    {},
		"SOURCES/Install.WIM": {},
	}
	got, err := findInstallWim(src)
	if err != nil || got != "SOURCES/Install.WIM" {
		t.Fatalf("got %q, %v", got, err)
	}
	s := &FlashContext{USBMountPath: "/media/ESD-USB", InstallWim: got}
	if p := s.splitPrefix(); p != filepath.Join("/media/ESD-USB", "SOURCES", "install") {
		t.Fatalf("split prefix %q", p)
	}

	if _, err := findInstallWim(fstest.MapFS{"sources/install.esd": {}, "sources/install.wim/x": {}}); err == nil {
		t.Fatal("found install.wim where there is none")
	}
}
