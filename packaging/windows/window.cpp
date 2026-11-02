// TailSplit's app-owned WebView2 window. Build for x64 or ARM64 with the matching
// Microsoft WebView2 SDK headers and WebView2LoaderStatic.lib. No CGO is needed.
#ifndef UNICODE
#define UNICODE
#endif
#ifndef _UNICODE
#define _UNICODE
#endif
#ifndef _WIN32_WINNT
#define _WIN32_WINNT 0x0A00
#endif
#ifndef WIN32_LEAN_AND_MEAN
#define WIN32_LEAN_AND_MEAN
#endif
#ifndef NOMINMAX
#define NOMINMAX
#endif
#if !defined(_M_X64) && !defined(_M_ARM64)
#error TailSplitWindow requires an x64 or ARM64 build.
#endif
#include <windows.h>
#include <objbase.h>
#include <shlwapi.h>
#include <wrl.h>
#include <cwchar>
#include "WebView2.h"

using Microsoft::WRL::Callback;
using Microsoft::WRL::ComPtr;

namespace {

constexpr UINT kShow = WM_APP + 0x451;
constexpr UINT kQuit = WM_APP + 0x452;
constexpr UINT kFinish = WM_APP + 0x453;
constexpr wchar_t kReady[] = L"TailSplit.WebView2.Ready";
// A numeric phase lets startup diagnostics inspect a hidden window without
// exposing its capability URL, profile contents or browser command line.
constexpr wchar_t kStartupStage[] = L"TailSplit.WebView2.StartupStage";
LONG activeRun = 0;

HRESULT LastErrorResult() noexcept {
    const DWORD error = GetLastError();
    return HRESULT_FROM_WIN32(error == ERROR_SUCCESS ? ERROR_GEN_FAILURE : error);
}

bool IsOwnWindow(HWND window) noexcept {
    DWORD pid = 0;
    return GetWindowThreadProcessId(window, &pid) != 0 && pid == GetCurrentProcessId();
}

// Check the authority ourselves rather than accepting URL parser normalization
// of credentials, backslashes, alternate loopback spellings or external schemes.
// WebView2 removes the default :80, so subsequent requests may omit that port.
bool LoopbackPort(const wchar_t* uri, bool initial, unsigned& port) noexcept {
    constexpr wchar_t prefix[] = L"http://127.0.0.1";
    constexpr size_t prefixLength = sizeof(prefix) / sizeof(wchar_t) - 1;
    if (!uri || wcsncmp(uri, prefix, prefixLength) != 0) {
        return false;
    }
    const wchar_t* rest = uri + prefixLength;
    port = 80;
    if (*rest == L':') {
        ++rest;
        if (*rest < L'0' || *rest > L'9') {
            return false;
        }
        port = 0;
        while (*rest >= L'0' && *rest <= L'9') {
            port = port * 10 + static_cast<unsigned>(*rest++ - L'0');
            if (port > 65535) {
                return false;
            }
        }
        if (port == 0) {
            return false;
        }
    } else if (initial) {
        return false;
    }
    if (*rest && *rest != L'/' && *rest != L'?' && *rest != L'#') {
        return false;
    }
    for (const wchar_t* p = uri; *p; ++p) {
        if (*p < L' ' || *p == 127 || *p == L'\\') {
            return false;
        }
    }
    if (initial) {
        const wchar_t* query = wcschr(rest, L'?');
        const wchar_t* fragment = wcschr(rest, L'#');
        if (query && (!fragment || query < fragment) && query[1] && query[1] != L'#') {
            return false;
        }
    }
    return true;
}

template <typename Function>
Function User32Function(const char* name) noexcept {
    return reinterpret_cast<Function>(GetProcAddress(GetModuleHandleW(L"user32.dll"), name));
}

UINT WindowDPI(HWND window) noexcept {
    using GetWindowDPI = UINT(WINAPI*)(HWND);
    using GetSystemDPI = UINT(WINAPI*)();
    static const auto getWindowDPI = User32Function<GetWindowDPI>("GetDpiForWindow");
    static const auto getSystemDPI = User32Function<GetSystemDPI>("GetDpiForSystem");
    const UINT dpi = window && getWindowDPI ? getWindowDPI(window)
                                          : (getSystemDPI ? getSystemDPI() : 0);
    return dpi ? dpi : USER_DEFAULT_SCREEN_DPI;
}

class ThreadDPI {
public:
    ThreadDPI() noexcept {
        change_ = User32Function<Change>("SetThreadDpiAwarenessContext");
        if (change_) {
            previous_ = change_(DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2);
            if (!previous_) {
                previous_ = change_(DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE);
            }
        }
    }
    ~ThreadDPI() {
        if (previous_) {
            change_(previous_);
        }
    }
private:
    using Change = DPI_AWARENESS_CONTEXT(WINAPI*)(DPI_AWARENESS_CONTEXT);
    Change change_ = nullptr;
    DPI_AWARENESS_CONTEXT previous_ = nullptr;
};

class Apartment {
public:
    Apartment() noexcept : result(CoInitializeEx(nullptr, COINIT_APARTMENTTHREADED | COINIT_DISABLE_OLE1DDE)) {}
    ~Apartment() {
        if (SUCCEEDED(result)) {
            CoUninitialize();
        }
    }
    const HRESULT result;
};

// Each subscription retains its source until it has removed the callback. Event
// callbacks only borrow the host, whose lifetime covers the entire STA loop.
template <typename Source>
class Subscription {
public:
    using Remove = HRESULT(STDMETHODCALLTYPE Source::*)(EventRegistrationToken);

