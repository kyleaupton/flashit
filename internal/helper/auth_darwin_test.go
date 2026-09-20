//go:build darwin

package helper

import "testing"

// Both requirement shapes must be accepted by Security.framework's parser,
// and a malformed one must keep the helper from starting.
func TestNewAuthParsesRequirement(t *testing.T) {
	for _, c := range []struct{ leaf, team string }{
		{"AD62A0FCDD9DA6353321AF70A1670C9AE2A5499F", ""},
		{"", "AB12CD34EF"},
	} {
		req, err := Requirement("dev.kyleupton.flashit", c.leaf, c.team)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := NewAuth(req); err != nil {
			t.Fatalf("%s: %v", req, err)
		}
	}
	for _, bad := range []string{"", "identifier", `identifier "x" and certificate leaf = H"nothex"`} {
		if _, err := NewAuth(bad); err == nil {
			t.Fatalf("NewAuth(%q) accepted a malformed requirement", bad)
		}
	}
}
