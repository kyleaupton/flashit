#include "peer_darwin.h"
#include <errno.h>
#include <stdio.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/un.h>
#include <bsm/libbsm.h>
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>

static void cferr_to_buf(CFErrorRef e, OSStatus st, char *buf, size_t n) {
	buf[0] = 0;
	if (e) {
		CFStringRef d = CFErrorCopyDescription(e);
		if (d) {
			CFStringGetCString(d, buf, n, kCFStringEncodingUTF8);
			CFRelease(d);
		}
		CFRelease(e);
	}
	if (!buf[0]) snprintf(buf, n, "OSStatus %d", (int)st);
}

static OSStatus requirement_from(const char *requirement, SecRequirementRef *out, char *err, size_t errlen) {
	CFStringRef s = CFStringCreateWithCString(NULL, requirement, kCFStringEncodingUTF8);
	if (!s) { snprintf(err, errlen, "requirement is not UTF-8"); return errSecParam; }
	CFErrorRef cferr = NULL;
	OSStatus st = SecRequirementCreateWithStringAndErrors(s, kSecCSDefaultFlags, &cferr, out);
	CFRelease(s);
	if (st) cferr_to_buf(cferr, st, err, errlen);
	return st;
}

int flashit_requirement_validate(const char *requirement, char *err, size_t errlen) {
	SecRequirementRef req = NULL;
	OSStatus st = requirement_from(requirement, &req, err, errlen);
	if (req) CFRelease(req);
	return st;
}

