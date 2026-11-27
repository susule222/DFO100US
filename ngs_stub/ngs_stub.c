// ngs_stub.c - 美服 US100 客户端 NGClient64.aes 桩模块
//
// 目的：替换 NGS（Nexon Game Security）客户端模块，使客户端离线可启动，
// 同时记录游戏对 NGS 导出函数的调用（序号 1..6），便于逐步确定接口语义。
//
// 返回值可在游戏目录的 ngs_stub.ini 中按序号配置（十六进制），默认 0。
// ord5=0: 不请求 NGS 截屏。旧版 ord5=1 会逐帧分配未被桩接管释放的图像。
// 调用日志写入游戏目录的 ngs_stub.log。
// 与韩服版差异：locale 补丁改为 en-US（0x0409 / CP1252 / OEM 437），
// 不含任何韩服 exe 专用 RVA 内存补丁（US100 的补丁点待定位后另行加入）。
#include <windows.h>
#include <stdio.h>
#include <string.h>
#include <intrin.h>
#include <stdarg.h>

static FILE *g_log = NULL;
static CRITICAL_SECTION g_cs;
static ULONG64 g_ret[8];
static volatile LONG g_inited = 0;

// All legacy logging sites share a bounded sink. Never close/reopen g_log while
// another callback could be using it. The launcher supplies a per-run log path.
static int bounded_log(FILE *unused, const char *format, ...)
{
    int result = 0;
    (void)unused;
    EnterCriticalSection(&g_cs);
    if (g_log != NULL && _ftelli64(g_log) < 2 * 1024 * 1024) {
        va_list args;
        va_start(args, format);
        result = vfprintf(g_log, format, args);
        va_end(args);
    }
    LeaveCriticalSection(&g_cs);
    return result;
}
#define fprintf bounded_log

static void ensure_init(void)
{
    LONG state = InterlockedCompareExchange(&g_inited, 1, 0);
    if (state != 0) {
        return;
    }
    InitializeCriticalSection(&g_cs);
    for (int i = 0; i < 8; i++) {
        g_ret[i] = 0;
    }
    char path[MAX_PATH];
    DWORD n = GetModuleFileNameA(NULL, path, MAX_PATH);
    if (n == 0 || n >= MAX_PATH) {
        return;
    }
    char *p = strrchr(path, '\\');
    if (p == NULL) {
        return;
    }
    strcpy(p + 1, "ngs_stub.ini");
    FILE *f = fopen(path, "r");
    if (f != NULL) {
        char line[128];
        while (fgets(line, sizeof(line), f) != NULL) {
            int ord = 0;
            unsigned long long v = 0;
            if (sscanf(line, "%d=%llx", &ord, &v) == 2 && ord >= 1 && ord <= 6) {
                g_ret[ord] = (ULONG64)v;
            }
        }
        fclose(f);
    }
    strcpy(p + 1, "ngs_stub.log");
    wchar_t run_log[32768];
    DWORD run_len = GetEnvironmentVariableW(L"US100_LOG_PATH", run_log, 32768);
    g_log = (run_len > 0 && run_len < 32768) ? _wfopen(run_log, L"a") : fopen(path, "a");
}

static void log_call(int ord, void *ra, ULONG64 a1, ULONG64 a2, ULONG64 a3, ULONG64 a4)
{
    ensure_init();
    if (g_log == NULL) {
        return;
    }
    // 节流：ord5（状态轮询）/ord6（逐帧调用）只记录前 8 次，
    // 之后每 4000 次记一条汇总，避免日志无限膨胀导致内存/磁盘压力。
    static LONG g_calls[8];
    static LONG g_skipped[8];
    LONG n = InterlockedIncrement(&g_calls[ord]);
    if ((ord == 5 || ord == 6) && n > 8) {
        if ((n % 4000) != 0) {
            InterlockedIncrement(&g_skipped[ord]);
            return;
        }
        LONG skipped = InterlockedExchange(&g_skipped[ord], 0);
        EnterCriticalSection(&g_cs);
        fprintf(g_log,
                "t=%lu ord=%d [throttled] total_calls=%ld skipped=%ld "
                "rcx=%016llX rdx=%016llX r8=%016llX r9=%016llX -> %016llX\n",
                GetTickCount(), ord, n, skipped, a1, a2, a3, a4, g_ret[ord]);
        fflush(g_log);
        LeaveCriticalSection(&g_cs);
        return;
    }
    EnterCriticalSection(&g_cs);
    fprintf(g_log,
            "t=%lu ord=%d caller=%p rcx=%016llX rdx=%016llX r8=%016llX r9=%016llX "
            "-> %016llX\n",
            GetTickCount(), ord, ra, a1, a2, a3, a4, g_ret[ord]);
    fflush(g_log);
    LeaveCriticalSection(&g_cs);
}

// 弹窗旁观线程：与"hook 哪个 API"无关地关掉阻塞启动的对话框。
//
// 背景（2026-09-20）：客户端 NGS 初始化失败后由**游戏自己**弹出
// "RUN ERROR / NGS init failed..(ffffffff)"。桩只挂了 MessageBoxW/A 两个 API：
// 昨天那次会话游戏走 MessageBoxW，被 hook 直接拦掉（return 1），游戏继续到
// 渲染循环；今天同一条对话框走了别的路径（日志里没有 MessageBoxW 记录），框
// 停在屏幕上，用户点掉之后客户端以 0xC0000005 退出，等于整个启动链被卡死。
// 与其继续猜它用哪个 API，不如按标题枚举本进程窗口并把它点掉。
//
// 只处理 ini 里 dismiss= 列出的标题（默认 "RUN ERROR"），绝不碰游戏主窗口。
static volatile LONG g_watch_started = 0;
static char g_dismiss[4][64] = { "RUN ERROR", "", "", "" };

