//go:build darwin

package main

/*
#cgo darwin CFLAGS: -x objective-c
#cgo darwin LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>
#import <stdlib.h>

extern void overdriveTrayNotifyClick(void);

@interface OverdriveTrayNote : NSObject <NSUserNotificationCenterDelegate>
@end

@implementation OverdriveTrayNote
- (BOOL)userNotificationCenter:(NSUserNotificationCenter *)center shouldPresentNotification:(NSUserNotification *)notification {
    return YES;
}
- (void)userNotificationCenter:(NSUserNotificationCenter *)center didActivateNotification:(NSUserNotification *)notification {
    overdriveTrayNotifyClick();
}
@end

static OverdriveTrayNote *overdriveNote;

static void overdrivePostNote(const char *title, const char *body) {
    if (overdriveNote == nil) {
        overdriveNote = [OverdriveTrayNote new];
        [[NSUserNotificationCenter defaultUserNotificationCenter] setDelegate:overdriveNote];
    }
    NSUserNotification *note = [NSUserNotification new];
    note.title = [NSString stringWithUTF8String:title];
    note.informativeText = [NSString stringWithUTF8String:body];
    note.hasActionButton = YES;
    note.actionButtonTitle = @"Open";
    [[NSUserNotificationCenter defaultUserNotificationCenter] deliverNotification:note];
}
*/
import "C"
import (
	"os/exec"
	"sync/atomic"
	"unsafe"

	"github.com/gfrancodev/overdrive/runtime/tray/internal/browser"
	"github.com/gfrancodev/overdrive/runtime/tray/internal/coreclient"
	"github.com/gfrancodev/overdrive/runtime/tray/internal/quiet"
)

var darwinURL atomic.Value

//export overdriveTrayNotifyClick
func overdriveTrayNotifyClick() {
	raw, _ := darwinURL.Load().(string)
	if raw == "" {
		return
	}
	_ = browser.Open(raw)
}

func notificationsPaused() bool {
	out, err := exec.Command("defaults", "-currentHost", "read", "com.apple.notificationcenterui", "doNotDisturb").Output()
	return quiet.Paused(quiet.DarwinDoNotDisturb(string(out), err), false)
}

func sendNotice(title, body, openURL string) error {
	if _, err := coreclient.ValidateUIURL(openURL); err != nil {
		return nil
	}
	darwinURL.Store(openURL)
	ct := C.CString(title)
	cb := C.CString(body)
	defer C.free(unsafe.Pointer(ct))
	defer C.free(unsafe.Pointer(cb))
	C.overdrivePostNote(ct, cb)
	return nil
}
