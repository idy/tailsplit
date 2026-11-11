//go:build darwin && cgo

#import <AppKit/AppKit.h>
#include <stdint.h>

// Retain the installed app's frame preference; window creation and rendering
// belong entirely to MyGo. This runs on MyGo's main AppKit thread.
void tailsplit_restore_mygo_frame(uintptr_t handle) {
    NSWindow *window = (__bridge NSWindow *)(void *)handle;
    [window setFrameUsingName:@"TailSplitConfigurationWindow"];
    [window setFrameAutosaveName:@"TailSplitConfigurationWindow"];
}

// Opt-in proof only. Keep the bit order in nativePopupProofState in sync.
uint32_t tailsplit_popup_proof_state(uintptr_t handle) {
    NSWindow *window = (__bridge NSWindow *)(void *)handle;
    if (window == nil) return 0;
    NSWindowCollectionBehavior behavior = window.collectionBehavior;
    return (([window isKindOfClass:[NSPanel class]] &&
             (window.styleMask & NSWindowStyleMaskNonactivatingPanel) != 0) << 0) |
           (((behavior & NSWindowCollectionBehaviorCanJoinAllSpaces) != 0) << 1) |
           (((behavior & NSWindowCollectionBehaviorFullScreenAuxiliary) != 0) << 2) |
           (window.onActiveSpace << 3) |
           (((window.occlusionState & NSWindowOcclusionStateVisible) != 0) << 4) |
           (window.keyWindow << 5) |
           (NSApp.active << 6);
}

// Opt-in proof only: an inert, app-local target for a real AppKit outside click.
// No global input is generated and no application action can be activated.
uintptr_t tailsplit_new_popup_proof_target(void) {
    NSWindow *window = [[NSWindow alloc]
        initWithContentRect:NSMakeRect(100, 100, 160, 80)
        styleMask:NSWindowStyleMaskBorderless
        backing:NSBackingStoreBuffered defer:NO];
    [window setReleasedWhenClosed:NO];
    [window setTitle:@"TailSplit popup dismissal proof"];
    [window orderFront:nil];
    return (uintptr_t)(__bridge void *)window;
}

void tailsplit_click_popup_proof_target(uintptr_t handle) {
    NSWindow *window = (__bridge NSWindow *)(void *)handle;
    for (NSNumber *type in @[@(NSEventTypeLeftMouseDown), @(NSEventTypeLeftMouseUp)]) {
        NSEvent *event = [NSEvent mouseEventWithType:(NSEventType)[type unsignedIntegerValue]
            location:NSMakePoint(20, 20) modifierFlags:0
            timestamp:[NSProcessInfo processInfo].systemUptime
            windowNumber:window.windowNumber context:nil
            eventNumber:0 clickCount:1 pressure:1];
        [NSApp postEvent:event atStart:NO];
    }
}

void tailsplit_close_popup_proof_target(uintptr_t handle) {
    NSWindow *window = (__bridge NSWindow *)(void *)handle;
    [window close];
    [window release];
}
