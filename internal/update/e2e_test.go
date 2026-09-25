package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/updater"
)

// These run the real Wails updater and endpoint provider against a local
// server, as a shipped build would, with a key made for the test.

type fakeHost struct{}

func (*fakeHost) Emit(string, ...any) bool                              { return true }
func (*fakeHost) OnEvent(string, func(any)) func()                      { return func() {} }
func (*fakeHost) OpenWindow(updater.WindowOptions) updater.WindowHandle { return nil }
func (*fakeHost) Quit()                                                 {}

type keypair struct {
	priv ed25519.PrivateKey
	pub  []byte
}

func newKeypair(t *testing.T) keypair {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return keypair{priv: priv, pub: pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})}
}

func bundleArchive(t *testing.T, version string) []byte {
	t.Helper()
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>dev.kyleupton.flashit</string>
<key>CFBundleExecutable</key><string>FlashIt</string>
<key>CFBundleShortVersionString</key><string>%s</string>
</dict></plist>`, version)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, d := range []string{"FlashIt.app/", "FlashIt.app/Contents/", "FlashIt.app/Contents/MacOS/"} {
		if err := tw.WriteHeader(&tar.Header{Name: d, Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"FlashIt.app/Contents/Info.plist":    plist,
		"FlashIt.app/Contents/MacOS/FlashIt": "#!/bin/sh\n",
	}
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type artifact struct {
	URL           string `json:"url"`
	Platform      string `json:"platform"`
	Arch          string `json:"arch"`
	Size          int64  `json:"size"`
	DigestAlgo    string `json:"digestAlgo,omitempty"`
	Digest        string `json:"digest,omitempty"`
	SignatureAlgo string `json:"signatureAlgo,omitempty"`
	Signature     string `json:"signature,omitempty"`
}

// signed is the manifest entry `wails3 updater manifest -key` writes.
func signed(url, platform string, body []byte, key ed25519.PrivateKey) artifact {
	sum := sha512.Sum512(body)
	sig, err := key.Sign(nil, sum[:], &ed25519.Options{Hash: crypto.SHA512})
	if err != nil {
		panic(err)
	}
	return artifact{
		URL: url, Platform: platform, Arch: runtime.GOARCH, Size: int64(len(body)),
		DigestAlgo: "sha512", Digest: base64.StdEncoding.EncodeToString(sum[:]),
		SignatureAlgo: "ed25519ph", Signature: base64.StdEncoding.EncodeToString(sig),
	}
}

type server struct {
	*httptest.Server
	manifest  []byte
	files     map[string][]byte
	downloads atomic.Int32
}

func newServer(t *testing.T) *server {
	s := &server{files: map[string][]byte{}}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/manifest.json" {
			_, _ = w.Write(s.manifest)
			return
		}
		body, ok := s.files[strings.TrimPrefix(r.URL.Path, "/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		s.downloads.Add(1)
		_, _ = w.Write(body)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *server) publish(t *testing.T, version string, arts ...artifact) {
	t.Helper()
	m, err := json.Marshal(map[string]any{"schemaVersion": 1, "version": version, "notes": "What's new", "artifacts": arts})
	if err != nil {
		t.Fatal(err)
	}
	s.manifest = m
}

func (s *server) config(goos string, key []byte) Config {
	return Config{
		GOOS:        goos,
		Version:     "0.0.1",
		ManifestURL: s.URL + "/manifest.json",
		ArtifactURL: func(_, filename string) string { return s.URL + "/" + filename },
		PublicKey:   key,
		Gate:        &fakeGate{},
	}
}

func archiveName(version string) string {
	return fmt.Sprintf("flashit-%s-darwin-%s.tar.gz", version, runtime.GOARCH)
}

func TestEndToEnd_MacInstallsSignedUpdate(t *testing.T) {
	key := newKeypair(t)
	srv := newServer(t)
	body := bundleArchive(t, "0.0.2")
	name := archiveName("0.0.2")
	srv.files[name] = body
	srv.publish(t, "0.0.2", signed(srv.URL+"/"+name, "darwin", body, key.priv))

	u := updater.New(&fakeHost{})
	m, err := setup(u, srv.config("darwin", key.pub), checkBundleInfo, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := m.Check(context.Background())
	if s.Status != StatusReady || s.Version != "0.0.2" {
		t.Fatalf("state = %+v, want ready 0.0.2", s)
	}
	staged := u.DownloadedPath()
	t.Cleanup(func() { discard(staged) })
	if filepath.Base(staged) != "FlashIt.app" {
		t.Fatalf("staged %q", staged)
	}
	if _, err := os.Stat(filepath.Join(staged, "Contents", "MacOS", "FlashIt")); err != nil {
		t.Fatal(err)
	}
}

func TestEndToEnd_MacRefuses(t *testing.T) {
	key := newKeypair(t)
	other := newKeypair(t)
	good := bundleArchive(t, "0.0.2")
	name := archiveName("0.0.2")

	cases := map[string]func(srv *server){
		"tampered archive": func(srv *server) {
			bad := bytes.Clone(good)
			bad[len(bad)/2] ^= 0xff
			srv.files[name] = bad
			srv.publish(t, "0.0.2", signed(srv.URL+"/"+name, "darwin", good, key.priv))
		},
		"signed with another key": func(srv *server) {
			srv.files[name] = good
			srv.publish(t, "0.0.2", signed(srv.URL+"/"+name, "darwin", good, other.priv))
		},
		"unsigned": func(srv *server) {
			srv.files[name] = good
			a := signed(srv.URL+"/"+name, "darwin", good, key.priv)
			a.Signature, a.SignatureAlgo = "", ""
			srv.publish(t, "0.0.2", a)
		},
		"no digest or signature": func(srv *server) {
			srv.files[name] = good
			srv.publish(t, "0.0.2", artifact{URL: srv.URL + "/" + name, Platform: "darwin", Arch: runtime.GOARCH, Size: int64(len(good))})
		},
		"older release replayed as newer": func(srv *server) {
			old := bundleArchive(t, "0.0.1")
			n := archiveName("9.9.9")
			srv.files[n] = old
			srv.publish(t, "9.9.9", signed(srv.URL+"/"+n, "darwin", old, key.priv))
		},
		"signed file under another name": func(srv *server) {
			srv.files["flashit_0.0.2_arm64.deb"] = good
			srv.publish(t, "0.0.2", signed(srv.URL+"/flashit_0.0.2_arm64.deb", "darwin", good, key.priv))
		},
		"artifact elsewhere": func(srv *server) {
			srv.files["x/"+name] = good
			srv.publish(t, "0.0.2", signed(srv.URL+"/x/"+name, "darwin", good, key.priv))
		},
		"prerelease version": func(srv *server) {
			n := archiveName("0.0.2-rc.1")
			srv.files[n] = good
			srv.publish(t, "0.0.2-rc.1", signed(srv.URL+"/"+n, "darwin", good, key.priv))
		},
		"longer than the manifest says": func(srv *server) {
			srv.files[name] = good
			a := signed(srv.URL+"/"+name, "darwin", good, key.priv)
			a.Size--
			srv.publish(t, "0.0.2", a)
		},
	}
	for label, arrange := range cases {
		t.Run(label, func(t *testing.T) {
			srv := newServer(t)
			arrange(srv)
			u := updater.New(&fakeHost{})
			m, err := setup(u, srv.config("darwin", key.pub), checkBundleInfo, nil)
			if err != nil {
				t.Fatal(err)
			}
			s := m.Check(context.Background())
			if s.Status != StatusError {
				t.Fatalf("state = %+v, want error", s)
			}
			t.Log(s.Error)
			if err := m.Restart(context.Background()); err == nil {
				t.Fatal("Restart allowed after a refused update")
			}
			if p := u.DownloadedPath(); p != "" {
				discard(p)
			}
		})
	}
}

func TestEndToEnd_LinuxNotifiesWithoutDownloading(t *testing.T) {
	key := newKeypair(t)
	srv := newServer(t)
	deb := []byte("not really a deb")
	srv.files["flashit_0.0.2_amd64.deb"] = deb
	srv.publish(t, "0.0.2", signed(srv.URL+"/flashit_0.0.2_amd64.deb", "linux", deb, key.priv))

	var emitted []State
	cfg := srv.config("linux", key.pub)
	cfg.Emit = func(s State) { emitted = append(emitted, s) }
	u := updater.New(&fakeHost{})
	m, err := setup(u, cfg, CheckBundle, CanInstall)
	if err != nil {
		t.Fatal(err)
	}
	s := m.Check(context.Background())
	if s.Status != StatusAvailable || s.Version != "0.0.2" || s.Notes != "What's new" {
		t.Fatalf("state = %+v, want available 0.0.2", s)
	}
	if n := srv.downloads.Load(); n != 0 {
		t.Fatalf("%d downloads, want none", n)
	}
	if u.DownloadedPath() != "" {
		t.Fatal("something was staged")
	}
	// Even driven directly, the provider will not fetch on Linux.
	if err := u.DownloadAndInstall(context.Background()); err == nil {
		t.Fatal("DownloadAndInstall succeeded on Linux")
	}
	if n := srv.downloads.Load(); n != 0 {
		t.Fatalf("%d downloads, want none", n)
	}
}