int flashit_peer_check(int fd, const char *requirement, int *uid, int *pid, char *err, size_t errlen) {
	*uid = -1;
	*pid = -1;
	audit_token_t tok;
	socklen_t len = sizeof tok;
	if (getsockopt(fd, SOL_LOCAL, LOCAL_PEERTOKEN, &tok, &len) != 0) {
		int rc = errno;
		snprintf(err, errlen, "LOCAL_PEERTOKEN: %s", strerror(rc));
		return rc;
	}
	if (len != sizeof tok) { snprintf(err, errlen, "LOCAL_PEERTOKEN: short token"); return EINVAL; }
	*uid = (int)audit_token_to_euid(tok);
	*pid = (int)audit_token_to_pid(tok);

	CFDataRef data = CFDataCreate(NULL, (const UInt8 *)&tok, sizeof tok);
	const void *k[] = {kSecGuestAttributeAudit};
	const void *v[] = {data};
	CFDictionaryRef attrs = CFDictionaryCreate(NULL, k, v, 1, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	SecCodeRef code = NULL;
	OSStatus st = SecCodeCopyGuestWithAttributes(NULL, attrs, kSecCSDefaultFlags, &code);
	CFRelease(attrs);
	CFRelease(data);
	if (st) { snprintf(err, errlen, "SecCodeCopyGuestWithAttributes: %d", (int)st); return st; }

	SecRequirementRef req = NULL;
	st = requirement_from(requirement, &req, err, errlen);
	if (st) { CFRelease(code); return st; }

	CFErrorRef cferr = NULL;
	st = SecCodeCheckValidityWithErrors(code, kSecCSDefaultFlags, req, &cferr);
	if (st) cferr_to_buf(cferr, st, err, errlen);
	CFRelease(req);
	CFRelease(code);
	return st;
}

static void rule_str(CFDictionaryRef rule, const char *key, char *buf, size_t n) {
	buf[0] = 0;
	CFStringRef k = CFStringCreateWithCString(NULL, key, kCFStringEncodingUTF8);
	CFTypeRef v = CFDictionaryGetValue(rule, k);
	CFRelease(k);
	if (v && CFGetTypeID(v) == CFStringGetTypeID()) CFStringGetCString(v, buf, n, kCFStringEncodingUTF8);
}

static int rule_bool(CFDictionaryRef rule, const char *key) {
	CFStringRef k = CFStringCreateWithCString(NULL, key, kCFStringEncodingUTF8);
	CFTypeRef v = CFDictionaryGetValue(rule, k);
	CFRelease(k);
	if (!v) return -1;
	if (CFGetTypeID(v) == CFBooleanGetTypeID()) return CFBooleanGetValue(v) ? 1 : 0;
	if (CFGetTypeID(v) == CFNumberGetTypeID()) {
		int n = 0;
		if (CFNumberGetValue(v, kCFNumberIntType, &n)) return n ? 1 : 0;
	}
	return -1;
}

static long rule_num(CFDictionaryRef rule, const char *key) {
	CFStringRef k = CFStringCreateWithCString(NULL, key, kCFStringEncodingUTF8);
	CFTypeRef v = CFDictionaryGetValue(rule, k);
	CFRelease(k);
	long n = -1;
	if (v && CFGetTypeID(v) == CFNumberGetTypeID() && !CFNumberGetValue(v, kCFNumberLongType, &n)) n = -1;
	return n;
}

int flashit_authz_right_read(const char *right, flashit_authz_rule *out) {
	memset(out, 0, sizeof *out);
	out->authenticate_user = out->allow_root = out->shared = -1;
	out->timeout = -1;
	CFDictionaryRef rule = NULL;
	OSStatus st = AuthorizationRightGet(right, &rule);
	if (st) return st;
	if (!rule) return errAuthorizationInternal;
	rule_str(rule, "class", out->class_, sizeof out->class_);
	rule_str(rule, "group", out->group, sizeof out->group);
	out->authenticate_user = rule_bool(rule, "authenticate-user");
	out->allow_root = rule_bool(rule, "allow-root");
	out->shared = rule_bool(rule, "shared");
	out->timeout = rule_num(rule, "timeout");
	CFRelease(rule);
	return 0;
}

static void dict_set_bool(CFMutableDictionaryRef d, const char *key, Boolean v) {
	CFStringRef k = CFStringCreateWithCString(NULL, key, kCFStringEncodingUTF8);
	CFDictionarySetValue(d, k, v ? kCFBooleanTrue : kCFBooleanFalse);
	CFRelease(k);
}

static void dict_set_str(CFMutableDictionaryRef d, const char *key, const char *v) {
	CFStringRef k = CFStringCreateWithCString(NULL, key, kCFStringEncodingUTF8);
	CFStringRef s = CFStringCreateWithCString(NULL, v, kCFStringEncodingUTF8);
	CFDictionarySetValue(d, k, s);
	CFRelease(s);
	CFRelease(k);
}

static void dict_set_int(CFMutableDictionaryRef d, const char *key, int v) {
	CFStringRef k = CFStringCreateWithCString(NULL, key, kCFStringEncodingUTF8);
	CFNumberRef n = CFNumberCreate(NULL, kCFNumberIntType, &v);
	CFDictionarySetValue(d, k, n);
	CFRelease(n);
	CFRelease(k);
}

int flashit_authz_right_create(const char *right, const char *prompt, int timeout_s, char *err, size_t errlen) {
	AuthorizationRef ref = NULL;
	OSStatus st = AuthorizationCreate(NULL, NULL, kAuthorizationFlagDefaults, &ref);
	if (st) { snprintf(err, errlen, "AuthorizationCreate: %d", (int)st); return st; }

	// An admin user must authenticate, the credential is private to this
	// right (shared = false, so another app's cached admin credential does
	// not satisfy it) and expires quickly. A zero timeout cannot be
	// pre-authorized, so it stays non-zero even though the helper prompts.
	CFMutableDictionaryRef rule = CFDictionaryCreateMutable(NULL, 0, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	dict_set_str(rule, "class", "user");
	dict_set_str(rule, "group", "admin");
	dict_set_bool(rule, "authenticate-user", true);
	dict_set_bool(rule, "allow-root", false);
	dict_set_bool(rule, "session-owner", false);
	dict_set_bool(rule, "shared", false);
	dict_set_int(rule, "timeout", timeout_s);
	dict_set_int(rule, "tries", 3);
	dict_set_str(rule, "comment", "FlashIt: write a disk image to a removable drive.");

	CFStringRef p = CFStringCreateWithCString(NULL, prompt, kCFStringEncodingUTF8);
	st = AuthorizationRightSet(ref, right, rule, p, NULL, NULL);
	CFRelease(p);
	CFRelease(rule);
	AuthorizationFree(ref, kAuthorizationFlagDefaults);
	if (st) snprintf(err, errlen, "AuthorizationRightSet: %d", (int)st);
	return st;
}

int flashit_authz_check(const unsigned char *ext32, const char *right) {
	AuthorizationExternalForm form;
	memcpy(form.bytes, ext32, kAuthorizationExternalFormLength);
	AuthorizationRef ref = NULL;
	OSStatus st = AuthorizationCreateFromExternalForm(&form, &ref);
	if (st) return st;

	AuthorizationItem item = {right, 0, NULL, 0};
	AuthorizationRights rights = {1, &item};
	st = AuthorizationCopyRights(ref, &rights, NULL,
		kAuthorizationFlagExtendRights | kAuthorizationFlagInteractionAllowed, NULL);
	AuthorizationFree(ref, kAuthorizationFlagDefaults);
	return st;
}
