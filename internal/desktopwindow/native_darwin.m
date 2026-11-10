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
