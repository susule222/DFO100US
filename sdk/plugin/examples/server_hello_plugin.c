/*
 * server_hello_plugin.c - 100US Server External DLL Plugin Example (ABI v1)
 *
 * Demonstrates:
 *   - Plugin metadata registration & lifecycle hooks
 *   - Intercepting custom/native client packets (e.g. CMD 9001 echo + KV + session query)
 *   - Blocking/inspecting outbound server packets (e.g. suppressing test ID 9002)
 *   - Calling back into the Go wireprobe host (send_packet, broadcast_packet,
 *     get_session_info, list_sessions, grant_item, exec_admin_op_json,
 *     get_character_json, kv_put/kv_get/kv_delete, notify_character_sync)
 */
#include <windows.h>
#include <stdio.h>
#include <string.h>
#include "../us100_server_plugin.h"

static const US100ServerHostAPI *g_host = NULL;
static volatile LONG g_tick_count = 0;

US100_PLUGIN_EXPORT int32_t us100_server_plugin_init(
    const US100ServerHostAPI *host, US100ServerPluginInfo *out_info)
{
    if (host == NULL || out_info == NULL) {
        return US100_ERR_INVALID_ARG;
    }
    if (host->abi_version != US100_PLUGIN_ABI_VERSION ||
        host->struct_size < sizeof(US100ServerHostAPI)) {
        return US100_ERR_UNSUPPORTED;
    }
    g_host = host;

    strncpy(out_info->name, "server_hello_plugin", sizeof(out_info->name) - 1);
    strncpy(out_info->version, "1.0.0", sizeof(out_info->version) - 1);
    strncpy(out_info->author, "100US SDK", sizeof(out_info->author) - 1);
    strncpy(out_info->description,
            "Reference external server DLL plugin demonstrating packet hooks, KV store, and GM ops.",
            sizeof(out_info->description) - 1);

    if (g_host->log != NULL) {
        g_host->log(US100_LOG_INFO, "server_hello_plugin", "initialized successfully");
    }
    return US100_OK;
}

US100_PLUGIN_EXPORT void us100_server_plugin_shutdown(void)
{
    if (g_host != NULL && g_host->log != NULL) {
        g_host->log(US100_LOG_INFO, "server_hello_plugin", "shutting down");
    }
    g_host = NULL;
}

US100_PLUGIN_EXPORT void us100_server_plugin_on_session_open(const US100SessionInfo *session)
{
    if (g_host == NULL || session == NULL || g_host->log == NULL) {
        return;
    }
    char msg[256];
    _snprintf(msg, sizeof(msg), "session_open id=%llu peer=%s channel=%u(%s)",
              (unsigned long long)session->session_id, session->peer_addr,
              session->channel_id, session->channel_name);
    msg[sizeof(msg) - 1] = 0;
    g_host->log(US100_LOG_INFO, "server_hello_plugin", msg);
}

US100_PLUGIN_EXPORT void us100_server_plugin_on_session_close(const US100SessionInfo *session)
{
    if (g_host == NULL || session == NULL || g_host->log == NULL) {
        return;
    }
    char msg[256];
    _snprintf(msg, sizeof(msg), "session_close id=%llu char=%lld",
              (unsigned long long)session->session_id, (long long)session->character_id);
    msg[sizeof(msg) - 1] = 0;
    g_host->log(US100_LOG_INFO, "server_hello_plugin", msg);
}

US100_PLUGIN_EXPORT void us100_server_plugin_on_character_enter(const US100SessionInfo *session)
{
    if (g_host == NULL || session == NULL) {
        return;
    }
    if (g_host->kv_put != NULL) {
        char val[128];
        _snprintf(val, sizeof(val), "{\"char_id\":%lld,\"level\":%u,\"town\":%u}",
                  (long long)session->character_id, (unsigned)session->level, (unsigned)session->town_id);
        val[sizeof(val) - 1] = 0;
        g_host->kv_put("hello", "last_enter", val);
    }
    if (g_host->log != NULL) {
        char msg[256];
        _snprintf(msg, sizeof(msg), "character_enter name=%s char_id=%lld lv=%u",
                  session->char_name, (long long)session->character_id, (unsigned)session->level);
        msg[sizeof(msg) - 1] = 0;
        g_host->log(US100_LOG_INFO, "server_hello_plugin", msg);
    }
}

