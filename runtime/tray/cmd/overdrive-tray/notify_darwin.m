//go:build darwin

// Notification Center glue for the tray. It lives in a .m file because cgo compiles the preamble of a Go
// file with //export twice, which would define the Objective-C class twice.

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

void overdrivePostNote(const char *title, const char *body) {
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