    template <typename Handler>
    HRESULT Add(Source* source,
                HRESULT(STDMETHODCALLTYPE Source::*add)(Handler*, EventRegistrationToken*),
                Remove remove, const ComPtr<Handler>& handler) noexcept {
        if (!handler) {
            return E_OUTOFMEMORY;
        }
        const HRESULT hr = (source->*add)(handler.Get(), &token_);
        if (SUCCEEDED(hr)) {
            source_ = source;
            remove_ = remove;
        }
        return hr;
    }

    void Reset() noexcept {
        if (source_) {
            (source_.Get()->*remove_)(token_);
            source_.Reset();
        }
    }
    ~Subscription() { Reset(); }
private:
    ComPtr<Source> source_;
    EventRegistrationToken token_{};
    Remove remove_ = nullptr;
};

class WindowHost {
public:
    WindowHost(const wchar_t* url, const wchar_t* userData, const wchar_t* className,
               unsigned port) noexcept
        : url_(url), userData_(userData), className_(className), port_(port) {}

    ~WindowHost() {
        CloseBrowser();
        if (window_) {
            DestroyWindow(window_);
        }
        if (registered_) {
            UnregisterClassW(className_, instance_);
        }
    }

    HRESULT Run() noexcept {
        // Establish a queue before any completion can request deferred teardown.
        MSG message{};
        PeekMessageW(&message, nullptr, WM_USER, WM_USER, PM_NOREMOVE);
        if (!GetModuleHandleExW(GET_MODULE_HANDLE_EX_FLAG_FROM_ADDRESS |
                                   GET_MODULE_HANDLE_EX_FLAG_UNCHANGED_REFCOUNT,
                               reinterpret_cast<LPCWSTR>(&WindowProc), &instance_)) {
            return LastErrorResult();
        }
        WNDCLASSEXW klass{};
        klass.cbSize = sizeof(klass);
        klass.style = CS_HREDRAW | CS_VREDRAW;
        klass.lpfnWndProc = WindowProc;
        klass.hInstance = instance_;
        klass.hCursor = LoadCursorW(nullptr, IDC_ARROW);
        klass.hIcon = LoadIconW(nullptr, IDI_APPLICATION);
        klass.hIconSm = klass.hIcon;
        klass.hbrBackground = reinterpret_cast<HBRUSH>(COLOR_WINDOW + 1);
        klass.lpszClassName = className_;
        if (!RegisterClassExW(&klass)) {
            return LastErrorResult();
        }
        registered_ = true;

        constexpr DWORD style = WS_OVERLAPPEDWINDOW | WS_CLIPCHILDREN;
        const UINT dpi = WindowDPI(nullptr);
        RECT bounds{0, 0, MulDiv(1120, dpi, USER_DEFAULT_SCREEN_DPI),
                           MulDiv(780, dpi, USER_DEFAULT_SCREEN_DPI)};
        using AdjustForDPI = BOOL(WINAPI*)(LPRECT, DWORD, BOOL, DWORD, UINT);
        const auto adjustForDPI = User32Function<AdjustForDPI>("AdjustWindowRectExForDpi");
        const BOOL adjusted = adjustForDPI ? adjustForDPI(&bounds, style, FALSE, 0, dpi)
                                          : AdjustWindowRectEx(&bounds, style, FALSE, 0);
        if (!adjusted) {
            return LastErrorResult();
        }
        window_ = CreateWindowExW(0, className_, L"TailSplit", style,
                                  CW_USEDEFAULT, CW_USEDEFAULT,
                                  bounds.right - bounds.left, bounds.bottom - bounds.top,
                                  nullptr, nullptr, instance_, this);
        if (!window_) {
            return LastErrorResult();
        }
        StartupStage(1); // Native HWND created; its browser is not ready yet.
        const HRESULT hr = StartEnvironment();
        if (FAILED(hr)) {
            Stop(hr);
        }

        // Quit while either creation callback is pending hides the window but
        // keeps the parent HWND, STA, host and DLL alive until that callback has
        // completed. Never return with a callback still borrowing this host.
        for (;;) {
            const BOOL received = GetMessageW(&message, nullptr, 0, 0);
            if (received == -1) {
                Stop(LastErrorResult());
                continue;
            }
            if (received == 0) {
                if (finished_) {
                    break;
                }
                Stop(S_OK);
                continue;
            }
            if (!message.hwnd && message.message == kFinish) {
                Finish();
            } else {
                TranslateMessage(&message);
                DispatchMessageW(&message);
            }
        }
        return result_;
    }

private:
    void StartupStage(UINT stage) noexcept {
        if (window_) {
            SetPropW(window_, kStartupStage, reinterpret_cast<HANDLE>(static_cast<UINT_PTR>(stage)));
        }
    }

