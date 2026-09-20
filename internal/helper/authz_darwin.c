#include "authz_darwin.h"
#include <string.h>

int flashit_authz_from_external(const unsigned char *in32, AuthorizationRef *ref) {
	AuthorizationExternalForm form;
	memcpy(form.bytes, in32, kAuthorizationExternalFormLength);
	return AuthorizationCreateFromExternalForm(&form, ref);
}

int flashit_authz_copy_rights(AuthorizationRef ref, const char *right) {
	AuthorizationItem item = {right, 0, NULL, 0};
	AuthorizationRights rights = {1, &item};
	return AuthorizationCopyRights(ref, &rights, kAuthorizationEmptyEnvironment,
		kAuthorizationFlagExtendRights | kAuthorizationFlagInteractionAllowed, NULL);
}

int flashit_authz_external(AuthorizationRef ref, unsigned char *out32) {
	AuthorizationExternalForm form;
	OSStatus st = AuthorizationMakeExternalForm(ref, &form);
	if (st == errAuthorizationSuccess) memcpy(out32, form.bytes, kAuthorizationExternalFormLength);
	return st;
}

void flashit_authz_destroy(AuthorizationRef ref) {
	AuthorizationFree(ref, kAuthorizationFlagDestroyRights);
}
