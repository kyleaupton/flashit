#ifndef FLASHIT_DA_DARWIN_H
#define FLASHIT_DA_DARWIN_H
#include <stddef.h>

typedef struct flashit_da_handle flashit_da_handle;

// Claims bsd (e.g. "disk4") through Disk Arbitration so nothing else mounts
// or probes it while it is written. Returns 0 and sets *out on success, the
// DAReturn status if another client dissented, or -1 if the claim could not
// be attempted or did not answer in time.
int flashit_da_claim(const char *bsd, flashit_da_handle **out, char *err, size_t errlen);

// Releases the claim and the session behind it.
void flashit_da_unclaim(flashit_da_handle *c);
#endif
