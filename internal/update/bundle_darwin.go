package update

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/sys/unix"
)

var teamID = regexp.MustCompile(`^[A-Z0-9]{10}$`)

// CheckBundle vets the unpacked update before the app restarts into it: the
// right bundle at the right version, a valid code signature, and, when this
// build is signed by a team, the same team. A broken bundle swapped into
// /Applications would not launch, and nothing would put the old one back.
func CheckBundle(path, version string) error {
	if err := checkBundleInfo(path, version); err != nil {
		return err
	}
	running, err := runningBundle()
	if err != nil {
		return err
	}
	team, err := signingTeam(running)
	if err != nil {
		return err
	}
	args := []string{"--verify", "--deep", "--strict"}
	if team != "" {
		args = append(args, fmt.Sprintf(`-R=anchor apple generic and certificate leaf[subject.OU] = "%s"`, team))
	}
	out, err := exec.Command("/usr/bin/codesign", append(args, path)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("codesign rejected the update: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// CanInstall fails when the Wails updater could not swap this copy: it
// renames a FlashIt.app.bak beside the bundle and replaces the bundle, so
// a DMG, a translocated copy or a folder the user cannot write would fail
// after the app had already quit.
func CanInstall() error {
	running, err := runningBundle()
	if err != nil {
		return err
	}
	if strings.Contains(running, "/AppTranslocation/") {
		return fmt.Errorf("%s is translocated; move it to Applications", running)
	}
	for _, p := range []string{filepath.Dir(running), running} {
		if err := unix.Access(p, unix.W_OK); err != nil {
			return fmt.Errorf("%s is not writable: %w", p, err)
		}
	}
	return nil
}

func runningBundle() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	for dir := filepath.Dir(exe); dir != "/" && dir != "."; dir = filepath.Dir(dir) {
		if strings.HasSuffix(dir, ".app") {
			return dir, nil
		}
	}
	return "", fmt.Errorf("%s is not in a bundle", exe)
}

// signingTeam is the Team ID the bundle is signed with, "" when it is ad
// hoc, and an error when neither can be read: the team check must not
// quietly drop away.
func signingTeam(bundle string) (string, error) {
	out, err := exec.Command("/usr/bin/codesign", "-dv", "--verbose=2", bundle).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("codesign cannot read this build's signature: %s", strings.TrimSpace(string(out)))
	}
	for _, line := range bytes.Split(out, []byte("\n")) {
		if v, ok := bytes.CutPrefix(line, []byte("TeamIdentifier=")); ok && teamID.Match(v) {
			return string(v), nil
		}
	}
	if bytes.Contains(out, []byte("Signature=adhoc")) {
		return "", nil
	}
	return "", errors.New("this build is signed, but its Team ID cannot be read")
}
