#import "shim_darwin.h"
#import <Foundation/Foundation.h>
#import <ServiceManagement/ServiceManagement.h>
#import <Security/Security.h>
#import <stdio.h>
#import <string.h>

static void ns_err(NSError *e, char *buf, size_t n) {
	if (!e) { buf[0] = 0; return; }
	snprintf(buf, n, "%s (%s %ld)", e.localizedDescription.UTF8String, e.domain.UTF8String, (long)e.code);
}

static SMAppService *daemon_for(const char *plist) {
	return [SMAppService daemonServiceWithPlistName:[NSString stringWithUTF8String:plist]];
}

int flashit_sm_status(const char *plist) {
	@autoreleasepool { return (int)daemon_for(plist).status; }
}

int flashit_sm_register(const char *plist, char *err, size_t errlen) {
	@autoreleasepool {
		NSError *e = nil;
		BOOL ok = [daemon_for(plist) registerAndReturnError:&e];
		ns_err(e, err, errlen);
		return ok ? 0 : 1;
	}
}

int flashit_sm_unregister(const char *plist, char *err, size_t errlen) {
	@autoreleasepool {
		NSError *e = nil;
		BOOL ok = [daemon_for(plist) unregisterAndReturnError:&e];
		ns_err(e, err, errlen);
		return ok ? 0 : 1;
	}
}

void flashit_sm_open_settings(void) {
	@autoreleasepool { [SMAppService openSystemSettingsLoginItems]; }
}

int flashit_authz_create(void **ref, unsigned char *ext32) {
	AuthorizationRef r = NULL;
	OSStatus st = AuthorizationCreate(NULL, NULL, kAuthorizationFlagDefaults, &r);
	if (st) return st;
	AuthorizationExternalForm form;
	st = AuthorizationMakeExternalForm(r, &form);
	if (st) { AuthorizationFree(r, kAuthorizationFlagDefaults); return st; }
	memcpy(ext32, form.bytes, kAuthorizationExternalFormLength);
	*ref = (void *)r;
	return 0;
}

// DestroyRights so the credential the helper obtained on this ref cannot
// satisfy a later request: every flash gets its own sheet.
void flashit_authz_free(void *ref) {
	if (ref) AuthorizationFree((AuthorizationRef)ref, kAuthorizationFlagDestroyRights);
}