static void load_dismiss_list(void)
{
    char path[MAX_PATH];
    DWORD n = GetModuleFileNameA(NULL, path, MAX_PATH);
    if (n == 0 || n >= MAX_PATH) {
        return;
    }
    char *p = strrchr(path, '\\');
    if (p == NULL) {
        return;
    }
    strcpy(p + 1, "ngs_stub.ini");
    FILE *f = fopen(path, "r");
    if (f == NULL) {
        return;
    }
    char line[256];
    while (fgets(line, sizeof(line), f) != NULL) {
        char *value = NULL;
        if (strncmp(line, "dismiss=", 8) == 0) {
            value = line + 8;
        }
        if (value == NULL) {
            continue;
        }
        int slot = 0;
        char *cursor = value;
        while (slot < 4 && cursor != NULL && *cursor != 0) {
            char *sep = strchr(cursor, ';');
            if (sep != NULL) {
                *sep = 0;
            }
            while (*cursor == ' ' || *cursor == '\t') {
                cursor++;
            }
            char *end = cursor + strlen(cursor);
            while (end > cursor && (end[-1] == '\n' || end[-1] == '\r' || end[-1] == ' ')) {
                *--end = 0;
            }
            if (*cursor != 0) {
                strncpy(g_dismiss[slot], cursor, sizeof(g_dismiss[0]) - 1);
                g_dismiss[slot][sizeof(g_dismiss[0]) - 1] = 0;
                slot++;
            }
            cursor = (sep != NULL) ? sep + 1 : NULL;
        }
    }
    fclose(f);
}

static int title_matches(const wchar_t *title)
{
    for (int i = 0; i < 4; i++) {
        if (g_dismiss[i][0] == 0) {
            continue;
        }
        wchar_t wide[64];
        int k = 0;
        for (; g_dismiss[i][k] != 0 && k < 63; k++) {
            wide[k] = (wchar_t)(unsigned char)g_dismiss[i][k];
        }
        wide[k] = 0;
        if (_wcsicmp(title, wide) == 0) {
            return 1;
        }
    }
    return 0;
}

typedef struct {
    DWORD pid;
    int found;
} dismiss_ctx;

static BOOL CALLBACK dismiss_enum_proc(HWND hwnd, LPARAM param)
{
    dismiss_ctx *ctx = (dismiss_ctx *)param;
    DWORD owner = 0;
    GetWindowThreadProcessId(hwnd, &owner);
    if (owner != ctx->pid) {
        return TRUE;
    }
    wchar_t title[256];
    if (GetWindowTextW(hwnd, title, 256) <= 0) {
        return TRUE;
    }
    if (!title_matches(title)) {
        return TRUE;
    }
    ctx->found = 1;
    // 先按"点确定"送 IDOK；没有 OK 按钮的窗口再用 WM_CLOSE 收尾。
    PostMessageW(hwnd, WM_COMMAND, MAKEWPARAM(IDOK, BN_CLICKED), 0);
    PostMessageW(hwnd, WM_CLOSE, 0, 0);
    if (g_log != NULL) {
        EnterCriticalSection(&g_cs);
        fprintf(g_log, "dialog-dismiss: posted IDOK/WM_CLOSE to \"%ls\"\n", title);
        fflush(g_log);
        LeaveCriticalSection(&g_cs);
    }
    return TRUE;
}

static DWORD WINAPI dismiss_thread(LPVOID unused)
{
    (void)unused;
    dismiss_ctx ctx;
    ctx.pid = GetCurrentProcessId();
    for (;;) {
        ctx.found = 0;
        EnumWindows(dismiss_enum_proc, (LPARAM)&ctx);
        Sleep(ctx.found ? 200 : 500);
    }
    return 0;
}

static void start_dismiss_watcher(void)
{
    if (InterlockedCompareExchange(&g_watch_started, 1, 0) != 0) {
        return;
    }
    load_dismiss_list();
    // 从序数调用里启动，而不是 DllMain：加载器锁里起线程是禁忌。
    HANDLE thread = CreateThread(NULL, 0, dismiss_thread, NULL, 0, NULL);
    if (thread != NULL) {
        CloseHandle(thread);
        if (g_log != NULL) {
            EnterCriticalSection(&g_cs);
            fprintf(g_log, "dialog-dismiss: watcher started (\"%s\")\n", g_dismiss[0]);
            fflush(g_log);
            LeaveCriticalSection(&g_cs);
        }
    }
}

#define NGS_FN(n)                                                        \
    __declspec(dllexport) ULONG64 ngs_fn##n(                             \
        ULONG64 a1, ULONG64 a2, ULONG64 a3, ULONG64 a4)                  \
    {                                                                    \
        start_dismiss_watcher();                                         \
        log_call(n, _ReturnAddress(), a1, a2, a3, a4);                   \
        return g_ret[n];                                                 \
    }

NGS_FN(1)
NGS_FN(2)
NGS_FN(3)
NGS_FN(4)
NGS_FN(5)
NGS_FN(6)

