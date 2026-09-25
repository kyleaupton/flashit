//go:build updatertest

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "embed"

	"github.com/kyleaupton/flashit/internal/logger"
	"github.com/kyleaupton/flashit/internal/update"
)

// Local updater harness (build/updater/harness.sh): a throwaway key the
// script generates, and a manifest served from localhost. Release
// workflows never build with this tag, and check their binaries for the
// FLASHIT_UPDATE_URL string below.
//
//go:embed bin/updatertest/key/updater-test.key.pub
var updaterPublicKey []byte

const harnessManifestURL = "http://127.0.0.1:8765/manifest.json"

func updateSource() (string, func(version, filename string) string, []byte) {
	manifest := os.Getenv("FLASHIT_UPDATE_URL")
	if manifest == "" {
		// A relaunch through open(1) drops the environment.
		manifest = harnessManifestURL
	}
	base := manifest[:strings.LastIndex(manifest, "/")+1]
	return manifest, func(_, filename string) string { return base + filename }, updaterPublicKey
}

// harnessHooks logs every state to $TMPDIR/flashit-updatertest.log, which
// survives the relaunch, and with FLASHIT_UPDATE_AUTORESTART=1 restarts as
// soon as an update is ready, through the same gate as the button.
func harnessHooks(mgr *update.Manager) func(update.State) {
	path := filepath.Join(os.TempDir(), "flashit-updatertest.log")
	write := func(line string) {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return
		}
		defer f.Close()
		fmt.Fprintf(f, "%s pid=%d version=%s %s\n", time.Now().Format(time.RFC3339), os.Getpid(), Version, line)
	}
	manifest, _, _ := updateSource()
	logger.Warn("updater harness build", "manifest", manifest)
	write("started mode=" + string(mgr.Mode()) + " manifest=" + manifest)

	autorestart := os.Getenv("FLASHIT_UPDATE_AUTORESTART") == "1"
	var once sync.Once
	return func(s update.State) {
		write(fmt.Sprintf("status=%s found=%s error=%q", s.Status, s.Version, s.Error))
		if autorestart && s.Status == update.StatusReady {
			once.Do(func() {
				go func() {
					write(fmt.Sprintf("autorestart err=%v", mgr.Restart(context.Background())))
				}()
			})
		}
	}
}
