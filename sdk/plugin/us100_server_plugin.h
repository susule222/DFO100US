#ifndef US100_SERVER_PLUGIN_H
#define US100_SERVER_PLUGIN_H

#include "us100_plugin_common.h"

#ifdef __cplusplus
extern "C" {
#endif

/*
 * 100US Server External DLL Plugin ABI (v1, Windows x64)
 *
 * Place compiled 64-bit .dll files in `<project>/plugins/server/` (or the path
 * configured via `wireprobe-us100.exe -plugins-dir <dir>`).
 * Files whose names start with '_' or do not end with '.dll' are skipped.
 *
 * Required export:
 *   US100_PLUGIN_EXPORT int32_t us100_server_plugin_init(
 *       const US100ServerHostAPI *host, US100ServerPluginInfo *out_info);
 *
 * Optional exports:
 *   US100_PLUGIN_EXPORT void us100_server_plugin_shutdown(void);
 *   US100_PLUGIN_EXPORT void us100_server_plugin_on_session_open(const US100SessionInfo *session);
 *   US100_PLUGIN_EXPORT void us100_server_plugin_on_session_close(const US100SessionInfo *session);
 *   US100_PLUGIN_EXPORT void us100_server_plugin_on_character_enter(const US100SessionInfo *session);
 *   US100_PLUGIN_EXPORT int32_t us100_server_plugin_on_client_packet(
 *       const US100SessionInfo *session, const US100PacketView *pkt);
 *   US100_PLUGIN_EXPORT int32_t us100_server_plugin_on_server_packet(
 *       const US100SessionInfo *session, const US100PacketView *pkt);
 *   US100_PLUGIN_EXPORT void us100_server_plugin_on_tick(uint64_t now_unix_ms);
 */

typedef struct US100ServerPluginInfo {
    uint32_t struct_size;
    uint32_t abi_version;
    char     name[64];
    char     version[32];
    char     author[64];
    char     description[256];
} US100ServerPluginInfo;

typedef struct US100SessionInfo {
    uint32_t struct_size;
    uint32_t server_id;
    uint64_t session_id;
    int64_t  account_id;
    int64_t  character_id;
    uint32_t channel_id;
    uint16_t wire_id;
    uint16_t town_id;
    uint16_t area_id;
    uint16_t pos_x;
    uint16_t pos_y;
    uint8_t  level;
    uint8_t  bootstrapped;
    uint8_t  in_dungeon;
    uint8_t  _pad[7];
    char     peer_addr[64];
    char     char_name[64];
    char     channel_name[64];
} US100SessionInfo;

typedef struct US100PacketView {
    uint32_t       struct_size;
    uint16_t       packet_id;
    uint8_t        kind;        /* 0 = NOTI, 1 = CMD/ACK */
    uint8_t        verified;    /* 1 = checksum verified (client->server) or 1 (server->client) */
    const uint8_t *payload;     /* Plaintext payload bytes; valid during callback only */
    uint32_t       payload_len;
    uint32_t       raw_len;
} US100PacketView;

typedef struct US100ServerHostAPI {
    uint32_t    struct_size;
    uint32_t    abi_version;
    const char *plugins_dir;    /* UTF-8 absolute path to server plugins directory */
    const char *config_dir;     /* UTF-8 path to server output/runtime directory */

    /* Write a log entry into wireprobe log and events.jsonl */
    void (*log)(int32_t level, const char *plugin_name, const char *message);

    /* Encrypt, frame, and send a server packet to a live session (thread-safe).
     * kind: 0=NOTI, 1=CMD/ACK. */
    int32_t (*send_packet)(uint64_t session_id, uint8_t kind, uint16_t packet_id,
                           const uint8_t *payload, uint32_t payload_len);

    /* Broadcast a server packet to all connected sessions. */
    int32_t (*broadcast_packet)(uint8_t kind, uint16_t packet_id,
                                const uint8_t *payload, uint32_t payload_len);

    /* Query live session metadata by session_id. */
    int32_t (*get_session_info)(uint64_t session_id, US100SessionInfo *out_info);

    /* Enumerate active session IDs. Writes up to max_count IDs and sets *out_count
     * to the total number of active sessions. */
    int32_t (*list_sessions)(uint64_t *out_ids, uint32_t max_count, uint32_t *out_count);

    /* Grant a single item (stackable/equipment/avatar) via admin.Service and
     * trigger live inventory sync if the character is online. */
    int32_t (*grant_item)(int64_t account_id, int64_t character_id,
                          uint32_t template_id, uint32_t amount, const char *reason);

    /* Execute any admin.CharacterOp JSON payload (currency, level, advancement,
     * bag, quest, contract, avatar, etc.) and write receipt JSON into out_buf.
     * If out_buf is NULL or too small, returns US100_ERR_BUFFER_TOO_SMALL and
     * sets *out_len to required byte length (excluding NUL). */
    int32_t (*exec_admin_op_json)(const char *op_json, char *out_buf,
                                  uint32_t out_cap, uint32_t *out_len);

    /* Read the full character JSON snapshot from db100 storage. */
    int32_t (*get_character_json)(int64_t account_id, int64_t character_id,
                                  char *out_buf, uint32_t out_cap, uint32_t *out_len);

    /* Persistent plugin KV storage backed by server db100 (table "plugin_<ns>"). */
    int32_t (*kv_get)(const char *ns, const char *key,
                      char *out_buf, uint32_t out_cap, uint32_t *out_len);
    int32_t (*kv_put)(const char *ns, const char *key, const char *value_json);
    int32_t (*kv_delete)(const char *ns, const char *key);

    /* Wake online session(s) playing character_id to push bag/funds/exp/events sync. */
    int32_t (*notify_character_sync)(int64_t character_id);
} US100ServerHostAPI;

/* Function pointer typedefs for plugin exports */
typedef int32_t (*us100_server_plugin_init_fn)(
    const US100ServerHostAPI *host, US100ServerPluginInfo *out_info);
typedef void (*us100_server_plugin_shutdown_fn)(void);
typedef void (*us100_server_plugin_session_fn)(const US100SessionInfo *session);
typedef int32_t (*us100_server_plugin_packet_fn)(
    const US100SessionInfo *session, const US100PacketView *pkt);
typedef void (*us100_server_plugin_tick_fn)(uint64_t now_unix_ms);

#ifdef __cplusplus
}
#endif

#endif /* US100_SERVER_PLUGIN_H */