static void log_stack(const char *tag)
{
    void *frames[40];
    USHORT n = RtlCaptureStackBackTrace(0, 40, frames, NULL);
    if (g_log == NULL) {
        return;
    }
    EnterCriticalSection(&g_cs);
    fprintf(g_log, "--- stack(%s) ---\n", tag);
    for (USHORT i = 0; i < n; i++) {
        HMODULE mod = NULL;
        char name[MAX_PATH] = "?";
        if (GetModuleHandleExA(GET_MODULE_HANDLE_EX_FLAG_FROM_ADDRESS |
                                   GET_MODULE_HANDLE_EX_FLAG_UNCHANGED_REFCOUNT,
                               (LPCSTR)frames[i], &mod) &&
            mod != NULL) {
            GetModuleFileNameA(mod, name, MAX_PATH);
        }
        fprintf(g_log, "  #%d %p %s+0x%llX\n", i, frames[i], name,
                (unsigned long long)((BYTE *)frames[i] - (BYTE *)mod));
    }
    fflush(g_log);
    LeaveCriticalSection(&g_cs);
}

static void log_wide(const char *tag, const wchar_t *text)
{
    char utf8[4096];
    utf8[0] = 0;
    if (text != NULL) {
        WideCharToMultiByte(CP_UTF8, 0, text, -1, utf8, sizeof(utf8), NULL, NULL);
    }
    ensure_init();
    if (g_log == NULL) {
        return;
    }
    EnterCriticalSection(&g_cs);
    fprintf(g_log, "%s %s\n", tag, utf8);
    fflush(g_log);
    LeaveCriticalSection(&g_cs);
}

static int WINAPI hook_MessageBoxW(HWND hwnd, LPCWSTR text, LPCWSTR caption, UINT type)
{
    (void)hwnd;
    (void)type;
    log_wide("MessageBoxW text=", text);
    log_wide("MessageBoxW caption=", caption);
    log_stack("MessageBoxW");
    return 1;
}

// GetFileAttributesW 的保存/还原式 detour：记录被检查的路径后调用原函数。
static BYTE g_gfaw_orig[16];
static BYTE g_gfaw_patch[16];
static BYTE *g_gfaw_addr = NULL;
static BYTE g_ods_orig[16];
static BYTE g_ods_patch[16];
static BYTE g_ods_orig_w[16];
static BYTE g_ods_patch_w[16];
static volatile LONG g_gfaw_busy = 0;

static void log_caller(const char *tag, void *ra)
{
    HMODULE mod = NULL;
    char name[MAX_PATH] = "?";
    if (GetModuleHandleExA(GET_MODULE_HANDLE_EX_FLAG_FROM_ADDRESS |
                               GET_MODULE_HANDLE_EX_FLAG_UNCHANGED_REFCOUNT,
                           (LPCSTR)ra, &mod) &&
        mod != NULL) {
        GetModuleFileNameA(mod, name, MAX_PATH);
    }
    if (g_log != NULL) {
        EnterCriticalSection(&g_cs);
        fprintf(g_log, "    %s caller=%s+0x%llX\n", tag, name,
                (unsigned long long)((BYTE *)ra - (BYTE *)mod));
        fflush(g_log);
        LeaveCriticalSection(&g_cs);
    }
}

static DWORD WINAPI hook_GetFileAttributesW(LPCWSTR path)
{
    log_wide("GetFileAttributesW path=", path);
    log_caller("GetFileAttributesW", _ReturnAddress());
    if (g_gfaw_addr == NULL) {
        return 0xFFFFFFFF;
    }
    if (InterlockedExchange(&g_gfaw_busy, 1) != 0) {
        return 0xFFFFFFFF;
    }
    DWORD old = 0;
    VirtualProtect(g_gfaw_addr, 16, PAGE_EXECUTE_READWRITE, &old);
    memcpy(g_gfaw_addr, g_gfaw_orig, 16);
    DWORD ret = ((DWORD(WINAPI *)(LPCWSTR))g_gfaw_addr)(path);
    memcpy(g_gfaw_addr, g_gfaw_patch, 12);
    VirtualProtect(g_gfaw_addr, 16, old, &old);
    FlushInstructionCache(GetCurrentProcess(), g_gfaw_addr, 16);
    InterlockedExchange(&g_gfaw_busy, 0);
    return ret;
}

// msvcr100 的 _waccess/_access 钩子：记录游戏检查的原始路径。
static BYTE g_waccess_orig[16];
static BYTE g_waccess_patch[16];
static BYTE *g_waccess_addr = NULL;
static volatile LONG g_waccess_busy = 0;

static BYTE g_access_orig[16];
static BYTE g_access_patch[16];
static BYTE *g_access_addr = NULL;
static volatile LONG g_access_busy = 0;

static int __cdecl hook_waccess(const wchar_t *path, int mode)
{
    log_wide("_waccess path=", path);
    if (g_waccess_addr == NULL || InterlockedExchange(&g_waccess_busy, 1) != 0) {
        return -1;
    }
    DWORD old = 0;
    VirtualProtect(g_waccess_addr, 16, PAGE_EXECUTE_READWRITE, &old);
    memcpy(g_waccess_addr, g_waccess_orig, 16);
    int ret = ((int(__cdecl *)(const wchar_t *, int))g_waccess_addr)(path, mode);
    memcpy(g_waccess_addr, g_waccess_patch, 12);
    VirtualProtect(g_waccess_addr, 16, old, &old);
    FlushInstructionCache(GetCurrentProcess(), g_waccess_addr, 16);
    InterlockedExchange(&g_waccess_busy, 0);
    return ret;
}

