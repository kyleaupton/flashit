package update

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
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
	args := []string{"--verify", "--deep", "--strict"}
	if running, err := runningBundle(); err == nil {
		if team := signingTeam(running); team != "" {
			args = append(args, fmt.Sprintf(`-R=anchor apple generic and certificate leaf[subject.OU] = "%s"`, team))
		}
	}
	out, err := exec.Command("/usr/bin/codesign", append(args, path)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("codesign rejected the update: %s", strings.TrimSpace(string(out)))
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

// signingTeam is the Team ID a bundle is signed with, or "" when it is ad
// hoc or unreadable.
func signingTeam(bundle string) string {
	out, _ := exec.Command("/usr/bin/codesign", "-dv", "--verbose=2", bundle).CombinedOutput()
	for _, line := range bytes.Split(out, []byte("\n")) {
		if v, ok := bytes.CutPrefix(line, []byte("TeamIdentifier=")); ok && teamID.Match(v) {
			return string(v)
		}
	}
	return ""
}
