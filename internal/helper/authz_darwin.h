#ifndef FLASHIT_AUTHZ_DARWIN_H
#define FLASHIT_AUTHZ_DARWIN_H
#include <Security/Authorization.h>

// Thin wrappers over Authorization Services; every function returns the
// OSStatus of the call it makes (0 = success).
int flashit_authz_from_external(const unsigned char *in32, AuthorizationRef *ref);
// Asks authd for right with ExtendRights|InteractionAllowed, which raises the
// password sheet in the user's session when the ref does not hold it yet.
int flashit_authz_copy_rights(AuthorizationRef ref, const char *right);
int flashit_authz_external(AuthorizationRef ref, unsigned char *out32);
// Frees the ref with DestroyRights, invalidating the credential for every
// holder of its external form.
void flashit_authz_destroy(AuthorizationRef ref);
#endif