static int __cdecl hook_access(const char *path, int mode)
{
    if (path != NULL && g_log != NULL) {
        EnterCriticalSection(&g_cs);
        fprintf(g_log, "_access ansi path=%s\n", path);
        fflush(g_log);
        LeaveCriticalSection(&g_cs);
    }
    if (g_access_addr == NULL || InterlockedExchange(&g_access_busy, 1) != 0) {
        return -1;
    }
    DWORD old = 0;
    VirtualProtect(g_access_addr, 16, PAGE_EXECUTE_READWRITE, &old);
    memcpy(g_access_addr, g_access_orig, 16);
    int ret = ((int(__cdecl *)(const char *, int))g_access_addr)(path, mode);
    memcpy(g_access_addr, g_access_patch, 12);
    VirtualProtect(g_access_addr, 16, old, &old);
    FlushInstructionCache(GetCurrentProcess(), g_access_addr, 16);
    InterlockedExchange(&g_access_busy, 0);
    return ret;
}

static void install_crt_access_hooks(void)
{
    HMODULE m = GetModuleHandleA("msvcr100.dll");
    if (m == NULL) {
        return;
    }
    BYTE *wa = (BYTE *)GetProcAddress(m, "_waccess");
    if (wa != NULL) {
        g_waccess_addr = wa;
        memcpy(g_waccess_orig, wa, 16);
        memcpy(g_waccess_patch, wa, 16);
        g_waccess_patch[0] = 0x48;
        g_waccess_patch[1] = 0xB8;
        *(void **)(g_waccess_patch + 2) = (void *)hook_waccess;
        g_waccess_patch[10] = 0xFF;
        g_waccess_patch[11] = 0xE0;
        DWORD old = 0;
        VirtualProtect(wa, 16, PAGE_EXECUTE_READWRITE, &old);
        memcpy(wa, g_waccess_patch, 12);
        VirtualProtect(wa, 16, old, &old);
        FlushInstructionCache(GetCurrentProcess(), wa, 16);
    }
    BYTE *a = (BYTE *)GetProcAddress(m, "_access");
    if (a != NULL) {
        g_access_addr = a;
        memcpy(g_access_orig, a, 16);
        memcpy(g_access_patch, a, 16);
        g_access_patch[0] = 0x48;
        g_access_patch[1] = 0xB8;
        *(void **)(g_access_patch + 2) = (void *)hook_access;
        g_access_patch[10] = 0xFF;
        g_access_patch[11] = 0xE0;
        DWORD old = 0;
        VirtualProtect(a, 16, PAGE_EXECUTE_READWRITE, &old);
        memcpy(a, g_access_patch, 12);
        VirtualProtect(a, 16, old, &old);
        FlushInstructionCache(GetCurrentProcess(), a, 16);
    }
}

static BYTE g_gfaa_orig[16];
static BYTE g_gfaa_patch[16];
static BYTE *g_gfaa_addr = NULL;
static volatile LONG g_gfaa_busy = 0;

static DWORD WINAPI hook_GetFileAttributesA(LPCSTR path)
{
    if (path != NULL && g_log != NULL) {
        EnterCriticalSection(&g_cs);
        fprintf(g_log, "GetFileAttributesA path=%s\n", path);
        fflush(g_log);
        LeaveCriticalSection(&g_cs);
    }
    log_caller("GetFileAttributesA", _ReturnAddress());
    if (g_gfaa_addr == NULL || InterlockedExchange(&g_gfaa_busy, 1) != 0) {
        return 0xFFFFFFFF;
    }
    DWORD old = 0;
    VirtualProtect(g_gfaa_addr, 16, PAGE_EXECUTE_READWRITE, &old);
    memcpy(g_gfaa_addr, g_gfaa_orig, 16);
    DWORD ret = ((DWORD(WINAPI *)(LPCSTR))g_gfaa_addr)(path);
    memcpy(g_gfaa_addr, g_gfaa_patch, 12);
    VirtualProtect(g_gfaa_addr, 16, old, &old);
    FlushInstructionCache(GetCurrentProcess(), g_gfaa_addr, 16);
    InterlockedExchange(&g_gfaa_busy, 0);
    return ret;
}

static void install_swap_detour(BYTE *addr, void *hookfn, BYTE *orig, BYTE *patch)
{
    memcpy(orig, addr, 16);
    memcpy(patch, addr, 16);
    patch[0] = 0x48;
    patch[1] = 0xB8;
    *(void **)(patch + 2) = hookfn;
    patch[10] = 0xFF;
    patch[11] = 0xE0;
    DWORD old = 0;
    VirtualProtect(addr, 16, PAGE_EXECUTE_READWRITE, &old);
    memcpy(addr, patch, 12);
    VirtualProtect(addr, 16, old, &old);
    FlushInstructionCache(GetCurrentProcess(), addr, 16);
}

static LONG g_ods_lines = 0;
static LONG g_ods_total = 0;