    struct Completion {
        WindowHost* host;
        ~Completion() {
            --host->pending_;
            host->ScheduleFinish();
        }
    };

    bool Allows(const wchar_t* uri) const noexcept {
        unsigned port = 0;
        return !stopping_ && LoopbackPort(uri, false, port) && port == port_;
    }

    HRESULT StartEnvironment() noexcept {
        const auto handler = Callback<ICoreWebView2CreateCoreWebView2EnvironmentCompletedHandler>(
            [this](HRESULT hr, ICoreWebView2Environment* environment) -> HRESULT {
                Completion completion{this};
                StartupStage(3); // Environment completion reached the STA.
                environment_ = environment;
                if (stopping_) {
                    ScheduleFinish();
                    return S_OK;
                }
                if (FAILED(hr) || !environment) {
                    Stop(FAILED(hr) ? hr : E_POINTER);
                    return S_OK;
                }
                const auto controllerHandler = Callback<ICoreWebView2CreateCoreWebView2ControllerCompletedHandler>(
                    [this](HRESULT result, ICoreWebView2Controller* controller) -> HRESULT {
                        Completion completion{this};
                        StartupStage(5); // Controller completion reached the STA.
                        controller_ = controller;
                        if (stopping_) {
                            ScheduleFinish();
                            return S_OK;
                        }
                        if (FAILED(result) || !controller) {
                            Stop(FAILED(result) ? result : E_POINTER);
                            return S_OK;
                        }
                        HRESULT configured = ConfigureBrowser();
                        if (SUCCEEDED(configured)) {
                            StartupStage(6); // Local-origin restrictions are installed.
                            configured = Resize();
                        }
                        if (SUCCEEDED(configured)) {
                            // Pass the original URL verbatim, including its capability fragment.
                            StartupStage(7); // Waiting for the embedded document.
                            configured = webview_->Navigate(url_);
                        }
                        if (FAILED(configured)) {
                            Stop(configured);
                        }
                        return S_OK;
                    });
                if (!controllerHandler) {
                    Stop(E_OUTOFMEMORY);
                    return S_OK;
                }
                ++pending_;
                StartupStage(4); // Asynchronous controller creation requested.
                const HRESULT created = environment_->CreateCoreWebView2Controller(window_, controllerHandler.Get());
                if (FAILED(created)) {
                    --pending_;
                    Stop(created);
                }
                return S_OK;
            });
        if (!handler) {
            return E_OUTOFMEMORY;
        }
        ++pending_;
        // The installed runtime and caller's private data directory are used as
        // supplied. This host never downloads or installs a browser runtime.
        StartupStage(2); // Asynchronous browser environment creation requested.
        const HRESULT hr = CreateCoreWebView2EnvironmentWithOptions(nullptr, userData_, nullptr, handler.Get());
        if (FAILED(hr)) {
            --pending_;
        }
        return hr;
    }

