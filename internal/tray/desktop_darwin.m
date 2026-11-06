//go:build darwin && cgo

#import <AppKit/AppKit.h>
#import <ServiceManagement/ServiceManagement.h>
#include <stdlib.h>
#include <string.h>
#include <stdatomic.h>
#include <stdbool.h>

void tailsplit_on_main(void (^action)(void)) {
    if ([NSThread isMainThread]) {
        action();
    } else {
        dispatch_sync(dispatch_get_main_queue(), action);
    }
}

// Text can be requested by login-item operations off the AppKit main queue.
// The preference is atomic; menu mutations still run only on the main queue.
static atomic_bool tailsplitChinese = false;
void tailsplit_panel_refresh_language(void);
NSString *tailsplit_ui_text(NSString *chinese, NSString *english) {
    return atomic_load(&tailsplitChinese) ? chinese : english;
}
void tailsplit_set_ui_language(int chinese) {
    atomic_store(&tailsplitChinese, chinese != 0);
    void (^update)(void) = ^{
        tailsplit_panel_refresh_language();
    };
    // Initial setup can run before NSApplication's event loop starts.
    if ([NSThread isMainThread]) update();
    else dispatch_async(dispatch_get_main_queue(), update);
}

int tailsplit_login_item_status(void) {
    @autoreleasepool {
        if (![[[NSBundle mainBundle] bundlePath] hasSuffix:@".app"]) return 3;
        if (@available(macOS 13.0, *)) {
            return (int)[SMAppService mainAppService].status;
        }
        return 3;
    }
}

char *tailsplit_set_login_item(int enabled) {
    @autoreleasepool {
        if (tailsplit_login_item_status() == 3)
            return strdup([tailsplit_ui_text(@"请从 TailSplit.app 设置登录时启动", @"Set Open at Login from TailSplit.app") UTF8String]);
        if (@available(macOS 13.0, *)) {
            SMAppService *service = [SMAppService mainAppService];
            if ((enabled && (service.status == SMAppServiceStatusEnabled ||
                             service.status == SMAppServiceStatusRequiresApproval)) ||
                (!enabled && service.status == SMAppServiceStatusNotRegistered))
                return NULL;
            NSError *error = nil;
            BOOL ok = enabled ? [service registerAndReturnError:&error]
                              : [service unregisterAndReturnError:&error];
            if (ok) return NULL;
            return strdup(error ? [[error localizedDescription] UTF8String]
                                : [tailsplit_ui_text(@"无法更新 macOS 登录项", @"Could not update the macOS login item") UTF8String]);
        }
        return strdup([tailsplit_ui_text(@"此 macOS 版本不支持登录项设置", @"This macOS version does not support login item settings") UTF8String]);
    }
}

void tailsplit_login_item_settings(void) {
    tailsplit_on_main(^{
        if (@available(macOS 13.0, *)) {
            [SMAppService openSystemSettingsLoginItems];
        }
    });
}

char *tailsplit_app_version(void) {
    @autoreleasepool {
        NSString *version = [[NSBundle mainBundle] objectForInfoDictionaryKey:@"CFBundleShortVersionString"];
        return strdup(version ? [version UTF8String] : "dev");
    }
}

int tailsplit_confirm_update(const char *version) {
    __block int confirmed = 0;
    tailsplit_on_main(^{
        @autoreleasepool {
            NSAlert *alert = [[[NSAlert alloc] init] autorelease];
            alert.messageText = [NSString stringWithFormat:tailsplit_ui_text(@"升级到 TailSplit %@？", @"Update to TailSplit %@?"), [NSString stringWithUTF8String:version]];
            alert.informativeText = tailsplit_ui_text(@"将下载并校验新版 App，然后断开当前连接并重启。配置和设备登录会保留；下次连接时可能需要批准更新后台网络服务。", @"Download and verify the new app, then disconnect and restart. Configuration and device sign-in are kept. The next connection may require approval to update the background network service.");
            [alert addButtonWithTitle:tailsplit_ui_text(@"升级并重启", @"Update & Restart")];
            [alert addButtonWithTitle:tailsplit_ui_text(@"取消", @"Cancel")];
            [NSApp activateIgnoringOtherApps:YES];
            confirmed = [alert runModal] == NSAlertFirstButtonReturn;
        }
    });
    return confirmed;
}

void tailsplit_show_about(void) {
    tailsplit_on_main(^{
        @autoreleasepool {
            NSBundle *bundle = [NSBundle mainBundle];
            NSString *version = [bundle objectForInfoDictionaryKey:@"CFBundleShortVersionString"];
            NSString *build = [bundle objectForInfoDictionaryKey:@"CFBundleVersion"];
            [NSApp activateIgnoringOtherApps:YES];
            [NSApp orderFrontStandardAboutPanelWithOptions:@{
                NSAboutPanelOptionApplicationName: @"TailSplit",
                NSAboutPanelOptionApplicationVersion: version ?: tailsplit_ui_text(@"开发版本", @"Development version"),
                NSAboutPanelOptionVersion: build ?: @""
            }];
        }
    });
}

char *tailsplit_edit_device_name(const char *current, const char *suggested, int *saved) {
    __block char *result = NULL;
    *saved = 0;
    tailsplit_on_main(^{
        @autoreleasepool {
            NSAlert *alert = [[[NSAlert alloc] init] autorelease];
            alert.messageText = tailsplit_ui_text(@"设备名称", @"Device Name");
            alert.informativeText = tailsplit_ui_text(@"留空可自动使用本机 hostname。修改后，正在运行的代理会重新连接；已有设备登录会保留。", @"Leave blank to use this computer’s hostname. Saving reconnects the running proxy; device sign-in is kept.");
            [alert addButtonWithTitle:tailsplit_ui_text(@"保存", @"Save")];
            [alert addButtonWithTitle:tailsplit_ui_text(@"取消", @"Cancel")];
            [alert addButtonWithTitle:tailsplit_ui_text(@"使用本机名称", @"Use Computer Name")];
            NSTextField *field = [[[NSTextField alloc] initWithFrame:NSMakeRect(0, 0, 360, 26)] autorelease];
            field.stringValue = [NSString stringWithUTF8String:current];
            field.placeholderString = [NSString stringWithUTF8String:suggested];
            alert.accessoryView = field;
            [[alert window] setInitialFirstResponder:field];
            [NSApp activateIgnoringOtherApps:YES];
            NSModalResponse response = [alert runModal];
            if (response == NSAlertFirstButtonReturn) {
                result = strdup([field.stringValue UTF8String]);
                *saved = 1;
            } else if (response == NSAlertThirdButtonReturn) {
                result = strdup("");
                *saved = 1;
            }
        }
    });
    return result;
}