// The client reports packet parse failures through OutputDebugString only (the
// NOTI_START_MAP error literals), which is invisible without a debugger. This
// hook keeps the interesting lines in the stub log so a silent client failure
// can be diagnosed from the server side. Bounded: filtered and hard-capped.
static void log_ods(const char *text)
{
    if (text == NULL) {
        return;
    }
    InterlockedIncrement(&g_ods_total);
    if (g_ods_total > 4000) {
        return;
    }
    int interesting = 0;
    for (const char *p = text; *p != 0; p++) {
        if ((p[0] == 'N' && p[1] == 'O' && p[2] == 'T' && p[3] == 'I') ||
            (p[0] == 'E' && p[1] == 'R' && p[2] == 'R') ||
            (p[0] == 'e' && p[1] == 'r' && p[2] == 'r') ||
            (p[0] == 'D' && p[1] == 'U' && p[2] == 'N' && p[3] == 'G')) {
            interesting = 1;
            break;
        }
    }
    if (!interesting || g_ods_lines > 1500 || g_log == NULL) {
        return;
    }
    InterlockedIncrement(&g_ods_lines);
    EnterCriticalSection(&g_cs);
    fprintf(g_log, "[ods] %s\n", text);
    fflush(g_log);
    LeaveCriticalSection(&g_cs);
}

static void WINAPI hook_OutputDebugStringA(LPCSTR text)
{
    ensure_init();
    log_ods(text);
}

static void WINAPI hook_OutputDebugStringW(LPCWSTR text)
{
    ensure_init();
    if (text == NULL) {
        return;
    }
    char utf8[2048];
    utf8[0] = 0;
    WideCharToMultiByte(CP_UTF8, 0, text, -1, utf8, sizeof(utf8), NULL, NULL);
    log_ods(utf8);
}

static void install_ods_hook(void)
{
    HMODULE m = GetModuleHandleA("kernelbase.dll");
    if (m == NULL) {
        m = GetModuleHandleA("kernel32.dll");
    }
    if (m == NULL) {
        return;
    }
    BYTE *a = (BYTE *)GetProcAddress(m, "OutputDebugStringA");
    BYTE *w = (BYTE *)GetProcAddress(m, "OutputDebugStringW");
    if (a != NULL) {
        install_swap_detour(a, (void *)hook_OutputDebugStringA, g_ods_orig, g_ods_patch);
    }
    if (w != NULL) {
        install_swap_detour(w, (void *)hook_OutputDebugStringW, g_ods_orig_w, g_ods_patch_w);
    }
    if (g_log != NULL) {
        fprintf(g_log, "ods-hook: A=%p W=%p\n", (void *)a, (void *)w);
        fflush(g_log);
    }
}

static void install_gfaw_hook(void)
{
    HMODULE m = GetModuleHandleA("kernelbase.dll");
    if (m == NULL) {
        m = GetModuleHandleA("kernel32.dll");
    }
    if (m == NULL) {
        return;
    }
    BYTE *p = (BYTE *)GetProcAddress(m, "GetFileAttributesW");
    if (p != NULL) {
        g_gfaw_addr = p;
        install_swap_detour(p, (void *)hook_GetFileAttributesW, g_gfaw_orig, g_gfaw_patch);
    }
    BYTE *a = (BYTE *)GetProcAddress(m, "GetFileAttributesA");
    if (a != NULL) {
        g_gfaa_addr = a;
        install_swap_detour(a, (void *)hook_GetFileAttributesA, g_gfaa_orig, g_gfaa_patch);
    }
}

static int WINAPI hook_MessageBoxA(HWND hwnd, LPCSTR text, LPCSTR caption, UINT type)
{
    (void)hwnd;
    (void)type;
    ensure_init();
    if (g_log != NULL) {
        EnterCriticalSection(&g_cs);
        fprintf(g_log, "MessageBoxA text=%s caption=%s\n",
                text != NULL ? text : "", caption != NULL ? caption : "");
        fflush(g_log);
        LeaveCriticalSection(&g_cs);
    }
    log_stack("MessageBoxA");
    return 1;
}

static void install_jmp_hook(const char *dll, const char *name, void *hookfn)
{
    HMODULE m = GetModuleHandleA(dll);
    if (m == NULL) {
        return;
    }
    BYTE *p = (BYTE *)GetProcAddress(m, name);
    if (p == NULL) {
        return;
    }
    DWORD old = 0;
    if (!VirtualProtect(p, 14, PAGE_EXECUTE_READWRITE, &old)) {
        return;
    }
    p[0] = 0x48;
    p[1] = 0xB8;
    *(void **)(p + 2) = hookfn;
    p[10] = 0xFF;
    p[11] = 0xE0;
    VirtualProtect(p, 14, old, &old);
    FlushInstructionCache(GetCurrentProcess(), p, 14);
}

// 把返回 DWORD 的 API 直接改写成 "mov eax, imm32; ret"，让游戏看到美国区域设置。
static void patch_ret_dword(const char *dll, const char *name, DWORD value)
{
    HMODULE m = GetModuleHandleA(dll);
    if (m == NULL) {
        return;
    }
    BYTE *p = (BYTE *)GetProcAddress(m, name);
    if (p == NULL) {
        return;
    }
    DWORD old = 0;
    if (!VirtualProtect(p, 6, PAGE_EXECUTE_READWRITE, &old)) {
        return;
    }
    p[0] = 0xB8;
    *(DWORD *)(p + 1) = value;
    p[5] = 0xC3;
    VirtualProtect(p, 6, old, &old);
    FlushInstructionCache(GetCurrentProcess(), p, 6);
}

// ---- 游戏窗口保护：直接 hook 游戏主窗口过程（RVA 0x38B7350）----
// 背景：客户端初始化 NGS 失败后会向刚创建的 "Dungeon Fighter Online" 主窗口
// 发 WM_CLOSE，窗口销毁导致后续 D3D9 CreateDevice / DirectInput 拿不到有效
// hwnd，游戏报 "Failed to set up the graphics system" 后退出。
// 在窗口过程入口拦截 WM_CLOSE/WM_DESTROY/WM_NCDESTROY，直接返回 0。
#define GAME_WNDPROC_RVA 0x38B7350

