#ifndef US100_PLUGIN_COMMON_H
#define US100_PLUGIN_COMMON_H

#include <stdint.h>
#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

#define US100_PLUGIN_ABI_VERSION 1u

#if defined(_WIN32) || defined(_WIN64)
#define US100_PLUGIN_EXPORT __declspec(dllexport)
#else
#define US100_PLUGIN_EXPORT __attribute__((visibility("default")))
#endif

/* Unified return / error codes */
enum US100PluginStatus {
    US100_OK                   = 0,
    US100_ERR_INVALID_ARG      = -1,
    US100_ERR_NOT_FOUND        = -2,
    US100_ERR_BUFFER_TOO_SMALL = -3,
    US100_ERR_NOT_READY        = -4,
    US100_ERR_FAILED           = -5,
    US100_ERR_UNSUPPORTED      = -6,
    US100_ERR_LIMIT_REACHED    = -7,
    US100_ERR_MISMATCH         = -8
};

/* Log severity levels */
enum US100LogLevel {
    US100_LOG_DEBUG = 0,
    US100_LOG_INFO  = 1,
    US100_LOG_WARN  = 2,
    US100_LOG_ERROR = 3
};

/* Packet interception verdict returned by server packet hooks */
enum US100PacketAction {
    US100_ACTION_CONTINUE = 0, /* Pass through to next plugin / native handler */
    US100_ACTION_HANDLED  = 1, /* Plugin handled the packet; skip native handler */
    US100_ACTION_BLOCK    = 2  /* Drop/suppress packet without native handling or send */
};

#ifdef __cplusplus
}
#endif

#endif /* US100_PLUGIN_COMMON_H */
