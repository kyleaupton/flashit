#include "authz_darwin.h"
#include <string.h>

int flashit_app_authz_create(AuthorizationRef *ref, unsigned char *ext32) {
	AuthorizationRef r = NULL;
	OSStatus st = AuthorizationCreate(NULL, kAuthorizationEmptyEnvironment, kAuthorizationFlagDefaults, &r);
	if (st) return st;
	AuthorizationExternalForm form;
	st = AuthorizationMakeExternalForm(r, &form);
	if (st) { AuthorizationFree(r, kAuthorizationFlagDefaults); return st; }
	memcpy(ext32, form.bytes, kAuthorizationExternalFormLength);
	*ref = r;
	return 0;
}

void flashit_app_authz_free(AuthorizationRef ref) {
	AuthorizationFree(ref, kAuthorizationFlagDestroyRights);
}