    HRESULT ConfigureBrowser() noexcept {
        HRESULT hr = controller_->get_CoreWebView2(&webview_);
        if (FAILED(hr)) {
            return hr;
        }
        ComPtr<ICoreWebView2Settings> settings;
        if (FAILED(hr = webview_->get_Settings(&settings))) {
            return hr;
        }
        if (FAILED(hr = settings->put_AreDevToolsEnabled(FALSE)) ||
            FAILED(hr = settings->put_AreDefaultContextMenusEnabled(FALSE)) ||
            FAILED(hr = settings->put_AreDefaultScriptDialogsEnabled(FALSE)) ||
            FAILED(hr = settings->put_AreHostObjectsAllowed(FALSE)) ||
            FAILED(hr = settings->put_IsWebMessageEnabled(FALSE)) ||
            FAILED(hr = settings->put_IsStatusBarEnabled(FALSE)) ||
            FAILED(hr = settings->put_IsZoomControlEnabled(FALSE)) ||
            FAILED(hr = settings->put_IsBuiltInErrorPageEnabled(FALSE))) {
            return hr;
        }
        ComPtr<ICoreWebView2Settings3> keyboardSettings;
        if (FAILED(hr = settings.As(&keyboardSettings)) ||
            FAILED(hr = keyboardSettings->put_AreBrowserAcceleratorKeysEnabled(FALSE))) {
            return hr;
        }
        ComPtr<ICoreWebView2Settings4> formSettings;
        if (SUCCEEDED(settings.As(&formSettings))) {
            if (FAILED(hr = formSettings->put_IsPasswordAutosaveEnabled(FALSE)) ||
                FAILED(hr = formSettings->put_IsGeneralAutofillEnabled(FALSE))) {
                return hr;
            }
        }
        // Fail on older runtimes rather than opening a view without the APIs
        // needed to cancel downloads and native external-protocol launching.
        if (FAILED(hr = webview_.As(&downloads_)) || FAILED(hr = webview_.As(&externalURIs_))) {
            return hr;
        }
        if (FAILED(hr = environment_->CreateWebResourceResponse(
                       nullptr, 403, L"Forbidden", L"Content-Length: 0\r\nCache-Control: no-store\r\n",
                       &blockedResponse_))) {
            return hr;
        }

        hr = navigation_.Add(webview_.Get(), &ICoreWebView2::add_NavigationStarting,
                             &ICoreWebView2::remove_NavigationStarting,
            Callback<ICoreWebView2NavigationStartingEventHandler>(
                [this](ICoreWebView2*, ICoreWebView2NavigationStartingEventArgs* args) -> HRESULT {
                    const HRESULT result = CheckNavigation(args);
                    if (result == S_OK && !ready_ && !stopping_) {
                        const HRESULT idResult = args->get_NavigationId(&loadingNavigation_);
                        if (FAILED(idResult)) {
                            Stop(idResult);
                        } else {
                            navigationPending_ = true;
                        }
                    }
                    return S_OK;
                }));
        if (FAILED(hr)) {
            return hr;
        }
        hr = frameNavigation_.Add(webview_.Get(), &ICoreWebView2::add_FrameNavigationStarting,
                                  &ICoreWebView2::remove_FrameNavigationStarting,
            Callback<ICoreWebView2NavigationStartingEventHandler>(
                [this](ICoreWebView2*, ICoreWebView2NavigationStartingEventArgs* args) -> HRESULT {
                    return CheckNavigation(args);
                }));
        if (FAILED(hr)) {
            return hr;
        }
        hr = newWindows_.Add(webview_.Get(), &ICoreWebView2::add_NewWindowRequested,
                             &ICoreWebView2::remove_NewWindowRequested,
            Callback<ICoreWebView2NewWindowRequestedEventHandler>(
                [](ICoreWebView2*, ICoreWebView2NewWindowRequestedEventArgs* args) -> HRESULT {
                    return args->put_Handled(TRUE);
                }));
        if (FAILED(hr)) {
            return hr;
        }
        hr = permissions_.Add(webview_.Get(), &ICoreWebView2::add_PermissionRequested,
                              &ICoreWebView2::remove_PermissionRequested,
            Callback<ICoreWebView2PermissionRequestedEventHandler>(
                [](ICoreWebView2*, ICoreWebView2PermissionRequestedEventArgs* args) -> HRESULT {
                    return args->put_State(COREWEBVIEW2_PERMISSION_STATE_DENY);
                }));
        if (FAILED(hr)) {
            return hr;
        }
        hr = downloadEvents_.Add(downloads_.Get(), &ICoreWebView2_4::add_DownloadStarting,
                                 &ICoreWebView2_4::remove_DownloadStarting,
            Callback<ICoreWebView2DownloadStartingEventHandler>(
                [](ICoreWebView2*, ICoreWebView2DownloadStartingEventArgs* args) -> HRESULT {
                    args->put_Handled(TRUE);
                    return args->put_Cancel(TRUE);
                }));
        if (FAILED(hr)) {
            return hr;
        }
        hr = externalURIEvents_.Add(externalURIs_.Get(), &ICoreWebView2_18::add_LaunchingExternalUriScheme,
                                    &ICoreWebView2_18::remove_LaunchingExternalUriScheme,
            Callback<ICoreWebView2LaunchingExternalUriSchemeEventHandler>(
                [](ICoreWebView2*, ICoreWebView2LaunchingExternalUriSchemeEventArgs* args) -> HRESULT {
                    return args->put_Cancel(TRUE);
                }));
        if (FAILED(hr)) {
            return hr;
        }
        hr = resourceEvents_.Add(webview_.Get(), &ICoreWebView2::add_WebResourceRequested,
                                 &ICoreWebView2::remove_WebResourceRequested,
            Callback<ICoreWebView2WebResourceRequestedEventHandler>(
                [this](ICoreWebView2*, ICoreWebView2WebResourceRequestedEventArgs* args) -> HRESULT {
                    ComPtr<ICoreWebView2WebResourceRequest> request;
                    LPWSTR uri = nullptr;
                    HRESULT result = args->get_Request(&request);
                    if (SUCCEEDED(result)) {
                        result = request->get_Uri(&uri);
                    }
                    const bool allowed = SUCCEEDED(result) && Allows(uri);
                    CoTaskMemFree(uri);
                    if (!allowed) {
                        result = args->put_Response(blockedResponse_.Get());
                        if (FAILED(result)) {
                            webview_->Stop();
                            Stop(result);
                        }
                    }
                    return S_OK;
                }));
        if (FAILED(hr)) {
            return hr;
        }
        if (FAILED(hr = webview_->AddWebResourceRequestedFilter(L"*", COREWEBVIEW2_WEB_RESOURCE_CONTEXT_ALL))) {
            return hr;
        }
        hr = completed_.Add(webview_.Get(), &ICoreWebView2::add_NavigationCompleted,
                            &ICoreWebView2::remove_NavigationCompleted,
            Callback<ICoreWebView2NavigationCompletedEventHandler>(
                [this](ICoreWebView2*, ICoreWebView2NavigationCompletedEventArgs* args) -> HRESULT {
                    if (stopping_ || ready_ || !navigationPending_) {
                        return S_OK;
                    }
                    UINT64 navigation = 0;
                    HRESULT result = args->get_NavigationId(&navigation);
                    if (FAILED(result)) {
                        Stop(result);
                        return S_OK;
                    }
                    // A rejected external navigation must not complete startup
                    // or turn its canceled completion into a local page error.
                    if (navigation != loadingNavigation_) {
                        return S_OK;
                    }
                    BOOL success = FALSE;
                    result = args->get_IsSuccess(&success);
                    if (FAILED(result) || !success) {
                        Stop(FAILED(result) ? result : HRESULT_FROM_WIN32(ERROR_CONNECTION_ABORTED));
                        return S_OK;
                    }
                    if (!SetPropW(window_, kReady, reinterpret_cast<HANDLE>(1))) {
                        Stop(LastErrorResult());
                        return S_OK;
                    }
                    ready_ = true;
                    StartupStage(8); // The local document completed successfully.
                    if (wantVisible_) {
                        Show();
                    }
                    return S_OK;
                }));
        if (FAILED(hr)) {
            return hr;
        }
        return controller_->put_IsVisible(FALSE);
    }