static void install_swap_detour(BYTE *addr, void *hookfn, BYTE *orig, BYTE *patch);

static WNDPROC g_wp_original = NULL;
static ULONGLONG g_wp_deadline = 0;

static LRESULT WINAPI hook_game_wndproc(HWND hwnd, UINT msg, WPARAM w, LPARAM l)
{
    if (msg == WM_CLOSE && GetTickCount64() < g_wp_deadline) {
        if (g_log != NULL) {
            EnterCriticalSection(&g_cs);
            fprintf(g_log, "window-guard: swallowed msg=0x%X for game window %p\n", msg, (void *)hwnd);
            fflush(g_log);
            LeaveCriticalSection(&g_cs);
        }
        return 0;
    }
    return g_wp_original != NULL ? g_wp_original(hwnd, msg, w, l) : 0;
}

static void install_window_guard(void)
{
    HMODULE exe = GetModuleHandleA(NULL);
    if (exe == NULL) {
        return;
    }
    BYTE *p = (BYTE *)exe + GAME_WNDPROC_RVA;
    // Exact 13-byte prologue consists only of pushes and sub rsp, 0x68.
    // Copy complete instructions into a trampoline, then use a RIP-indirect
    // jump back. No restore/call/repatch races and no lost reentrant messages.
    static const BYTE expected[] = {0x40,0x53,0x55,0x56,0x57,0x41,0x56,0x41,0x57,0x48,0x83,0xEC,0x68};
    if (memcmp(p, expected, sizeof(expected)) != 0) {
        if (g_log) fprintf(g_log, "window-guard: prologue mismatch, not installed\n");
        return;
    }
    BYTE *trampoline = (BYTE *)VirtualAlloc(NULL, 4096, MEM_COMMIT | MEM_RESERVE, PAGE_READWRITE);
    if (trampoline == NULL) return;
    memcpy(trampoline, expected, sizeof(expected));
    BYTE *jump = trampoline + sizeof(expected);
    jump[0] = 0xFF; jump[1] = 0x25;
    memset(jump + 2, 0, 4);
    *(void **)(jump + 6) = p + sizeof(expected);
    DWORD old = 0;
    if (!VirtualProtect(trampoline, 4096, PAGE_EXECUTE_READ, &old) ||
        !VirtualProtect(p, sizeof(expected), PAGE_EXECUTE_READWRITE, &old)) {
        VirtualFree(trampoline, 0, MEM_RELEASE);
        return;
    }
    g_wp_original = (WNDPROC)(void *)trampoline;
    g_wp_deadline = GetTickCount64() + 30000;
    p[0] = 0x48; p[1] = 0xB8;
    *(void **)(p + 2) = (void *)hook_game_wndproc;
    p[10] = 0xFF; p[11] = 0xE0; p[12] = 0x90;
    VirtualProtect(p, sizeof(expected), old, &old);
    FlushInstructionCache(GetCurrentProcess(), trampoline, 64);
    FlushInstructionCache(GetCurrentProcess(), p, sizeof(expected));
    if (g_log != NULL) {
        EnterCriticalSection(&g_cs);
        fprintf(g_log, "window-guard: hooked game wndproc at %p\n", (void *)p);
        fflush(g_log);
        LeaveCriticalSection(&g_cs);
    }
}

static void install_locale_hooks(void)
{
    static const char *dlls[] = { "kernel32.dll", "kernelbase.dll" };
    for (int i = 0; i < 2; i++) {
        const char *dll = dlls[i];
        patch_ret_dword(dll, "GetACP", 1252);                  // 英语 ANSI 代码页
        patch_ret_dword(dll, "GetOEMCP", 437);
        patch_ret_dword(dll, "GetSystemDefaultLCID", 0x0409);  // en-US
        patch_ret_dword(dll, "GetUserDefaultLCID", 0x0409);
        patch_ret_dword(dll, "GetSystemDefaultLangID", 0x0409);
        patch_ret_dword(dll, "GetUserDefaultLangID", 0x0409);
        patch_ret_dword(dll, "GetSystemDefaultUILanguage", 0x0409);
        patch_ret_dword(dll, "GetUserDefaultUILanguage", 0x0409);
        patch_ret_dword(dll, "GetThreadLocale", 0x0409);
        patch_ret_dword(dll, "GetConsoleCP", 437);
        patch_ret_dword(dll, "GetConsoleOutputCP", 437);
    }
}

// 按 RVA 给主程序打内存补丁（校验原字节后写入）。US100 专用补丁点后续按需加入。
static void patch_game_bytes(DWORD rva, const BYTE *expected, const BYTE *replacement, int len)
{
    HMODULE exe = GetModuleHandleA(NULL);
    if (exe == NULL) {
        return;
    }
    BYTE *p = (BYTE *)exe + rva;
    if (memcmp(p, expected, len) != 0) {
        if (g_log != NULL) {
            EnterCriticalSection(&g_cs);
            fprintf(g_log, "patch %#x: unexpected bytes", rva);
            for (int i = 0; i < len; i++) {
                fprintf(g_log, " %02X", p[i]);
            }
            fprintf(g_log, "\n");
            fflush(g_log);
            LeaveCriticalSection(&g_cs);
        }
        return;
    }
    DWORD old = 0;
    VirtualProtect(p, len, PAGE_EXECUTE_READWRITE, &old);
    memcpy(p, replacement, len);
    VirtualProtect(p, len, old, &old);
    FlushInstructionCache(GetCurrentProcess(), p, len);
    if (g_log != NULL) {
        EnterCriticalSection(&g_cs);
        fprintf(g_log, "patch %#x: patched\n", rva);
        fflush(g_log);
        LeaveCriticalSection(&g_cs);
    }
}

