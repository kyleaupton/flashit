#ifndef FLASHIT_PEER_DARWIN_H
#define FLASHIT_PEER_DARWIN_H
#include <stddef.h>

// Parses a code-signing requirement string. Returns 0 if it is well formed.
int flashit_requirement_validate(const char *requirement, char *err, size_t errlen);

// Reads the peer's audit token from a connected unix socket and checks the
// process it names against requirement. Returns 0 if the requirement is
// satisfied. uid and pid are filled from the token whenever it was readable,
// so a rejection can still be logged with who it was.
int flashit_peer_check(int fd, const char *requirement, int *uid, int *pid, char *err, size_t errlen);

// Authorization Services. All return OSStatus (0 = success).
int flashit_authz_right_create(const char *right, const char *prompt, int timeout_s, char *err, size_t errlen);

// The rule authd holds for a right, reduced to the keys the helper checks.
// Booleans are 1/0, or -1 when the key is absent or not a boolean; timeout
// is -1 when absent or not a number.
typedef struct {
	char class_[32];
	char group[64];
	int authenticate_user;
	int allow_root;
	int shared;
	long timeout;
} flashit_authz_rule;
int flashit_authz_right_read(const char *right, flashit_authz_rule *out);
// Rebuilds an AuthorizationRef from its 32-byte external form and asks authd
// for right, raising the sheet in the user's session if needed. The ref is
// freed before returning.
int flashit_authz_check(const unsigned char *ext32, const char *right);
#endif