    HRESULT CheckNavigation(ICoreWebView2NavigationStartingEventArgs* args) noexcept {
        LPWSTR uri = nullptr;
        const HRESULT hr = args->get_Uri(&uri);
        const bool allowed = SUCCEEDED(hr) && Allows(uri);
        CoTaskMemFree(uri);
        if (!allowed) {
            const HRESULT canceled = args->put_Cancel(TRUE);
            if (FAILED(canceled)) {
                webview_->Stop();
                Stop(canceled);
            }
            return S_FALSE;
        }
        return S_OK;
    }

    HRESULT Resize() noexcept {
        if (!controller_ || !window_) {
            return S_OK;
        }
        RECT bounds{};
        if (!GetClientRect(window_, &bounds)) {
            return LastErrorResult();
        }
        return controller_->put_Bounds(bounds);
    }

    void Show() noexcept {
        if (!ready_ || stopping_) {
            return;
        }
        wantVisible_ = true;
        controller_->put_IsVisible(TRUE);
        ShowWindow(window_, IsIconic(window_) ? SW_RESTORE : SW_SHOW);
        SetForegroundWindow(window_);
        controller_->MoveFocus(COREWEBVIEW2_MOVE_FOCUS_REASON_PROGRAMMATIC);
    }

    void Hide() noexcept {
        wantVisible_ = false;
        if (controller_) {
            controller_->put_IsVisible(FALSE);
        }
        if (window_) {
            ShowWindow(window_, SW_HIDE);
        }
    }