US100_PLUGIN_EXPORT int32_t us100_server_plugin_on_client_packet(
    const US100SessionInfo *session, const US100PacketView *pkt)
{
    if (g_host == NULL || session == NULL || pkt == NULL) {
        return US100_ACTION_CONTINUE;
    }

    /* Example custom protocol hook: CMD 9001 -> echo payload back + store in KV + query session */
    if (pkt->kind == 1 && pkt->packet_id == 9001) {
        US100SessionInfo live;
        memset(&live, 0, sizeof(live));
        if (g_host->get_session_info != NULL) {
            g_host->get_session_info(session->session_id, &live);
        }
        if (g_host->kv_put != NULL) {
            char val[128];
            _snprintf(val, sizeof(val), "{\"sid\":%llu,\"len\":%u}",
                      (unsigned long long)live.session_id, pkt->payload_len);
            val[sizeof(val) - 1] = 0;
            g_host->kv_put("hello", "last_cmd_9001", val);
        }
        if (g_host->send_packet != NULL) {
            g_host->send_packet(session->session_id, 1, 9001, pkt->payload, pkt->payload_len);
        }
        return US100_ACTION_HANDLED;
    }

    /* Example custom protocol hook: CMD 9003 -> execute admin op JSON & reply with receipt JSON */
    if (pkt->kind == 1 && pkt->packet_id == 9003 && pkt->payload != NULL && pkt->payload_len > 0) {
        char op_buf[1024];
        uint32_t n = pkt->payload_len < (sizeof(op_buf) - 1) ? pkt->payload_len : (uint32_t)(sizeof(op_buf) - 1);
        memcpy(op_buf, pkt->payload, n);
        op_buf[n] = 0;

        char receipt[2048];
        uint32_t out_len = 0;
        int32_t rc = g_host->exec_admin_op_json(op_buf, receipt, sizeof(receipt), &out_len);
        if (rc == US100_OK && g_host->send_packet != NULL) {
            g_host->send_packet(session->session_id, 1, 9003, (const uint8_t *)receipt, out_len);
        }
        return US100_ACTION_HANDLED;
    }

    /* Example custom protocol hook: CMD 9004 -> grant item & notify sync */
    if (pkt->kind == 1 && pkt->packet_id == 9004 && pkt->payload != NULL && pkt->payload_len >= 8) {
        uint32_t tpl_id = 0;
        uint32_t amount = 0;
        memcpy(&tpl_id, pkt->payload, 4);
        memcpy(&amount, pkt->payload + 4, 4);
        if (g_host->grant_item != NULL) {
            int32_t rc = g_host->grant_item(session->account_id, session->character_id, tpl_id, amount, "plugin_9004");
            uint8_t ack[1] = { rc == US100_OK ? 1 : 0 };
            if (g_host->send_packet != NULL) {
                g_host->send_packet(session->session_id, 1, 9004, ack, 1);
            }
        }
        return US100_ACTION_HANDLED;
    }

    return US100_ACTION_CONTINUE;
}

US100_PLUGIN_EXPORT int32_t us100_server_plugin_on_server_packet(
    const US100SessionInfo *session, const US100PacketView *pkt)
{
    (void)session;
    if (pkt != NULL && pkt->kind == 0 && pkt->packet_id == 9002) {
        /* Suppress synthetic test packet 9002 */
        return US100_ACTION_BLOCK;
    }
    return US100_ACTION_CONTINUE;
}

US100_PLUGIN_EXPORT void us100_server_plugin_on_tick(uint64_t now_unix_ms)
{
    LONG c = InterlockedIncrement(&g_tick_count);
    if (c == 1 && g_host != NULL && g_host->kv_put != NULL) {
        char val[64];
        _snprintf(val, sizeof(val), "{\"first_tick\":%llu}", (unsigned long long)now_unix_ms);
        val[sizeof(val) - 1] = 0;
        g_host->kv_put("hello", "tick_seen", val);
    }
}
