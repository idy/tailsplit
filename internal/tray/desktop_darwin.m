//go:build darwin && cgo

#import <AppKit/AppKit.h>
#import <ServiceManagement/ServiceManagement.h>
#include <stdlib.h>
#include <string.h>

void tailsplit_on_main(void (^action)(void)) {
    if ([NSThread isMainThread]) {
        action();
    } else {
        dispatch_sync(dispatch_get_main_queue(), action);
    }
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
            return strdup("请从 TailSplit.app 设置登录时启动");
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
                                : "无法更新 macOS 登录项");
        }
        return strdup("此 macOS 版本不支持登录项设置");
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
            alert.messageText = [NSString stringWithFormat:@"升级到 TailSplit %@？", [NSString stringWithUTF8String:version]];
            alert.informativeText = @"将下载并校验新版 App，然后断开当前连接并重启。配置和设备登录会保留；下次连接时可能需要批准更新后台网络服务。";
            [alert addButtonWithTitle:@"升级并重启"];
            [alert addButtonWithTitle:@"取消"];
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
                NSAboutPanelOptionApplicationVersion: version ?: @"开发版本",
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
            alert.messageText = @"设备名称";
            alert.informativeText = @"留空可自动使用本机 hostname。修改后，正在运行的代理会重新连接；已有设备登录会保留。";
            [alert addButtonWithTitle:@"保存"];
            [alert addButtonWithTitle:@"取消"];
            [alert addButtonWithTitle:@"使用本机名称"];
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