    void Stop(HRESULT hr) noexcept {
        if (FAILED(hr) && SUCCEEDED(result_)) {
            result_ = hr;
        }
        stopping_ = true;
        if (window_) {
            RemovePropW(window_, kReady);
        }
        Hide();
        ScheduleFinish();
    }

    void ScheduleFinish() noexcept {
        if (!stopping_ || pending_ || finished_ || finishPosted_) {
            return;
        }
        // Defer Close/DestroyWindow until the current WebView2 callback returns.
        const BOOL posted = window_ ? PostMessageW(window_, kFinish, 0, 0)
                                    : PostThreadMessageW(GetCurrentThreadId(), kFinish, 0, 0);
        if (posted) {
            finishPosted_ = true;
        } else {
            if (SUCCEEDED(result_)) {
                result_ = LastErrorResult();
            }
            Finish();
        }
    }

    void CloseBrowser() noexcept {
        completed_.Reset();
        resourceEvents_.Reset();
        externalURIEvents_.Reset();
        downloadEvents_.Reset();
        permissions_.Reset();
        newWindows_.Reset();
        frameNavigation_.Reset();
        navigation_.Reset();
        if (controller_) {
            controller_->Close();
        }
        blockedResponse_.Reset();
        externalURIs_.Reset();
        downloads_.Reset();
        webview_.Reset();
        controller_.Reset();
        environment_.Reset();
    }

