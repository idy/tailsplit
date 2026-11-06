//go:build darwin && cgo

#import <AppKit/AppKit.h>
#import <WebKit/WebKit.h>
#include "_cgo_export.h"

// Defined in desktop_darwin.m.
void tailsplit_on_main(void (^action)(void));
void tailsplit_show_about(void);

// The configuration window, its page and delegate live until the app exits.
// They are used only on the main thread.
static NSWindow *panelWindow;
static WKWebView *panelView;
static NSURL *panelURL;
static int panelPort;
static BOOL panelLoaded;
static BOOL panelFailed;
static WKNavigation *panelNavigation; // The app's own load of panelURL.
static NSEvent *panelTitlebarMouseDown;
static NSRect panelFillRestoreFrame;
static NSRect panelLastFillFrame;
static BOOL panelHasFillRestoreFrame;

static NSString *const panelFrameName = @"TailSplitConfigurationWindow";

// Match the page's canvas before it loads and during overscroll.
static NSColor *tailsplit_panel_canvas(NSAppearance *appearance) {
    NSString *match = [appearance bestMatchFromAppearancesWithNames:@[NSAppearanceNameAqua, NSAppearanceNameDarkAqua]];
    if ([match isEqualToString:NSAppearanceNameDarkAqua]) {
        return [NSColor colorWithSRGBRed:28 / 255.0 green:28 / 255.0 blue:30 / 255.0 alpha:1];
    }
    return [NSColor colorWithSRGBRed:245 / 255.0 green:245 / 255.0 blue:247 / 255.0 alpha:1];
}

// As on Windows, the page has no context menu: its items could reload the page
// or open and download links elsewhere. AppKit does not show an empty menu.
@interface TailSplitPanelWebView : WKWebView
@end

@implementation TailSplitPanelWebView
- (void)willOpenMenu:(NSMenu *)menu withEvent:(NSEvent *)event {
    [menu removeAllItems];
}

- (void)viewDidChangeEffectiveAppearance {
    [super viewDidChangeEffectiveAppearance];
    NSColor *canvas = tailsplit_panel_canvas(self.effectiveAppearance);
    self.window.backgroundColor = canvas;
    if (@available(macOS 12.0, *)) self.underPageBackgroundColor = canvas;
}
@end

@interface TailSplitPanelDelegate : NSObject <NSWindowDelegate, WKNavigationDelegate, WKUIDelegate, WKScriptMessageHandler>
@end

static BOOL tailsplit_panel_allows(NSURL *url) {
    const char *value = url.absoluteString.UTF8String;
    return value && tailsplitConfigurationWindowAllows((char *)value, panelPort);
}

static void tailsplit_panel_load(void) {
    panelLoaded = NO;
    panelFailed = NO;
    [panelNavigation release];
    panelNavigation = [[panelView loadRequest:[NSURLRequest requestWithURL:panelURL]] retain];
}

// Showing the window again retries only the app's own load, and only until a
// page has loaded; other refused or replaced navigations must keep the draft.
static void tailsplit_panel_failed(WKNavigation *navigation) {
    if (!panelLoaded && navigation && navigation == panelNavigation) panelFailed = YES;
}

@implementation TailSplitPanelDelegate

