package helper

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var (
	bundleIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]*$`)
	sha1Re     = regexp.MustCompile(`^[0-9A-Fa-f]{40}$`)
	teamIDRe   = regexp.MustCompile(`^[A-Z0-9]{10}$`)
)

// Requirement assembles the code-signing requirement the macOS helper holds
// every client to. Exactly one of leafSHA1 (a dev build signed with a
// self-signed certificate) or teamID (a Developer ID build) must be given.
// The pieces arrive as separate -ldflags values because a quoted string does
// not survive the build tooling.
func Requirement(identifier, leafSHA1, teamID string) (string, error) {
	if !bundleIDRe.MatchString(identifier) {
		return "", fmt.Errorf("bundle identifier %q is not valid", identifier)
	}
	switch {
	case leafSHA1 != "" && teamID != "":
		return "", errors.New("both a leaf certificate hash and a team ID were given; use one")
	case leafSHA1 != "":
		if !sha1Re.MatchString(leafSHA1) {
			return "", fmt.Errorf("leaf certificate hash %q is not 40 hex digits", leafSHA1)
		}
		return fmt.Sprintf(`identifier "%s" and certificate leaf = H"%s"`, identifier, strings.ToUpper(leafSHA1)), nil
	case teamID != "":
		if !teamIDRe.MatchString(teamID) {
			return "", fmt.Errorf("team ID %q is not ten upper-case alphanumerics", teamID)
		}
		return fmt.Sprintf(`identifier "%s" and anchor apple generic and certificate leaf[subject.OU] = "%s"`, identifier, teamID), nil
	default:
		return "", errors.New("no leaf certificate hash or team ID; refusing to accept any client")
	}
}
