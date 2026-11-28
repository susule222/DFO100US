// ngs_plugin_host.inc - 100US Client External DLL Plugin Host (ABI v1)
//
// Included by ngs_stub.c. Loads 64-bit external plugin DLLs from `<DFO_DIR>\plugins\`
// (or ngs_stub.ini `plugins_dir=` / env `US100_CLIENT_PLUGINS_DIR`) safely outside
// DllMain on the first NGS export call.

#include "../sdk/plugin/us100_client_plugin.h"

#define US100_MAX_CLIENT_PLUGINS 64
#define US100_MAX_WNDPROC_HOOKS  32
#define US100_MAX_TICK_HOOKS     32
#define US100_MAX_STAT_HOOKS     16

typedef struct {
    HMODULE module;
    us100_client_plugin_shutdown_fn fn_shutdown;
    char file[MAX_PATH];
    char name[64];
    char version[32];
} us100_loaded_client_plugin;

typedef struct {
    int32_t handle;
    us100_wndproc_cb cb;
    void *user_data;
} us100_wndproc_slot;

typedef struct {
    int32_t handle;
    us100_tick_cb cb;
    void *user_data;
} us100_tick_slot;

typedef struct {
    int32_t handle;
    us100_stat_int_cb int_cb;
    us100_stat_f2_cb f2_cb;
    us100_stat_f3_cb f3_cb;
    void *user_data;
} us100_stat_slot;

static CRITICAL_SECTION g_plugin_cs;
static volatile LONG g_plugin_cs_ready = 0;
static volatile LONG g_plugins_loaded = 0;
static volatile LONG g_plugins_shutdown = 0;
static volatile LONG g_next_hook_handle = 0;
static volatile LONG64 g_client_tick_seq = 0;

static int g_plugins_enabled = 1;
static char g_plugins_dir_cfg[260] = "plugins";
static char g_game_dir_utf8[1024] = "";
static char g_plugins_dir_utf8[1024] = "";

static us100_loaded_client_plugin g_client_plugins[US100_MAX_CLIENT_PLUGINS];
static int g_client_plugin_count = 0;

static us100_wndproc_slot g_wndproc_hooks[US100_MAX_WNDPROC_HOOKS];
static us100_tick_slot g_tick_hooks[US100_MAX_TICK_HOOKS];
static us100_stat_slot g_stat_hooks[US100_MAX_STAT_HOOKS];

static HMODULE g_stub_module = NULL;
static US100ClientHostAPI g_client_host_api;

// Forward declaration provided by ngs_stub.c
static int ensure_panel_getters_installed(void);

static void plugin_host_init_cs(void)
{
    if (InterlockedCompareExchange(&g_plugin_cs_ready, 1, 0) == 0) {
        InitializeCriticalSection(&g_plugin_cs);
    }
}

static const char *plugin_level_name(int32_t level)
{
    switch (level) {
    case US100_LOG_DEBUG: return "debug";
    case US100_LOG_INFO:  return "info";
    case US100_LOG_WARN:  return "warn";
    case US100_LOG_ERROR: return "error";
    default:              return "info";
    }
}

static FILE *plugin_open_ini_file(void)
{
    wchar_t ini_path[1024];
    DWORD env_len = GetEnvironmentVariableW(L"US100_STUB_INI_PATH", ini_path, 1024);
    if (env_len > 0 && env_len < 1024) {
        return _wfopen(ini_path, L"r");
    }
    DWORD n = GetModuleFileNameW(NULL, ini_path, 1024);
    if (n == 0 || n >= 1000) {
        return NULL;
    }
    wchar_t *slash = wcsrchr(ini_path, L'\\');
    if (slash == NULL) {
        return NULL;
    }
    wcscpy(slash + 1, L"ngs_stub.ini");
    return _wfopen(ini_path, L"r");
}

