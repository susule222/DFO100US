/*
 * client_hello_plugin.c - 100US Client External DLL Plugin Example (ABI v1)
 *
 * Loaded automatically by NGClient64.aes from `<DFO_DIR>\plugins\`.
 * Demonstrates:
 *   - Reading host metadata (game_base, game_pid, game_dir, plugins_dir)
 *   - Reading ngs_stub.ini keys via get_ini_int / get_ini_string
 *   - Registering shared WndProc and frame-tick hooks
 *   - Safely reading process memory via read_memory
 */
#include <windows.h>
#include <stdio.h>
#include <string.h>
#include "../us100_client_plugin.h"

static const US100ClientHostAPI *g_host = NULL;
static int32_t g_wndproc_handle = 0;
static int32_t g_tick_handle = 0;
static volatile LONG g_tick_logged = 0;

static int32_t on_game_wndproc(
    void *hwnd, uint32_t msg, uintptr_t wparam, intptr_t lparam,
    intptr_t *out_result, void *user_data)
{
    (void)hwnd;
    (void)lparam;
    (void)out_result;
    (void)user_data;
    if (msg == WM_KEYUP && wparam == VK_F11 && g_host != NULL && g_host->log != NULL) {
        g_host->log(US100_LOG_INFO, "client_hello_plugin", "F11 hotkey observed in game WndProc");
    }
    return 0; /* Pass through to game */
}

static void on_game_tick(uint64_t tick_ms, uint64_t tick_seq, void *user_data)
{
    (void)user_data;
    if (InterlockedCompareExchange(&g_tick_logged, 1, 0) == 0 && g_host != NULL && g_host->log != NULL) {
        char msg[128];
        _snprintf(msg, sizeof(msg), "first frame tick observed: seq=%llu ms=%llu",
                  (unsigned long long)tick_seq, (unsigned long long)tick_ms);
        msg[sizeof(msg) - 1] = 0;
        g_host->log(US100_LOG_INFO, "client_hello_plugin", msg);
    }
}

US100_PLUGIN_EXPORT int32_t us100_client_plugin_init(
    const US100ClientHostAPI *host, US100ClientPluginInfo *out_info)
{
    if (host == NULL || out_info == NULL) {
        return US100_ERR_INVALID_ARG;
    }
    if (host->abi_version != US100_PLUGIN_ABI_VERSION ||
        host->struct_size < sizeof(US100ClientHostAPI)) {
        return US100_ERR_UNSUPPORTED;
    }
    g_host = host;

    strncpy(out_info->name, "client_hello_plugin", sizeof(out_info->name) - 1);
    strncpy(out_info->version, "1.0.0", sizeof(out_info->version) - 1);
    strncpy(out_info->author, "100US SDK", sizeof(out_info->author) - 1);
    strncpy(out_info->description,
            "Reference external client DLL plugin for NGClient64.aes.",
            sizeof(out_info->description) - 1);

    uint8_t mz[2] = { 0, 0 };
    int32_t mz_rc = host->read_memory(0, mz, 2);
    int32_t fast_ch = host->get_ini_int("fast_channel_select", -1);
    char greeting[64] = "";
    host->get_ini_string("hello_greeting", "default", greeting, sizeof(greeting));
    char scoped_greeting[64] = "";
    host->get_ini_string("client_hello_plugin:scoped_greeting", "none", scoped_greeting, sizeof(scoped_greeting));

    g_wndproc_handle = host->register_wndproc_hook(on_game_wndproc, NULL);
    g_tick_handle = host->register_tick_hook(on_game_tick, NULL);

    if (host->log != NULL) {
        char msg[320];
        _snprintf(msg, sizeof(msg),
                  "initialized pid=%u base=%p mz=%02X%02X(rc=%d) fast_ch=%d greeting=%s scoped=%s wnd_h=%d tick_h=%d",
                  host->game_pid, host->game_base, mz[0], mz[1], mz_rc, fast_ch, greeting, scoped_greeting,
                  g_wndproc_handle, g_tick_handle);
        msg[sizeof(msg) - 1] = 0;
        host->log(US100_LOG_INFO, "client_hello_plugin", msg);
    }
    return US100_OK;
}

US100_PLUGIN_EXPORT void us100_client_plugin_shutdown(void)
{
    if (g_host != NULL) {
        if (g_tick_handle > 0 && g_host->unregister_tick_hook != NULL) {
            g_host->unregister_tick_hook(g_tick_handle);
            g_tick_handle = 0;
        }
        if (g_wndproc_handle > 0 && g_host->unregister_wndproc_hook != NULL) {
            g_host->unregister_wndproc_hook(g_wndproc_handle);
            g_wndproc_handle = 0;
        }
        if (g_host->log != NULL) {
            g_host->log(US100_LOG_INFO, "client_hello_plugin", "shutdown complete");
        }
        g_host = NULL;
    }
}
