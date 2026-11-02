//go:build darwin && cgo

#import <AppKit/AppKit.h>
#include <stdlib.h>
#include <string.h>

char *tailsplit_default_browser(void) {
    @autoreleasepool {
        NSURL *target = [NSURL URLWithString:@"https://login.tailscale.com/"];
        NSURL *application = [[NSWorkspace sharedWorkspace]
                              URLForApplicationToOpenURL:target];
        if (!application) return NULL;
        NSString *identifier = [[NSBundle bundleWithURL:application]
                                 bundleIdentifier];
        if (!identifier) return NULL;
        const char *value = [identifier UTF8String];
        return value ? strdup(value) : NULL;
    }
}
