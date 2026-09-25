//go:build !updatertest

package main

import (
	_ "embed"

	"github.com/kyleaupton/flashit/internal/update"
)

// The only trust root for updates. The private half is the Actions secret
// UPDATER_PRIVATE_KEY and never touches the repo.
//
//go:embed build/updater/updater.key.pub
var updaterPublicKey []byte

func updateSource() (manifestURL string, artifactURL func(version, filename string) string, publicKey []byte) {
	return update.ManifestURL, update.ReleaseArtifactURL, updaterPublicKey
}

func harnessHooks(*update.Manager) func(update.State) { return nil }