// The local main frame can request only titlebar gestures. Use the original
// native mouse event, and require a recent click inside this window's title row.
- (void)userContentController:(WKUserContentController *)controller didReceiveScriptMessage:(WKScriptMessage *)message {
    if (message.webView != panelView || !message.frameInfo.mainFrame ||
        !tailsplit_panel_allows(message.frameInfo.request.URL) ||
        ![message.body isKindOfClass:[NSString class]] || !panelTitlebarMouseDown) return;
    NSEvent *event = [[panelTitlebarMouseDown retain] autorelease];
    [panelTitlebarMouseDown release];
    panelTitlebarMouseDown = nil;
    if (event.window != panelWindow) return;
    if ([message.body isEqualToString:@"drag"] && event.clickCount == 1) {
        // Start the native move loop after returning to WebKit. The block retains
        // its mouse event; do nothing if the user has already released the button.
        dispatch_async(dispatch_get_main_queue(), ^{
            if (panelWindow.visible && (NSEvent.pressedMouseButtons & 1)) [panelWindow performWindowDragWithEvent:event];
        });
    } else if ([message.body isEqualToString:@"zoom"] && event.clickCount == 2 &&
               NSProcessInfo.processInfo.systemUptime - event.timestamp < 1) {
        dispatch_async(dispatch_get_main_queue(), ^{
            if (!panelWindow.visible) return;
            NSString *action = [NSUserDefaults.standardUserDefaults stringForKey:@"AppleActionOnDoubleClick"];
            if ([action isEqualToString:@"Minimize"]) {
                [panelWindow performMiniaturize:nil];
            } else if ([action isEqualToString:@"Fill"] && panelWindow.screen) {
                NSRect available = panelWindow.screen.visibleFrame;
                if (panelHasFillRestoreFrame && NSEqualRects(panelWindow.frame, panelLastFillFrame)) {
                    [panelWindow setFrame:panelFillRestoreFrame display:YES animate:YES];
                    panelHasFillRestoreFrame = NO;
                } else {
                    panelFillRestoreFrame = panelWindow.frame;
                    panelLastFillFrame = available;
                    panelHasFillRestoreFrame = YES;
                    [panelWindow setFrame:available display:YES animate:YES];
                }
            } else if (![action isEqualToString:@"None"]) {
                [panelWindow performZoom:nil];
            }
        });
    }
}

- (void)webView:(WKWebView *)webView decidePolicyForNavigationAction:(WKNavigationAction *)action
    decisionHandler:(void (^)(WKNavigationActionPolicy))decisionHandler {
    // A navigation without a target frame would open a new window.
    BOOL allowed = action.targetFrame != nil && !action.shouldPerformDownload &&
                   tailsplit_panel_allows(action.request.URL);
    decisionHandler(allowed ? WKNavigationActionPolicyAllow : WKNavigationActionPolicyCancel);
}

- (void)webView:(WKWebView *)webView decidePolicyForNavigationResponse:(WKNavigationResponse *)response
    decisionHandler:(void (^)(WKNavigationResponsePolicy))decisionHandler {
    BOOL allowed = response.canShowMIMEType && tailsplit_panel_allows(response.response.URL);
    decisionHandler(allowed ? WKNavigationResponsePolicyAllow : WKNavigationResponsePolicyCancel);
}

- (void)webView:(WKWebView *)webView didFinishNavigation:(WKNavigation *)navigation {
    panelLoaded = YES;
    panelFailed = NO;
    webView.hidden = NO;
}

- (void)webView:(WKWebView *)webView didFailProvisionalNavigation:(WKNavigation *)navigation
    withError:(NSError *)error {
    tailsplit_panel_failed(navigation);
}

- (void)webView:(WKWebView *)webView didFailNavigation:(WKNavigation *)navigation withError:(NSError *)error {
    tailsplit_panel_failed(navigation);
}

- (void)webViewWebContentProcessDidTerminate:(WKWebView *)webView {
    // The page removes its capability from the address after loading, so load
    // the original URL again behind the themed canvas.
    webView.hidden = YES;
    tailsplit_panel_load();
}

- (WKWebView *)webView:(WKWebView *)webView createWebViewWithConfiguration:(WKWebViewConfiguration *)configuration
    forNavigationAction:(WKNavigationAction *)action windowFeatures:(WKWindowFeatures *)features {
    return nil;
}

- (void)webView:(WKWebView *)webView requestMediaCapturePermissionForOrigin:(WKSecurityOrigin *)origin
    initiatedByFrame:(WKFrameInfo *)frame type:(WKMediaCaptureType)type
    decisionHandler:(void (^)(WKPermissionDecision))decisionHandler API_AVAILABLE(macos(12.0)) {
    decisionHandler(WKPermissionDecisionDeny);
}

