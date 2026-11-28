#ifndef US100_CLIENT_PLUGIN_H
#define US100_CLIENT_PLUGIN_H

#include "us100_plugin_common.h"

#ifdef __cplusplus
extern "C" {
#endif

/*
 * 100US Client External DLL Plugin ABI (v1, Windows x64)
 *
 * Loaded by NGClient64.aes (ngs_stub) safely outside DllMain on first NGS entry.
 * Place compiled 64-bit .dll files in `<DFO_DIR>\plugins\` (configurable via
 * `plugins_dir=` and `plugins_enabled=1` in `ngs_stub.ini`).
 * Files whose names start with '_' or do not end with '.dll' are skipped.
 *
 * Required export:
 *   US100_PLUGIN_EXPORT int32_t us100_client_plugin_init(
 *       const US100ClientHostAPI *host, US100ClientPluginInfo *out_info);
 *
 * Optional export:
 *   US100_PLUGIN_EXPORT void us100_client_plugin_shutdown(void);
 *   Note: shutdown is called on main window WM_NCDESTROY (outside loader lock)
 *   and falls back to DllMain(DLL_PROCESS_DETACH) for headless hosts. Keep
 *   shutdown non-blocking (do not wait on worker threads or call LoadLibrary).
 */

typedef struct US100ClientPluginInfo {
    uint32_t struct_size;
    uint32_t abi_version;
    char     name[64];
    char     version[32];
    char     author[64];
    char     description[256];
} US100ClientPluginInfo;

/* WndProc hook callback.
 * Return non-zero to mark message handled and return *out_result to Windows.
 * Return 0 to pass through to subsequent hooks and the original game WndProc. */
typedef int32_t (*us100_wndproc_cb)(
    void *hwnd, uint32_t msg, uintptr_t wparam, intptr_t lparam,
    intptr_t *out_result, void *user_data);

/* Periodic tick callback invoked from NGS frame poll (ord6). */
typedef void (*us100_tick_cb)(uint64_t tick_ms, uint64_t tick_seq, void *user_data);

/* Stat getter override callbacks.
 * Return non-zero to override the stat value in *out_val; return 0 to pass through. */
typedef int32_t (*us100_stat_int_cb)(
    void *src, int32_t idx, int32_t *out_val, void *user_data);
typedef int32_t (*us100_stat_f2_cb)(
    void *src, int32_t idx, void *r8, float *out_val, void *user_data);
typedef int32_t (*us100_stat_f3_cb)(
    void *src, int32_t idx, uint32_t a, uint32_t b, float c, int32_t d,
    float *out_val, void *user_data);

typedef struct US100ClientHostAPI {
    uint32_t    struct_size;
    uint32_t    abi_version;
    void       *game_base;      /* HMODULE of DFO.exe */
    void       *stub_module;    /* HMODULE of NGClient64.aes */
    uint32_t    game_pid;
    uint32_t    _reserved;
    const char *game_dir;       /* UTF-8 directory containing DFO.exe */
    const char *plugins_dir;    /* UTF-8 directory from which plugins were loaded */

    /* Thread-safe bounded log into ngs_stub.log / US100_LOG_PATH */
    void (*log)(int32_t level, const char *plugin_name, const char *message);

    /* Verify expected bytes (if expected != NULL) and patch DFO.exe + rva */
    int32_t (*patch_bytes)(uint32_t rva, const uint8_t *expected,
                           const uint8_t *replacement, uint32_t len);

    /* Read len bytes from DFO.exe + rva into out_buf */
    int32_t (*read_memory)(uint32_t rva, uint8_t *out_buf, uint32_t len);

    /* Install a safe x64 trampoline detour at DFO.exe + rva (prologue_len >= 12).
     * Verifies expected bytes (if non-NULL), rejects RIP-relative / relative
     * branch instructions in the copied prologue (US100_ERR_UNSUPPORTED),
     * copies prologue into executable trampoline, writes RIP-indirect jump back,
     * and returns trampoline via *out_trampoline. */
    int32_t (*install_trampoline)(uint32_t rva, const uint8_t *expected,
                                  uint32_t prologue_len, void *hook_fn,
                                  void **out_trampoline);

    /* Register / unregister a shared game WndProc hook (returns handle >= 1 on success) */
    int32_t (*register_wndproc_hook)(us100_wndproc_cb cb, void *user_data);
    int32_t (*unregister_wndproc_hook)(int32_t handle);

    /* Register / unregister a game frame tick hook (returns handle >= 1 on success) */
    int32_t (*register_tick_hook)(us100_tick_cb cb, void *user_data);
    int32_t (*unregister_tick_hook)(int32_t handle);

    /* Register / unregister stat getter override hooks (auto-installs getter
     * trampolines on first registration; returns handle >= 1 on success) */
    int32_t (*register_stat_hook)(us100_stat_int_cb int_cb,
                                  us100_stat_f2_cb f2_cb,
                                  us100_stat_f3_cb f3_cb,
                                  void *user_data);
    int32_t (*unregister_stat_hook)(int32_t handle);

    /* Read integer or string key from INI.
     * Supports scoped keys "plugin_name:key" (checks <plugins_dir>/<plugin_name>.ini,
     * then [plugin_name] section in ngs_stub.ini) as well as top-level ngs_stub.ini keys. */
    int32_t (*get_ini_int)(const char *key, int32_t default_val);
    int32_t (*get_ini_string)(const char *key, const char *default_val,
                              char *out_buf, uint32_t out_cap);
} US100ClientHostAPI;

typedef int32_t (*us100_client_plugin_init_fn)(
    const US100ClientHostAPI *host, US100ClientPluginInfo *out_info);
typedef void (*us100_client_plugin_shutdown_fn)(void);

#ifdef __cplusplus
}
#endif

#endif /* US100_CLIENT_PLUGIN_H */