static int plugin_read_ini_key_from_file(FILE *f, const char *section, const char *key,
                                         char *out_buf, uint32_t out_cap, int32_t *out_rc)
{
    if (f == NULL || key == NULL || key[0] == 0) {
        return 0;
    }
    size_t key_len = strlen(key);
    int want_section = (section != NULL && section[0] != 0);
    int in_section = !want_section;
    char line[512];
    while (fgets(line, sizeof(line), f) != NULL) {
        char *p = line;
        while (*p == ' ' || *p == '\t') p++;
        if (*p == '#' || *p == ';' || *p == '\r' || *p == '\n' || *p == 0) {
            continue;
        }
        if (*p == '[') {
            char *rb = strchr(p + 1, ']');
            if (rb != NULL) {
                *rb = 0;
                char *sec = p + 1;
                while (*sec == ' ' || *sec == '\t') sec++;
                size_t slen = strlen(sec);
                while (slen > 0 && (sec[slen - 1] == ' ' || sec[slen - 1] == '\t')) {
                    sec[--slen] = 0;
                }
                if (want_section) {
                    in_section = (_stricmp(sec, section) == 0);
                } else {
                    in_section = 0;
                }
            }
            continue;
        }
        if (!in_section) {
            continue;
        }
        if (_strnicmp(p, key, key_len) == 0 && p[key_len] == '=') {
            char *val = p + key_len + 1;
            while (*val == ' ' || *val == '\t') val++;
            size_t len = strlen(val);
            while (len > 0 && (val[len - 1] == '\r' || val[len - 1] == '\n' ||
                               val[len - 1] == ' ' || val[len - 1] == '\t')) {
                val[--len] = 0;
            }
            if (len + 1 > out_cap) {
                memcpy(out_buf, val, out_cap - 1);
                out_buf[out_cap - 1] = 0;
                *out_rc = US100_ERR_BUFFER_TOO_SMALL;
            } else {
                memcpy(out_buf, val, len + 1);
                *out_rc = US100_OK;
            }
            return 1;
        }
    }
    return 0;
}