static void patch_game_startup_checks(void)
{
    // US100 NGS 初始化成功判定：0x4EC40B7 的 je 0x4EC40E1 -> jmp，
    // 强制 init-OK 包装函数返回成功（等价于跳过 NGS 失败退出路径）。
    static const BYTE ngs_expected[] = { 0x74, 0x28 };
    static const BYTE ngs_replacement[] = { 0xEB, 0x28 };
    patch_game_bytes(0x4EC40B7, ngs_expected, ngs_replacement, 2);
}

// ---- 面板供数（115US 形式）：在索引 getter 边界覆写返回值 ----
// 实测依据（2026-09-22，活体捕获 + 截图）：
//   * 面板显示值（HP/MP/四维/攻防/暴击/速度…）来自三个带索引的 stat getter：
//       0x3ED8E50 (rcx=src, edx=idx)          -> eax   int
//       0x3EDF9E0 (rcx, edx, r8d, r9d, 栈×2)  -> xmm0  float
//       0x3EDAFC0 (rcx, edx, r8)              -> xmm0  float
//     它们读 stat 源对象 +0x1330/+0x1428 的索引映射表（链表节点 key@+0x1C）。
//   * 安全槽数组 +0x30C+8k 的直写实验（首轮 panelfill）对显示无效：显示不读它。
//   * 强制补跑结算 settle(actor,1) 安全但不触发面板写方（vtable[45]）。
//   所以 115 式"服务端值直达面板"的正确注入点就是 getter 边界：钩住三个 getter，
//   对 idx 0..28 返回标记值（2000+idx），面板即读数——哪个字段显示哪个标记，
//   一次截图得到完整"索引→字段"映射；随后把标记换成服务端真值即点亮面板。
// 三个函数的序言都是完整指令边界（12/14/15 字节），trampoline 安全。
// 开关：ngs_stub.ini 里 panelfill=1（默认 0，对正常启动零影响）。
#define PF_INT_GETTER_RVA  0x3ED8E50
#define PF_F3_GETTER_RVA   0x3EDF9E0
#define PF_F2_GETTER_RVA   0x3EDAFC0
#define PF_IDX_MAX         127

static volatile LONG g_pf_on = 0;
static BYTE *g_pf_exe2 = NULL;

typedef int   (*pf_int_fn)(void *src, int idx);
typedef float (*pf_f3_fn)(void *src, int idx, unsigned a, unsigned b, float c, int d);
typedef float (*pf_f2_fn)(void *src, int idx, void *r8);
static pf_int_fn g_pf_orig_int = NULL;
static pf_f3_fn g_pf_orig_f3 = NULL;
static pf_f2_fn g_pf_orig_f2 = NULL;
static int g_pf_log_budget = 40;

static void pf_log_hit(const char *which, int idx, void *ret)
{
    if (g_pf_log_budget <= 0 || g_log == NULL) {
        return;
    }
    g_pf_log_budget--;
    fprintf(g_log, "panelfill: %s idx=%d caller=%p\n", which, idx, ret);
    fflush(g_log);
}

static int __fastcall hook_pf_int(void *src, int idx)
{
    if (g_pf_on) {
        pf_log_hit("int", idx, _ReturnAddress());
        return 2000 + idx;
    }
    return g_pf_orig_int(src, idx);
}

static float __fastcall hook_pf_f3(void *src, int idx, unsigned a, unsigned b, float c, int d)
{
    if (g_pf_on) {
        pf_log_hit("f3", idx, _ReturnAddress());
        return (float)(2000 + idx);
    }
    return g_pf_orig_f3(src, idx, a, b, c, d);
}

static float __fastcall hook_pf_f2(void *src, int idx, void *r8)
{
    if (g_pf_on) {
        pf_log_hit("f2", idx, _ReturnAddress());
        return (float)(2000 + idx);
    }
    return g_pf_orig_f2(src, idx, r8);
}

// 复制 nbytes 序言到可执行 trampoline，尾部跳回原函数 +nbytes。
static void *pf_trampoline(BYTE *p, int nbytes)
{
    BYTE *t = (BYTE *)VirtualAlloc(NULL, 64, MEM_COMMIT | MEM_RESERVE, PAGE_READWRITE);
    DWORD old = 0;
    if (t == NULL) {
        return NULL;
    }
    memcpy(t, p, nbytes);
    t[nbytes] = 0xFF;
    t[nbytes + 1] = 0x25;
    memset(t + nbytes + 2, 0, 4);
    *(void **)(t + nbytes + 6) = p + nbytes;
    if (!VirtualProtect(t, 64, PAGE_EXECUTE_READ, &old)) {
        VirtualFree(t, 0, MEM_RELEASE);
        return NULL;
    }
    FlushInstructionCache(GetCurrentProcess(), t, 64);
    return t;
}

