package helper

import "fmt"

// WriteRight is the Authorization Services right every write_image and
// format_disk must hold on macOS.
const WriteRight = "dev.kyleupton.flashit.write"

// writeRightTimeout bounds the credential's life; the helper prompts itself,
// so this is a ceiling, not a session cache.
const writeRightTimeout = 30

// rightRule is the authorization database rule for WriteRight as read back
// from authd. Tri-state fields are 1, 0, or -1 for absent.
type rightRule struct {
	Class, Group     string
	AuthenticateUser int
	AllowRoot        int
	Shared           int
	Timeout          int64
}

// checkRightRule refuses any rule that would grant the right without an
// admin authenticating for it. config.add is open to everyone, so a rule
// found in the database may have been planted before the helper ever ran.
func checkRightRule(r rightRule) error {
	switch {
	case r.Class != "user":
		return fmt.Errorf("rule class is %q, want user", r.Class)
	case r.Group != "admin":
		return fmt.Errorf("rule group is %q, want admin", r.Group)
	case r.AuthenticateUser != 1:
		return fmt.Errorf("rule does not require authenticate-user")
	case r.AllowRoot != 0:
		return fmt.Errorf("rule does not set allow-root to false")
	case r.Shared != 0:
		return fmt.Errorf("rule does not set shared to false")
	case r.Timeout < 0 || r.Timeout > writeRightTimeout:
		return fmt.Errorf("rule timeout is %d, want 0..%d", r.Timeout, writeRightTimeout)
	}
	return nil
}
