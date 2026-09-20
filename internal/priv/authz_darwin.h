#ifndef FLASHIT_APP_AUTHZ_DARWIN_H
#define FLASHIT_APP_AUTHZ_DARWIN_H
#include <Security/Authorization.h>

// Creates an AuthorizationRef that holds no rights yet and writes its
// 32-byte external form. The helper redeems the form for the right on the
// raw device, which raises the sheet named after this app. Returns OSStatus.
int flashit_app_authz_create(AuthorizationRef *ref, unsigned char *ext32);
// Frees the ref with DestroyRights so the credential cannot satisfy a later
// request: every flash gets its own sheet.
void flashit_app_authz_free(AuthorizationRef ref);
#endif