// 12 字节 RIP 间接跳转补丁：48 B8 <imm64> FF E0，其余填 nop。
static int pf_patch_jmp(BYTE *p, void *hook, int nbytes)
{
    DWORD old = 0;
    int i;
    if (!VirtualProtect(p, nbytes, PAGE_EXECUTE_READWRITE, &old)) {
        return 0;
    }
    p[0] = 0x48;
    p[1] = 0xB8;
    *(void **)(p + 2) = hook;
    p[10] = 0xFF;
    p[11] = 0xE0;
    for (i = 12; i < nbytes; i++) {
        p[i] = 0x90;
    }
    VirtualProtect(p, nbytes, old, &old);
    FlushInstructionCache(GetCurrentProcess(), p, nbytes);
    return 1;
}

static void install_panel_fill(void)
{
    HMODULE exe = GetModuleHandleA(NULL);
    BYTE *pi, *p3, *p2;
    static const BYTE exp_int[] = {0x48,0x8B,0xC4,0x55,0x41,0x54,0x41,0x55,0x41,0x56,0x41,0x57};
    static const BYTE exp_f3[]  = {0x44,0x89,0x44,0x24,0x18,0x55,0x53,0x56,0x41,0x54,0x41,0x56,0x41,0x57};
    static const BYTE exp_f2[]  = {0x48,0x89,0x5C,0x24,0x08,0x48,0x89,0x74,0x24,0x10,0x57,0x48,0x83,0xEC,0x50};
    if (exe == NULL) {
        return;
    }
    g_pf_exe2 = (BYTE *)exe;
    pi = (BYTE *)exe + PF_INT_GETTER_RVA;
    p3 = (BYTE *)exe + PF_F3_GETTER_RVA;
    p2 = (BYTE *)exe + PF_F2_GETTER_RVA;
    if (memcmp(pi, exp_int, sizeof(exp_int)) != 0 ||
        memcmp(p3, exp_f3, sizeof(exp_f3)) != 0 ||
        memcmp(p2, exp_f2, sizeof(exp_f2)) != 0) {
        fprintf(g_log, "panelfill: getter prologue mismatch, not installed\n");
        return;
    }
    g_pf_orig_int = (pf_int_fn)pf_trampoline(pi, (int)sizeof(exp_int));
    g_pf_orig_f3 = (pf_f3_fn)pf_trampoline(p3, (int)sizeof(exp_f3));
    g_pf_orig_f2 = (pf_f2_fn)pf_trampoline(p2, (int)sizeof(exp_f2));
    if (g_pf_orig_int == NULL || g_pf_orig_f3 == NULL || g_pf_orig_f2 == NULL) {
        fprintf(g_log, "panelfill: trampoline alloc failed\n");
        return;
    }
    if (!pf_patch_jmp(pi, (void *)hook_pf_int, (int)sizeof(exp_int)) ||
        !pf_patch_jmp(p3, (void *)hook_pf_f3, (int)sizeof(exp_f3)) ||
        !pf_patch_jmp(p2, (void *)hook_pf_f2, (int)sizeof(exp_f2))) {
        fprintf(g_log, "panelfill: jmp patch failed\n");
        return;
    }
    InterlockedExchange(&g_pf_on, 1);
    fprintf(g_log, "panelfill: getter overrides installed (idx 0..%d -> 2000+idx)\n", PF_IDX_MAX);
}

BOOL WINAPI DllMain(HINSTANCE inst, DWORD reason, LPVOID reserved)
{
    (void)inst;
    (void)reserved;
    if (reason == DLL_PROCESS_ATTACH) {
        ensure_init();
        // 可通过 ngs_stub.ini 的 hooks=0 禁用全部 API 钩子（用于二分排障）
        int hooks_on = 1;
        int panelfill_on = 0;
        {
            char path[MAX_PATH];
            DWORD n = GetModuleFileNameA(NULL, path, MAX_PATH);
            if (n != 0 && n < MAX_PATH) {
                char *p = strrchr(path, '\\');
                if (p != NULL) {
                    strcpy(p + 1, "ngs_stub.ini");
                    FILE *f = fopen(path, "r");
                    if (f != NULL) {
                        char line[128];
                        while (fgets(line, sizeof(line), f) != NULL) {
                            int v = 0;
                            if (sscanf(line, "hooks=%d", &v) == 1) {
                                hooks_on = v;
                            }
                            if (sscanf(line, "panelfill=%d", &v) == 1) {
                                panelfill_on = v;
                            }
                        }
                        fclose(f);
                    }
                }
            }
        }
        if (hooks_on) {
            install_locale_hooks();
            install_jmp_hook("user32.dll", "MessageBoxW", (void *)hook_MessageBoxW);
            install_jmp_hook("user32.dll", "MessageBoxA", (void *)hook_MessageBoxA);
            // File-access swapping detours are legacy diagnostic hooks with
            // cross-thread races. They are not needed for game startup.
            install_window_guard();
            install_ods_hook();
        }
        patch_game_startup_checks();
        if (panelfill_on) {
            install_panel_fill();
        }
        if (g_log != NULL) {
            EnterCriticalSection(&g_cs);
            fprintf(g_log, "=== ngs_stub attached pid=%lu GetACP=%lu hooks=%d ===\n",
                    GetCurrentProcessId(), GetACP(), hooks_on);
            fflush(g_log);
            LeaveCriticalSection(&g_cs);
        }
        DisableThreadLibraryCalls(inst);
    } else if (reason == DLL_PROCESS_DETACH) {
        if (g_log != NULL) {
            EnterCriticalSection(&g_cs);
            fprintf(g_log, "=== ngs_stub detached pid=%lu ===\n", GetCurrentProcessId());
            fflush(g_log);
            LeaveCriticalSection(&g_cs);
        }
    }
    return TRUE;
}