    void Finish() noexcept {
        if (!stopping_ || pending_ || finished_) {
            return;
        }
        finished_ = true;
        CloseBrowser();
        if (window_) {
            DestroyWindow(window_);
        }
        PostQuitMessage(0);
    }

    static LRESULT CALLBACK WindowProc(HWND window, UINT message, WPARAM wparam, LPARAM lparam) noexcept {
        auto* host = reinterpret_cast<WindowHost*>(GetWindowLongPtrW(window, GWLP_USERDATA));
        if (message == WM_NCCREATE) {
            const auto* create = reinterpret_cast<CREATESTRUCTW*>(lparam);
            host = static_cast<WindowHost*>(create->lpCreateParams);
            host->window_ = window;
            SetWindowLongPtrW(window, GWLP_USERDATA, reinterpret_cast<LONG_PTR>(host));
        }
        if (!host) {
            return DefWindowProcW(window, message, wparam, lparam);
        }
        switch (message) {
        case kShow:
            host->Show();
            return 0;
        case kQuit:
            host->Stop(S_OK);
            return 0;
        case kFinish:
            host->Finish();
            return 0;
        case WM_CLOSE:
            host->Hide();
            return 0;
        case WM_SIZE: {
            const HRESULT hr = host->Resize();
            if (FAILED(hr)) {
                host->Stop(hr);
            }
            return 0;
        }
        case WM_MOVE:
        case WM_MOVING:
            if (host->controller_) {
                host->controller_->NotifyParentWindowPositionChanged();
            }
            break;
        case WM_DPICHANGED: {
            const auto* bounds = reinterpret_cast<RECT*>(lparam);
            SetWindowPos(window, nullptr, bounds->left, bounds->top,
                         bounds->right - bounds->left, bounds->bottom - bounds->top,
                         SWP_NOZORDER | SWP_NOACTIVATE);
            if (host->controller_) {
                host->controller_->NotifyParentWindowPositionChanged();
            }
            const HRESULT hr = host->Resize();
            if (FAILED(hr)) {
                host->Stop(hr);
            }
            return 0;
        }
        case WM_GETMINMAXINFO: {
            auto* sizes = reinterpret_cast<MINMAXINFO*>(lparam);
            const UINT dpi = WindowDPI(window);
            sizes->ptMinTrackSize.x = MulDiv(760, dpi, USER_DEFAULT_SCREEN_DPI);
            sizes->ptMinTrackSize.y = MulDiv(560, dpi, USER_DEFAULT_SCREEN_DPI);
            return 0;
        }
        case WM_QUERYENDSESSION:
            return TRUE;
        case WM_ENDSESSION:
            if (wparam) {
                host->Stop(S_OK);
            }
            return 0;
        case WM_DESTROY:
            if (!host->finished_) {
                host->Stop(S_OK);
            }
            return 0;
        case WM_NCDESTROY:
            RemovePropW(window, kReady);
            RemovePropW(window, kStartupStage);
            SetWindowLongPtrW(window, GWLP_USERDATA, 0);
            host->window_ = nullptr;
            // Unexpected destruction may have discarded a queued finish message.
            host->finishPosted_ = false;
            host->ScheduleFinish();
            break;
        }
        return DefWindowProcW(window, message, wparam, lparam);
    }

