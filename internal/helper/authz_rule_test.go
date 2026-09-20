package helper

import "testing"

func TestCheckRightRule(t *testing.T) {
	good := rightRule{Class: "user", Group: "admin", AuthenticateUser: 1, AllowRoot: 0, Shared: 0, Timeout: 30}
	if err := checkRightRule(good); err != nil {
		t.Fatal(err)
	}
	shorter := good
	shorter.Timeout = 0
	if err := checkRightRule(shorter); err != nil {
		t.Fatal(err)
	}

	bad := map[string]func(r *rightRule){
		"class allow":              func(r *rightRule) { r.Class = "allow" },
		"class rule":               func(r *rightRule) { r.Class = "rule" },
		"class missing":            func(r *rightRule) { r.Class = "" },
		"group staff":              func(r *rightRule) { r.Group = "staff" },
		"group missing":            func(r *rightRule) { r.Group = "" },
		"authenticate-user false":  func(r *rightRule) { r.AuthenticateUser = 0 },
		"authenticate-user absent": func(r *rightRule) { r.AuthenticateUser = -1 },
		"allow-root true":          func(r *rightRule) { r.AllowRoot = 1 },
		"allow-root absent":        func(r *rightRule) { r.AllowRoot = -1 },
		"shared true":              func(r *rightRule) { r.Shared = 1 },
		"shared absent":            func(r *rightRule) { r.Shared = -1 },
		"timeout long":             func(r *rightRule) { r.Timeout = 31 },
		"timeout huge":             func(r *rightRule) { r.Timeout = 86400 },
		"timeout absent":           func(r *rightRule) { r.Timeout = -1 },
	}
	for name, mutate := range bad {
		t.Run(name, func(t *testing.T) {
			r := good
			mutate(&r)
			if err := checkRightRule(r); err == nil {
				t.Fatalf("rule %+v was accepted", r)
			}
		})
	}
}
