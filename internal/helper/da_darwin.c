#include "da_darwin.h"
#include <stdio.h>
#include <stdlib.h>
#include <dispatch/dispatch.h>
#include <DiskArbitration/DiskArbitration.h>

struct flashit_da_handle {
	DASessionRef session;
	DADiskRef disk;
	dispatch_queue_t queue;
	dispatch_semaphore_t answered;
	int status;
};

static void claim_done(DADiskRef disk, DADissenterRef dissenter, void *context) {
	flashit_da_handle *c = context;
	c->status = dissenter ? (int)DADissenterGetStatus(dissenter) : 0;
	dispatch_semaphore_signal(c->answered);
}

// Something asked us to give the disk up mid-write; refuse.
static DADissenterRef claim_release(DADiskRef disk, void *context) {
	return DADissenterCreate(kCFAllocatorDefault, kDAReturnBusy, CFSTR("FlashIt is writing this disk"));
}

static void claim_free(flashit_da_handle *c) {
	if (c->disk) CFRelease(c->disk);
	if (c->session) {
		DASessionSetDispatchQueue(c->session, NULL);
		CFRelease(c->session);
	}
	if (c->queue) dispatch_release(c->queue);
	if (c->answered) dispatch_release(c->answered);
	free(c);
}

int flashit_da_claim(const char *bsd, flashit_da_handle **out, char *err, size_t errlen) {
	*out = NULL;
	flashit_da_handle *c = calloc(1, sizeof *c);
	if (!c) { snprintf(err, errlen, "out of memory"); return -1; }
	c->session = DASessionCreate(kCFAllocatorDefault);
	if (!c->session) { snprintf(err, errlen, "DASessionCreate failed"); claim_free(c); return -1; }
	c->queue = dispatch_queue_create("dev.kyleupton.flashit.helper.da", DISPATCH_QUEUE_SERIAL);
	c->answered = dispatch_semaphore_create(0);
	DASessionSetDispatchQueue(c->session, c->queue);
	c->disk = DADiskCreateFromBSDName(kCFAllocatorDefault, c->session, bsd);
	if (!c->disk) { snprintf(err, errlen, "no Disk Arbitration entry for %s", bsd); claim_free(c); return -1; }

	DADiskClaim(c->disk, kDADiskClaimOptionDefault, claim_release, c, claim_done, c);
	if (dispatch_semaphore_wait(c->answered, dispatch_time(DISPATCH_TIME_NOW, 10 * NSEC_PER_SEC)) != 0) {
		snprintf(err, errlen, "claim of %s did not answer", bsd);
		DADiskUnclaim(c->disk);
		claim_free(c);
		return -1;
	}
	if (c->status != 0) {
		snprintf(err, errlen, "claim of %s dissented: 0x%x", bsd, c->status);
		int st = c->status;
		claim_free(c);
		return st;
	}
	*out = c;
	return 0;
}

void flashit_da_unclaim(flashit_da_handle *c) {
	if (!c) return;
	DADiskUnclaim(c->disk);
	claim_free(c);
}