    const wchar_t* url_;
    const wchar_t* userData_;
    const wchar_t* className_;
    const unsigned port_;
    HINSTANCE instance_ = nullptr;
    HWND window_ = nullptr;
    bool registered_ = false;
    bool stopping_ = false;
    bool finished_ = false;
    bool finishPosted_ = false;
    bool ready_ = false;
    bool wantVisible_ = true;
    bool navigationPending_ = false;
    UINT64 loadingNavigation_ = 0;
    unsigned pending_ = 0;
    HRESULT result_ = S_OK;
    ComPtr<ICoreWebView2Environment> environment_;
    ComPtr<ICoreWebView2Controller> controller_;
    ComPtr<ICoreWebView2> webview_;
    ComPtr<ICoreWebView2_4> downloads_;
    ComPtr<ICoreWebView2_18> externalURIs_;
    ComPtr<ICoreWebView2WebResourceResponse> blockedResponse_;
    Subscription<ICoreWebView2> navigation_;
    Subscription<ICoreWebView2> frameNavigation_;
    Subscription<ICoreWebView2> newWindows_;
    Subscription<ICoreWebView2> permissions_;
    Subscription<ICoreWebView2_4> downloadEvents_;
    Subscription<ICoreWebView2_18> externalURIEvents_;
    Subscription<ICoreWebView2> resourceEvents_;
    Subscription<ICoreWebView2> completed_;
};

} // namespace

// These C ABI exports use the unified x64/ARM64 calling convention. They must be
// exported by name, without a .def file or compiler-generated C++ decoration.
extern "C" __declspec(dllexport) HRESULT __cdecl TailSplitWindowRun(
    const wchar_t* url, const wchar_t* userData, const wchar_t* className) {
    unsigned port = 0;
    if (!LoopbackPort(url, true, port) || !userData || !*userData || PathIsRelativeW(userData) ||
        !className || !*className || wcslen(className) > 255) {
        return E_INVALIDARG;
    }
    if (InterlockedCompareExchange(&activeRun, 1, 0) != 0) {
        return HRESULT_FROM_WIN32(ERROR_ALREADY_EXISTS);
    }
    struct RunGuard {
        ~RunGuard() { InterlockedExchange(&activeRun, 0); }
    } guard;
    ThreadDPI dpi;
    Apartment apartment;
    if (FAILED(apartment.result)) {
        return apartment.result;
    }
    WindowHost host(url, userData, className, port);
    return host.Run();
}

extern "C" __declspec(dllexport) HWND __cdecl TailSplitWindowFind(const wchar_t* className) {
    if (!className || !*className) {
        SetLastError(ERROR_INVALID_PARAMETER);
        return nullptr;
    }
    HWND window = nullptr;
    // A different process can register the same per-user class. Skip it rather
    // than activating or shutting down somebody else's window.
    while ((window = FindWindowExW(nullptr, window, className, nullptr)) != nullptr) {
        if (IsOwnWindow(window)) {
            return window;
        }
    }
    SetLastError(ERROR_NOT_FOUND);
    return nullptr;
}

extern "C" __declspec(dllexport) BOOL __cdecl TailSplitWindowShow(HWND window) {
    if (!IsOwnWindow(window)) {
        SetLastError(ERROR_INVALID_WINDOW_HANDLE);
        return FALSE;
    }
    if (!GetPropW(window, kReady)) {
        SetLastError(ERROR_NOT_READY);
        return FALSE;
    }
    return PostMessageW(window, kShow, 0, 0);
}

extern "C" __declspec(dllexport) BOOL __cdecl TailSplitWindowHide(HWND window) {
    if (!IsOwnWindow(window)) {
        SetLastError(ERROR_INVALID_WINDOW_HANDLE);
        return FALSE;
    }
    return PostMessageW(window, WM_CLOSE, 0, 0);
}

extern "C" __declspec(dllexport) BOOL __cdecl TailSplitWindowQuit(HWND window) {
    if (!IsOwnWindow(window)) {
        SetLastError(ERROR_INVALID_WINDOW_HANDLE);
        return FALSE;
    }
    return PostMessageW(window, kQuit, 0, 0);
}
