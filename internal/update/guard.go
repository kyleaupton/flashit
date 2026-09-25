package update

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/wailsapp/wails/v3/pkg/updater"
)

// maxArchiveSize bounds a download before its signature can be checked,
// which only happens once the last byte is in.
const maxArchiveSize = 512 << 20

// artifactURLKey is where the endpoint provider keeps the resolved artifact
// URL. If Wails renames it the lookup comes back empty and every release is
// refused, which is the failure we want.
const artifactURLKey = "endpoint.artifact.url"

// guard wraps the endpoint provider with what the Wails updater leaves to
// the app. The manifest itself is unsigned, only each artifact's bytes are:
// the updater installs an artifact with no signature, or a digest alone, and
// takes the version, file name and URL from the manifest as given.
type guard struct {
	next        updater.Provider
	goos        string
	artifactURL func(version, filename string) string
}

func (g *guard) Name() string { return g.next.Name() }

func (g *guard) Check(ctx context.Context, req updater.CheckRequest) (*updater.Release, error) {
	rel, err := g.next.Check(ctx, req)
	if err != nil || rel == nil {
		return rel, err
	}
	if err := g.validate(rel); err != nil {
		return nil, fmt.Errorf("refusing update: %w", err)
	}
	return rel, nil
}

func (g *guard) validate(rel *updater.Release) error {
	if !releaseVersion.MatchString(rel.Version) {
		return fmt.Errorf("manifest version %q is not MAJOR.MINOR.PATCH", rel.Version)
	}
	v := rel.Verification
	if v == nil || v.SignatureAlgo != "ed25519ph" || len(v.Signature) == 0 ||
		v.DigestAlgo != "sha512" || len(v.Digest) == 0 {
		return errors.New("artifact is not signed with ed25519ph over sha512")
	}
	if g.goos != "darwin" {
		return nil
	}

	// A signed file of ours under another name (a .deb, or last year's
	// archive) would otherwise be staged and swapped in for the bundle.
	want := fmt.Sprintf("flashit-%s-darwin-%s.tar.gz", rel.Version, rel.Artifact.Arch)
	if rel.Artifact.Filename != want {
		return fmt.Errorf("artifact is named %q, want %q", rel.Artifact.Filename, want)
	}
	url, _ := rel.Metadata[artifactURLKey].(string)
	if url != g.artifactURL(rel.Version, want) {
		return fmt.Errorf("artifact URL %q is not where %s is published", url, rel.Version)
	}
	if rel.Artifact.Size <= 0 || rel.Artifact.Size > maxArchiveSize {
		return fmt.Errorf("artifact size %d is out of range", rel.Artifact.Size)
	}
	return nil
}

func (g *guard) Download(ctx context.Context, rel *updater.Release, dst io.Writer, onProgress func(int64, int64)) error {
	if g.goos != "darwin" {
		return errors.New("updates are only downloaded on macOS")
	}
	if err := g.validate(rel); err != nil {
		return err
	}
	w := &cappedWriter{w: dst, left: rel.Artifact.Size}
	if err := g.next.Download(ctx, rel, w, onProgress); err != nil {
		return err
	}
	if w.left != 0 {
		return fmt.Errorf("artifact is %d bytes short of the size in the manifest", w.left)
	}
	return nil
}

type cappedWriter struct {
	w    io.Writer
	left int64
}

func (c *cappedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > c.left {
		return 0, errors.New("artifact is larger than the size in the manifest")
	}
	n, err := c.w.Write(p)
	c.left -= int64(n)
	return n, err
}