static int plugin_is_safe_section_name(const char *sec)
{
    if (sec == NULL || sec[0] == 0) {
        return 0;
    }
    for (const char *p = sec; *p != 0; p++) {
        unsigned char c = (unsigned char)*p;
        if (!((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
              (c >= '0' && c <= '9') || c == '_' || c == '-')) {
            return 0;
        }
    }
    return 1;
}

static int32_t client_host_get_ini_string(const char *key, const char *default_val,
                                          char *out_buf, uint32_t out_cap)
{
    if (key == NULL || key[0] == 0 || out_buf == NULL || out_cap == 0) {
        return US100_ERR_INVALID_ARG;
    }
    const char *def = default_val != NULL ? default_val : "";
    int32_t rc = US100_OK;

    // Support scoped keys "plugin_name:key":
    //   1) <plugins_dir>/<plugin_name>.ini (global section or [plugin_name])
    //   2) ngs_stub.ini [plugin_name] section
    //   3) fallback to flat key in ngs_stub.ini
    const char *colon = strchr(key, ':');
    if (colon != NULL && colon > key && colon[1] != 0) {
        size_t slen = (size_t)(colon - key);
        if (slen < 64) {
            char section[64];
            memcpy(section, key, slen);
            section[slen] = 0;
            const char *subkey = colon + 1;
            if (plugin_is_safe_section_name(section) && g_plugins_dir_utf8[0] != 0) {
                wchar_t dir_w[1024];
                wchar_t sec_w[64];
                wchar_t path_w[1120];
                dir_w[0] = 0;
                sec_w[0] = 0;
                MultiByteToWideChar(CP_UTF8, 0, g_plugins_dir_utf8, -1, dir_w, 1024);
                MultiByteToWideChar(CP_UTF8, 0, section, -1, sec_w, 64);
                _snwprintf(path_w, 1120, L"%ls\\%ls.ini", dir_w, sec_w);
                path_w[1119] = 0;
                FILE *pf = _wfopen(path_w, L"r");
                if (pf != NULL) {
                    int found = plugin_read_ini_key_from_file(pf, NULL, subkey, out_buf, out_cap, &rc);
                    if (!found) {
                        rewind(pf);
                        found = plugin_read_ini_key_from_file(pf, section, subkey, out_buf, out_cap, &rc);
                    }
                    fclose(pf);
                    if (found) {
                        return rc;
                    }
                }
            }
            FILE *f = plugin_open_ini_file();
            if (f != NULL) {
                int found = plugin_read_ini_key_from_file(f, section, subkey, out_buf, out_cap, &rc);
                fclose(f);
                if (found) {
                    return rc;
                }
            }
        }
    }

    FILE *f = plugin_open_ini_file();
    if (f != NULL) {
        int found = plugin_read_ini_key_from_file(f, NULL, key, out_buf, out_cap, &rc);
        fclose(f);
        if (found) {
            return rc;
        }
    }
    size_t def_len = strlen(def);
    if (def_len + 1 > out_cap) {
        memcpy(out_buf, def, out_cap - 1);
        out_buf[out_cap - 1] = 0;
        return US100_ERR_BUFFER_TOO_SMALL;
    }
    memcpy(out_buf, def, def_len + 1);
    return US100_OK;
}

static int32_t client_host_get_ini_int(const char *key, int32_t default_val)
{
    char buf[64];
    if (client_host_get_ini_string(key, "", buf, sizeof(buf)) != US100_OK || buf[0] == 0) {
        return default_val;
    }
    char *end = NULL;
    long v = strtol(buf, &end, 0);
    if (end == buf) {
        return default_val;
    }
    return (int32_t)v;
}

static void client_host_log(int32_t level, const char *plugin_name, const char *message)
{
    ensure_init();
    if (g_log == NULL) {
        return;
    }
    const char *pname = (plugin_name != NULL && plugin_name[0] != 0) ? plugin_name : "client_plugin";
    const char *msg = message != NULL ? message : "";
    EnterCriticalSection(&g_cs);
    fprintf(g_log, "[plugin:%s][%s] %s\n", pname, plugin_level_name(level), msg);
    fflush(g_log);
    LeaveCriticalSection(&g_cs);
}

static int32_t client_host_patch_bytes(uint32_t rva, const uint8_t *expected,
                                       const uint8_t *replacement, uint32_t len)
{
    if (replacement == NULL || len == 0 || len > 4096) {
        return US100_ERR_INVALID_ARG;
    }
    HMODULE exe = GetModuleHandleA(NULL);
    if (exe == NULL) {
        return US100_ERR_NOT_READY;
    }
    if (!is_rva_in_exe_image(exe, rva, len)) {
        return US100_ERR_INVALID_ARG;
    }
    BYTE *p = (BYTE *)exe + rva;
    if (expected != NULL && memcmp(p, expected, len) != 0) {
        return US100_ERR_MISMATCH;
    }
    DWORD old = 0;
    if (!VirtualProtect(p, len, PAGE_EXECUTE_READWRITE, &old)) {
        return US100_ERR_FAILED;
    }
    memcpy(p, replacement, len);
    VirtualProtect(p, len, old, &old);
    FlushInstructionCache(GetCurrentProcess(), p, len);
    return US100_OK;
}

static int32_t client_host_read_memory(uint32_t rva, uint8_t *out_buf, uint32_t len)
{
    if (out_buf == NULL || len == 0 || len > 65536) {
        return US100_ERR_INVALID_ARG;
    }
    HMODULE exe = GetModuleHandleA(NULL);
    if (exe == NULL) {
        return US100_ERR_NOT_READY;
    }
    SIZE_T got = 0;
    if (!ReadProcessMemory(GetCurrentProcess(), (LPCVOID)((BYTE *)exe + rva), out_buf, len, &got) || got != len) {
        return US100_ERR_FAILED;
    }
    return US100_OK;
}

// Lightweight x64 prologue safety scanner: rejects common RIP-relative operands
// and relative branches/calls inside the copied prologue because pf_trampoline()
// copies raw prologue bytes without instruction relocation.
static int plugin_prologue_has_unsafe_rel(const uint8_t *code, uint32_t len)
{
    uint32_t i = 0;
    while (i < len) {
        int has_66 = 0;
        int rex_w = 0;
        while (i < len) {
            uint8_t b = code[i];
            if (b == 0x66) {
                has_66 = 1;
                i++;
            } else if (b == 0xF2 || b == 0xF3 || b == 0x2E || b == 0x3E || b == 0x64 || b == 0x65) {
                i++;
            } else if (b >= 0x40 && b <= 0x4F) {
                rex_w = (b & 0x08) != 0;
                i++;
                break;
            } else {
                break;
            }
        }
        if (i >= len) {
            break;
        }
        uint8_t op = code[i++];
        // Direct relative control-flow transfers cannot be relocated by raw memcpy.
        if (op == 0xE8 || op == 0xE9 || op == 0xEB || (op >= 0x70 && op <= 0x7F) ||
            op == 0xE0 || op == 0xE1 || op == 0xE2 || op == 0xE3) {
            return 1;
        }
        // Single-byte pushes/pops/nop/ret
        if ((op >= 0x50 && op <= 0x5F) || op == 0x90 || op == 0xC3 || op == 0xCC) {
            continue;
        }
        // push imm8 / imm32
        if (op == 0x6A) {
            i += 1;
            continue;
        }
        if (op == 0x68) {
            i += has_66 ? 2 : 4;
            continue;
        }
        // mov reg, imm32/imm64
        if (op >= 0xB8 && op <= 0xBF) {
            i += rex_w ? 8 : (has_66 ? 2 : 4);
            continue;
        }
        // Two-byte 0x0F opcodes
        int has_modrm = 0;
        uint32_t imm_bytes = 0;
        if (op == 0x0F) {
            if (i >= len) {
                break;
            }
            uint8_t op2 = code[i++];
            if (op2 >= 0x80 && op2 <= 0x8F) {
                return 1; // jcc rel32
            }
            if (op2 == 0x1F || op2 == 0x10 || op2 == 0x11 || op2 == 0x28 || op2 == 0x29 ||
                op2 == 0xB6 || op2 == 0xB7 || op2 == 0xBE || op2 == 0xBF ||
                (op2 >= 0x40 && op2 <= 0x4F)) {
                has_modrm = 1;
            } else {
                break;
            }
        } else if (op == 0x88 || op == 0x89 || op == 0x8A || op == 0x8B || op == 0x8D ||
                   op == 0x84 || op == 0x85 || op == 0x01 || op == 0x03 || op == 0x29 ||
                   op == 0x2B || op == 0x31 || op == 0x33 || op == 0x39 || op == 0x3B ||
                   op == 0x63 || op == 0xFF) {
            has_modrm = 1;
        } else if (op == 0x83 || op == 0xC1 || op == 0xC6) {
            has_modrm = 1;
            imm_bytes = 1;
        } else if (op == 0x81 || op == 0xC7) {
            has_modrm = 1;
            imm_bytes = has_66 ? 2 : 4;
        } else {
            break;
        }

        if (has_modrm) {
            if (i >= len) {
                break;
            }
            uint8_t modrm = code[i++];
            uint8_t mod = (modrm >> 6) & 3;
            uint8_t rm = modrm & 7;
            if (mod == 0 && rm == 5) {
                return 1; // [RIP + disp32]
            }
            if (mod != 3 && rm == 4) {
                if (i >= len) {
                    break;
                }
                uint8_t sib = code[i++];
                if (mod == 0 && (sib & 7) == 5) {
                    i += 4;
                }
            }
            if (mod == 1) {
                i += 1;
            } else if (mod == 2) {
                i += 4;
            }
            i += imm_bytes;
        }
    }
    return 0;
}

static int32_t client_host_install_trampoline(uint32_t rva, const uint8_t *expected,
                                              uint32_t prologue_len, void *hook_fn,
                                              void **out_trampoline)
{
    if (hook_fn == NULL || out_trampoline == NULL || prologue_len < 12 || prologue_len > 48) {
        return US100_ERR_INVALID_ARG;
    }
    HMODULE exe = GetModuleHandleA(NULL);
    if (exe == NULL) {
        return US100_ERR_NOT_READY;
    }
    if (!is_rva_in_exe_image(exe, rva, prologue_len)) {
        return US100_ERR_INVALID_ARG;
    }
    BYTE *p = (BYTE *)exe + rva;
    if (expected != NULL && memcmp(p, expected, prologue_len) != 0) {
        return US100_ERR_MISMATCH;
    }
    if (plugin_prologue_has_unsafe_rel(p, prologue_len)) {
        client_host_log(US100_LOG_ERROR, "pluginhost",
                        "install_trampoline rejected: prologue contains RIP-relative or relative branch instruction");
        return US100_ERR_UNSUPPORTED;
    }
    void *tramp = pf_trampoline(p, (int)prologue_len);
    if (tramp == NULL) {
        return US100_ERR_FAILED;
    }
    if (!pf_patch_jmp(p, hook_fn, (int)prologue_len)) {
        VirtualFree(tramp, 0, MEM_RELEASE);
        return US100_ERR_FAILED;
    }
    *out_trampoline = tramp;
    return US100_OK;
}

static int32_t client_host_register_wndproc_hook(us100_wndproc_cb cb, void *user_data)
{
    if (cb == NULL) {
        return US100_ERR_INVALID_ARG;
    }
    plugin_host_init_cs();
    EnterCriticalSection(&g_plugin_cs);
    for (int i = 0; i < US100_MAX_WNDPROC_HOOKS; i++) {
        if (g_wndproc_hooks[i].handle == 0) {
            int32_t h = (int32_t)InterlockedIncrement(&g_next_hook_handle);
            g_wndproc_hooks[i].handle = h;
            g_wndproc_hooks[i].cb = cb;
            g_wndproc_hooks[i].user_data = user_data;
            LeaveCriticalSection(&g_plugin_cs);
            return h;
        }
    }
    LeaveCriticalSection(&g_plugin_cs);
    return US100_ERR_LIMIT_REACHED;
}

static int32_t client_host_unregister_wndproc_hook(int32_t handle)
{
    if (handle <= 0) {
        return US100_ERR_INVALID_ARG;
    }
    plugin_host_init_cs();
    EnterCriticalSection(&g_plugin_cs);
    for (int i = 0; i < US100_MAX_WNDPROC_HOOKS; i++) {
        if (g_wndproc_hooks[i].handle == handle) {
            g_wndproc_hooks[i].handle = 0;
            g_wndproc_hooks[i].cb = NULL;
            g_wndproc_hooks[i].user_data = NULL;
            LeaveCriticalSection(&g_plugin_cs);
            return US100_OK;
        }
    }
    LeaveCriticalSection(&g_plugin_cs);
    return US100_ERR_NOT_FOUND;
}

static int32_t client_host_register_tick_hook(us100_tick_cb cb, void *user_data)
{
    if (cb == NULL) {
        return US100_ERR_INVALID_ARG;
    }
    plugin_host_init_cs();
    EnterCriticalSection(&g_plugin_cs);
    for (int i = 0; i < US100_MAX_TICK_HOOKS; i++) {
        if (g_tick_hooks[i].handle == 0) {
            int32_t h = (int32_t)InterlockedIncrement(&g_next_hook_handle);
            g_tick_hooks[i].handle = h;
            g_tick_hooks[i].cb = cb;
            g_tick_hooks[i].user_data = user_data;
            LeaveCriticalSection(&g_plugin_cs);
            return h;
        }
    }
    LeaveCriticalSection(&g_plugin_cs);
    return US100_ERR_LIMIT_REACHED;
}

static int32_t client_host_unregister_tick_hook(int32_t handle)
{
    if (handle <= 0) {
        return US100_ERR_INVALID_ARG;
    }
    plugin_host_init_cs();
    EnterCriticalSection(&g_plugin_cs);
    for (int i = 0; i < US100_MAX_TICK_HOOKS; i++) {
        if (g_tick_hooks[i].handle == handle) {
            g_tick_hooks[i].handle = 0;
            g_tick_hooks[i].cb = NULL;
            g_tick_hooks[i].user_data = NULL;
            LeaveCriticalSection(&g_plugin_cs);
            return US100_OK;
        }
    }
    LeaveCriticalSection(&g_plugin_cs);
    return US100_ERR_NOT_FOUND;
}

static int32_t client_host_register_stat_hook(us100_stat_int_cb int_cb,
                                              us100_stat_f2_cb f2_cb,
                                              us100_stat_f3_cb f3_cb,
                                              void *user_data)
{
    if (int_cb == NULL && f2_cb == NULL && f3_cb == NULL) {
        return US100_ERR_INVALID_ARG;
    }
    ensure_panel_getters_installed();
    plugin_host_init_cs();
    EnterCriticalSection(&g_plugin_cs);
    for (int i = 0; i < US100_MAX_STAT_HOOKS; i++) {
        if (g_stat_hooks[i].handle == 0) {
            int32_t h = (int32_t)InterlockedIncrement(&g_next_hook_handle);
            g_stat_hooks[i].handle = h;
            g_stat_hooks[i].int_cb = int_cb;
            g_stat_hooks[i].f2_cb = f2_cb;
            g_stat_hooks[i].f3_cb = f3_cb;
            g_stat_hooks[i].user_data = user_data;
            LeaveCriticalSection(&g_plugin_cs);
            return h;
        }
    }
    LeaveCriticalSection(&g_plugin_cs);
    return US100_ERR_LIMIT_REACHED;
}

static int32_t client_host_unregister_stat_hook(int32_t handle)
{
    if (handle <= 0) {
        return US100_ERR_INVALID_ARG;
    }
    plugin_host_init_cs();
    EnterCriticalSection(&g_plugin_cs);
    for (int i = 0; i < US100_MAX_STAT_HOOKS; i++) {
        if (g_stat_hooks[i].handle == handle) {
            g_stat_hooks[i].handle = 0;
            g_stat_hooks[i].int_cb = NULL;
            g_stat_hooks[i].f2_cb = NULL;
            g_stat_hooks[i].f3_cb = NULL;
            g_stat_hooks[i].user_data = NULL;
            LeaveCriticalSection(&g_plugin_cs);
            return US100_OK;
        }
    }
    LeaveCriticalSection(&g_plugin_cs);
    return US100_ERR_NOT_FOUND;
}

static int plugin_host_dispatch_wndproc(HWND hwnd, UINT msg, WPARAM w, LPARAM l, LRESULT *out_res)
{
    if (g_plugin_cs_ready == 0) {
        return 0;
    }
    us100_wndproc_slot snap[US100_MAX_WNDPROC_HOOKS];
    int count = 0;
    EnterCriticalSection(&g_plugin_cs);
    for (int i = 0; i < US100_MAX_WNDPROC_HOOKS; i++) {
        if (g_wndproc_hooks[i].handle != 0 && g_wndproc_hooks[i].cb != NULL) {
            snap[count++] = g_wndproc_hooks[i];
        }
    }
    LeaveCriticalSection(&g_plugin_cs);
    for (int i = 0; i < count; i++) {
        intptr_t res = 0;
        if (snap[i].cb((void *)hwnd, (uint32_t)msg, (uintptr_t)w, (intptr_t)l, &res, snap[i].user_data) != 0) {
            if (out_res != NULL) {
                *out_res = (LRESULT)res;
            }
            return 1;
        }
    }
    return 0;
}

static void plugin_host_dispatch_tick(void)
{
    if (g_plugin_cs_ready == 0) {
        return;
    }
    us100_tick_slot snap[US100_MAX_TICK_HOOKS];
    int count = 0;
    EnterCriticalSection(&g_plugin_cs);
    for (int i = 0; i < US100_MAX_TICK_HOOKS; i++) {
        if (g_tick_hooks[i].handle != 0 && g_tick_hooks[i].cb != NULL) {
            snap[count++] = g_tick_hooks[i];
        }
    }
    LeaveCriticalSection(&g_plugin_cs);
    if (count == 0) {
        return;
    }
    uint64_t now_ms = (uint64_t)GetTickCount64();
    uint64_t seq = (uint64_t)InterlockedIncrement64(&g_client_tick_seq);
    for (int i = 0; i < count; i++) {
        snap[i].cb(now_ms, seq, snap[i].user_data);
    }
}

static int plugin_host_dispatch_stat_int(void *src, int idx, int *out_val)
{
    if (g_plugin_cs_ready == 0) {
        return 0;
    }
    us100_stat_slot snap[US100_MAX_STAT_HOOKS];
    int count = 0;
    EnterCriticalSection(&g_plugin_cs);
    for (int i = 0; i < US100_MAX_STAT_HOOKS; i++) {
        if (g_stat_hooks[i].handle != 0 && g_stat_hooks[i].int_cb != NULL) {
            snap[count++] = g_stat_hooks[i];
        }
    }
    LeaveCriticalSection(&g_plugin_cs);
    for (int i = 0; i < count; i++) {
        int32_t v = 0;
        if (snap[i].int_cb(src, (int32_t)idx, &v, snap[i].user_data) != 0) {
            *out_val = (int)v;
            return 1;
        }
    }
    return 0;
}

static int plugin_host_dispatch_stat_f2(void *src, int idx, void *r8, float *out_val)
{
    if (g_plugin_cs_ready == 0) {
        return 0;
    }
    us100_stat_slot snap[US100_MAX_STAT_HOOKS];
    int count = 0;
    EnterCriticalSection(&g_plugin_cs);
    for (int i = 0; i < US100_MAX_STAT_HOOKS; i++) {
        if (g_stat_hooks[i].handle != 0 && g_stat_hooks[i].f2_cb != NULL) {
            snap[count++] = g_stat_hooks[i];
        }
    }
    LeaveCriticalSection(&g_plugin_cs);
    for (int i = 0; i < count; i++) {
        float v = 0.0f;
        if (snap[i].f2_cb(src, (int32_t)idx, r8, &v, snap[i].user_data) != 0) {
            *out_val = v;
            return 1;
        }
    }
    return 0;
}

static int plugin_host_dispatch_stat_f3(void *src, int idx, unsigned a, unsigned b, float c, int d, float *out_val)
{
    if (g_plugin_cs_ready == 0) {
        return 0;
    }
    us100_stat_slot snap[US100_MAX_STAT_HOOKS];
    int count = 0;
    EnterCriticalSection(&g_plugin_cs);
    for (int i = 0; i < US100_MAX_STAT_HOOKS; i++) {
        if (g_stat_hooks[i].handle != 0 && g_stat_hooks[i].f3_cb != NULL) {
            snap[count++] = g_stat_hooks[i];
        }
    }
    LeaveCriticalSection(&g_plugin_cs);
    for (int i = 0; i < count; i++) {
        float v = 0.0f;
        if (snap[i].f3_cb(src, (int32_t)idx, (uint32_t)a, (uint32_t)b, c, (int32_t)d, &v, snap[i].user_data) != 0) {
            *out_val = v;
            return 1;
        }
    }
    return 0;
}

static void plugin_host_resolve_dirs(wchar_t *out_plugins_w, size_t out_cap)
{
    wchar_t exe_w[1024];
    DWORD n = GetModuleFileNameW(NULL, exe_w, 1024);
    if (n > 0 && n < 1024) {
        wchar_t *slash = wcsrchr(exe_w, L'\\');
        if (slash != NULL) {
            *slash = 0;
        }
    } else {
        exe_w[0] = 0;
    }
    WideCharToMultiByte(CP_UTF8, 0, exe_w, -1, g_game_dir_utf8, sizeof(g_game_dir_utf8), NULL, NULL);

    wchar_t env_dir[1024];
    DWORD env_len = GetEnvironmentVariableW(L"US100_CLIENT_PLUGINS_DIR", env_dir, 1024);
    if (env_len > 0 && env_len < 1024) {
        wcsncpy(out_plugins_w, env_dir, out_cap - 1);
        out_plugins_w[out_cap - 1] = 0;
    } else {
        wchar_t cfg_w[512];
        cfg_w[0] = 0;
        MultiByteToWideChar(CP_UTF8, 0, g_plugins_dir_cfg, -1, cfg_w, 512);
        if (cfg_w[0] == 0) {
            wcscpy(cfg_w, L"plugins");
        }
        if ((cfg_w[0] != 0 && cfg_w[1] == L':') || (cfg_w[0] == L'\\' && cfg_w[1] == L'\\')) {
            wcsncpy(out_plugins_w, cfg_w, out_cap - 1);
            out_plugins_w[out_cap - 1] = 0;
        } else {
            _snwprintf(out_plugins_w, out_cap, L"%ls\\%ls", exe_w, cfg_w);
            out_plugins_w[out_cap - 1] = 0;
        }
    }
    WideCharToMultiByte(CP_UTF8, 0, out_plugins_w, -1, g_plugins_dir_utf8, sizeof(g_plugins_dir_utf8), NULL, NULL);
}

static int cmp_wide_filename(const void *a, const void *b)
{
    return _wcsicmp((const wchar_t *)a, (const wchar_t *)b);
}

// Called safely outside DllMain (on first NGS export call).
static void plugin_host_ensure_loaded(void)
{
    if (InterlockedCompareExchange(&g_plugins_loaded, 1, 0) != 0) {
        return;
    }
    ensure_init();
    plugin_host_init_cs();
    if (!g_plugins_enabled) {
        client_host_log(US100_LOG_INFO, "pluginhost", "client plugins disabled (plugins_enabled=0)");
        return;
    }

    wchar_t plugins_dir_w[1024];
    plugin_host_resolve_dirs(plugins_dir_w, 1024);

    memset(&g_client_host_api, 0, sizeof(g_client_host_api));
    g_client_host_api.struct_size             = (uint32_t)sizeof(US100ClientHostAPI);
    g_client_host_api.abi_version             = US100_PLUGIN_ABI_VERSION;
    g_client_host_api.game_base               = (void *)GetModuleHandleA(NULL);
    g_client_host_api.stub_module             = (void *)g_stub_module;
    g_client_host_api.game_pid                = (uint32_t)GetCurrentProcessId();
    g_client_host_api.game_dir                = g_game_dir_utf8;
    g_client_host_api.plugins_dir             = g_plugins_dir_utf8;
    g_client_host_api.log                     = client_host_log;
    g_client_host_api.patch_bytes             = client_host_patch_bytes;
    g_client_host_api.read_memory             = client_host_read_memory;
    g_client_host_api.install_trampoline      = client_host_install_trampoline;
    g_client_host_api.register_wndproc_hook   = client_host_register_wndproc_hook;
    g_client_host_api.unregister_wndproc_hook = client_host_unregister_wndproc_hook;
    g_client_host_api.register_tick_hook      = client_host_register_tick_hook;
    g_client_host_api.unregister_tick_hook    = client_host_unregister_tick_hook;
    g_client_host_api.register_stat_hook      = client_host_register_stat_hook;
    g_client_host_api.unregister_stat_hook    = client_host_unregister_stat_hook;
    g_client_host_api.get_ini_int             = client_host_get_ini_int;
    g_client_host_api.get_ini_string          = client_host_get_ini_string;

    DWORD attrs = GetFileAttributesW(plugins_dir_w);
    if (attrs == INVALID_FILE_ATTRIBUTES || (attrs & FILE_ATTRIBUTE_DIRECTORY) == 0) {
        return;
    }

    wchar_t pattern[1050];
    _snwprintf(pattern, 1050, L"%ls\\*.dll", plugins_dir_w);
    pattern[1049] = 0;

    WIN32_FIND_DATAW fd;
    HANDLE hfind = FindFirstFileW(pattern, &fd);
    if (hfind == INVALID_HANDLE_VALUE) {
        return;
    }

    wchar_t files[US100_MAX_CLIENT_PLUGINS][MAX_PATH];
    int file_count = 0;
    do {
        if ((fd.dwFileAttributes & FILE_ATTRIBUTE_DIRECTORY) != 0) {
            continue;
        }
        if (fd.cFileName[0] == L'_') {
            continue;
        }
        if (file_count < US100_MAX_CLIENT_PLUGINS) {
            wcsncpy(files[file_count], fd.cFileName, MAX_PATH - 1);
            files[file_count][MAX_PATH - 1] = 0;
            file_count++;
        }
    } while (FindNextFileW(hfind, &fd));
    FindClose(hfind);

    if (file_count > 1) {
        qsort(files, (size_t)file_count, sizeof(files[0]), cmp_wide_filename);
    }

    for (int i = 0; i < file_count; i++) {
        wchar_t full_path[1280];
        _snwprintf(full_path, 1280, L"%ls\\%ls", plugins_dir_w, files[i]);
        full_path[1279] = 0;

        char file_utf8[MAX_PATH];
        WideCharToMultiByte(CP_UTF8, 0, files[i], -1, file_utf8, sizeof(file_utf8), NULL, NULL);

        HMODULE mod = LoadLibraryW(full_path);
        if (mod == NULL) {
            char msg[512];
            _snprintf(msg, sizeof(msg), "LoadLibraryW failed for %s (err=%lu)", file_utf8, GetLastError());
            msg[sizeof(msg) - 1] = 0;
            client_host_log(US100_LOG_ERROR, "pluginhost", msg);
            continue;
        }
        us100_client_plugin_init_fn init_fn =
            (us100_client_plugin_init_fn)GetProcAddress(mod, "us100_client_plugin_init");
        if (init_fn == NULL) {
            char msg[512];
            _snprintf(msg, sizeof(msg), "skipping %s: missing export us100_client_plugin_init", file_utf8);
            msg[sizeof(msg) - 1] = 0;
            client_host_log(US100_LOG_WARN, "pluginhost", msg);
            FreeLibrary(mod);
            continue;
        }
        US100ClientPluginInfo info;
        memset(&info, 0, sizeof(info));
        info.struct_size = (uint32_t)sizeof(US100ClientPluginInfo);
        info.abi_version = US100_PLUGIN_ABI_VERSION;
        int32_t rc = init_fn(&g_client_host_api, &info);
        if (rc != US100_OK) {
            char msg[512];
            _snprintf(msg, sizeof(msg), "plugin %s init returned %d", file_utf8, rc);
            msg[sizeof(msg) - 1] = 0;
            client_host_log(US100_LOG_ERROR, "pluginhost", msg);
            FreeLibrary(mod);
            continue;
        }
        us100_loaded_client_plugin *slot = &g_client_plugins[g_client_plugin_count++];
        memset(slot, 0, sizeof(*slot));
        slot->module = mod;
        slot->fn_shutdown =
            (us100_client_plugin_shutdown_fn)GetProcAddress(mod, "us100_client_plugin_shutdown");
        strncpy(slot->file, file_utf8, sizeof(slot->file) - 1);
        if (info.name[0] != 0) {
            strncpy(slot->name, info.name, sizeof(slot->name) - 1);
        } else {
            strncpy(slot->name, file_utf8, sizeof(slot->name) - 1);
        }
        strncpy(slot->version, info.version, sizeof(slot->version) - 1);

        char msg[512];
        _snprintf(msg, sizeof(msg), "loaded client plugin %s v%s (%s)",
                  slot->name, slot->version[0] ? slot->version : "0.0", slot->file);
        msg[sizeof(msg) - 1] = 0;
        client_host_log(US100_LOG_INFO, "pluginhost", msg);
    }
}

static void plugin_host_shutdown(void)
{
    if (InterlockedCompareExchange(&g_plugins_shutdown, 1, 0) != 0) {
        return;
    }
    for (int i = g_client_plugin_count - 1; i >= 0; i--) {
        if (g_client_plugins[i].fn_shutdown != NULL) {
            g_client_plugins[i].fn_shutdown();
        }
    }
    g_client_plugin_count = 0;
}