- (BOOL)windowShouldClose:(NSWindow *)window {
    // Keep the page and any draft; the menu item shows this window again.
    [window orderOut:nil];
    [NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
    return NO;
}

- (void)showAbout:(id)sender {
    tailsplit_show_about();
}

@end

static NSMenu *tailsplit_panel_menu(NSMenu *bar, NSString *title) {
    NSMenu *menu = [[[NSMenu alloc] initWithTitle:title] autorelease];
    [[bar addItemWithTitle:title action:NULL keyEquivalent:@""] setSubmenu:menu];
    return menu;
}

static void tailsplit_panel_item(NSMenu *menu, NSString *title, SEL action, NSString *key,
                                 NSEventModifierFlags modifiers, id target) {
    NSMenuItem *item = [menu addItemWithTitle:title action:action keyEquivalent:key];
    item.keyEquivalentModifierMask = modifiers;
    item.target = target;
}

// A menu-bar app has no main menu, so web text fields would not receive the
// standard editing shortcuts. The menu bar shows these menus while the window
// makes TailSplit a regular app; the shortcuts work whenever it is active.
static void tailsplit_panel_install_menu(id delegate) {
    const NSEventModifierFlags command = NSEventModifierFlagCommand;
    NSMenu *bar = [[[NSMenu alloc] initWithTitle:@""] autorelease];
    NSMenu *app = tailsplit_panel_menu(bar, @"TailSplit");
    tailsplit_panel_item(app, @"关于 TailSplit", @selector(showAbout:), @"", 0, delegate);
    [app addItem:[NSMenuItem separatorItem]];
    tailsplit_panel_item(app, @"隐藏 TailSplit", @selector(hide:), @"h", command, nil);
    tailsplit_panel_item(app, @"隐藏其他", @selector(hideOtherApplications:), @"h",
                         command | NSEventModifierFlagOption, nil);
    tailsplit_panel_item(app, @"全部显示", @selector(unhideAllApplications:), @"", 0, nil);
    [app addItem:[NSMenuItem separatorItem]];
    // terminate: runs the tray's exit handler, which stops a proxy this app started.
    tailsplit_panel_item(app, @"退出 TailSplit", @selector(terminate:), @"q", command, nil);

    NSMenu *edit = tailsplit_panel_menu(bar, @"编辑");
    tailsplit_panel_item(edit, @"撤销", @selector(undo:), @"z", command, nil);
    tailsplit_panel_item(edit, @"重做", @selector(redo:), @"Z", command, nil);
    [edit addItem:[NSMenuItem separatorItem]];
    tailsplit_panel_item(edit, @"剪切", @selector(cut:), @"x", command, nil);
    tailsplit_panel_item(edit, @"拷贝", @selector(copy:), @"c", command, nil);
    tailsplit_panel_item(edit, @"粘贴", @selector(paste:), @"v", command, nil);
    tailsplit_panel_item(edit, @"粘贴并匹配样式", @selector(pasteAsPlainText:), @"V",
                         command | NSEventModifierFlagOption, nil);
    tailsplit_panel_item(edit, @"删除", @selector(delete:), @"", 0, nil);
    tailsplit_panel_item(edit, @"全选", @selector(selectAll:), @"a", command, nil);

    NSMenu *window = tailsplit_panel_menu(bar, @"窗口");
    tailsplit_panel_item(window, @"最小化", @selector(performMiniaturize:), @"m", command, nil);
    tailsplit_panel_item(window, @"缩放", @selector(performZoom:), @"", 0, nil);
    [window addItem:[NSMenuItem separatorItem]];
    tailsplit_panel_item(window, @"关闭", @selector(performClose:), @"w", command, nil);
    NSApp.mainMenu = bar;
    NSApp.windowsMenu = window;
}

static BOOL tailsplit_panel_create(NSURL *url, int port) {
    static TailSplitPanelDelegate *delegate;
    if (!delegate) delegate = [[TailSplitPanelDelegate alloc] init];
    NSColor *canvas = tailsplit_panel_canvas(NSApp.effectiveAppearance);
    const NSRect bounds = NSMakeRect(0, 0, 1120, 780);

    WKWebViewConfiguration *configuration = [[[WKWebViewConfiguration alloc] init] autorelease];
    // Keep the page capability and any site data in memory, not in a profile.
    configuration.websiteDataStore = [WKWebsiteDataStore nonPersistentDataStore];
    configuration.preferences.javaScriptCanOpenWindowsAutomatically = NO;
    // A loopback page needs no Safe Browsing lookups.
    configuration.preferences.fraudulentWebsiteWarningEnabled = NO;
    [configuration.userContentController addScriptMessageHandler:delegate name:@"tailsplitWindow"];
    WKWebView *view = [[[TailSplitPanelWebView alloc] initWithFrame:bounds configuration:configuration] autorelease];
    NSWindow.allowsAutomaticWindowTabbing = NO;
    NSWindow *window = [[[NSWindow alloc] initWithContentRect:bounds
                                                    styleMask:NSWindowStyleMaskTitled | NSWindowStyleMaskClosable |
                                                              NSWindowStyleMaskMiniaturizable |
                                                              NSWindowStyleMaskResizable | NSWindowStyleMaskFullSizeContentView
                                                      backing:NSBackingStoreBuffered
                                                        defer:YES] autorelease];
    if (!view || !window) return NO;

    view.navigationDelegate = delegate;
    view.UIDelegate = delegate;
    // A Force Touch preview would load a link outside these policies.
    view.allowsLinkPreview = NO;
    view.allowsMagnification = NO;
    view.allowsBackForwardNavigationGestures = NO;
    if (@available(macOS 12.0, *)) view.underPageBackgroundColor = canvas;
    if (@available(macOS 13.3, *)) view.inspectable = NO;
    // The window shows its themed canvas, not a blank page, until the panel loads.
    view.hidden = YES;
    view.frame = window.contentView.bounds;
    view.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
    [window.contentView addSubview:view];

    window.title = @"TailSplit";
    window.releasedWhenClosed = NO;
    // Remember the frame only; window restoration would save the page's state.
    window.restorable = NO;
    window.tabbingMode = NSWindowTabbingModeDisallowed;
    window.collectionBehavior = NSWindowCollectionBehaviorFullScreenNone;
    // Inherit the system appearance; WebKit also uses it for prefers-color-scheme.
    window.backgroundColor = canvas;
    window.titlebarAppearsTransparent = YES;
    window.titleVisibility = NSWindowTitleHidden;
    // AppKit keeps its traffic lights above the full-size web content.
    [NSEvent addLocalMonitorForEventsMatchingMask:NSEventMaskLeftMouseDown handler:^NSEvent *(NSEvent *event) {
        [panelTitlebarMouseDown release];
        panelTitlebarMouseDown = nil;
        if (event.window == panelWindow) {
            NSPoint point = [panelView convertPoint:event.locationInWindow fromView:nil];
            CGFloat top = panelView.isFlipped ? point.y : NSHeight(panelView.bounds) - point.y;
            if (top >= 0 && top < 40 && point.x >= 80) panelTitlebarMouseDown = [event retain];
        }
        return event;
    }];
    window.contentMinSize = NSMakeSize(760, 560);
    window.delegate = delegate;
    if (![window setFrameUsingName:panelFrameName]) [window center];
    [window setFrameAutosaveName:panelFrameName];

    panelWindow = [window retain];
    panelView = [view retain];
    panelURL = [url retain];
    panelPort = port;
    tailsplit_panel_install_menu(delegate);
    tailsplit_panel_load();
    return YES;
}

static void tailsplit_panel_present(void) {
    // Join the Dock and Command-Tab while the window is open; closing it
    // returns TailSplit to the menu bar.
    [NSApp setActivationPolicy:NSApplicationActivationPolicyRegular];
    if (NSApp.hidden) [NSApp unhide:nil];
    if (panelWindow.miniaturized) [panelWindow deminiaturize:nil];
    if (panelFailed) tailsplit_panel_load();
    [NSApp activateIgnoringOtherApps:YES];
    [panelWindow makeKeyAndOrderFront:nil];
}

// Returns 0 when shown, 1 when the window could not be created, or 2 when the
// existing window belongs to a different page.
int tailsplit_show_configuration_window(const char *url, int port) {
    __block int result = 1;
    tailsplit_on_main(^{
        @autoreleasepool {
            NSString *value = url ? [NSString stringWithUTF8String:url] : nil;
            NSURL *target = value ? [NSURL URLWithString:value] : nil;
            if (!target) return;
            if (panelWindow && (port != panelPort || ![target isEqual:panelURL])) {
                result = 2;
                return;
            }
            if (!panelWindow && !tailsplit_panel_create(target, port)) return;
            tailsplit_panel_present();
            result = 0;
        }
    });
    return result;
}
