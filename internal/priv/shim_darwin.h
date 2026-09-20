#ifndef FLASHIT_SHIM_DARWIN_H
#define FLASHIT_SHIM_DARWIN_H
#include <stddef.h>

// SMAppService. Status values mirror SMAppServiceStatus:
// 0 notRegistered, 1 enabled, 2 requiresApproval, 3 notFound.
int  flashit_sm_status(const char *plist);
int  flashit_sm_register(const char *plist, char *err, size_t errlen);   // 0 ok
int  flashit_sm_unregister(const char *plist, char *err, size_t errlen); // 0 ok
void flashit_sm_open_settings(void);

// Creates an AuthorizationRef that holds no rights yet and writes its
// 32-byte external form. The helper redeems the form, prompting the user.
// Returns OSStatus; the ref must be released with flashit_authz_free.
int  flashit_authz_create(void **ref, unsigned char *ext32);
void flashit_authz_free(void *ref);
#endif
