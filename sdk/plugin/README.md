# 100US 外部 DLL 插件开发指南（ABI v1）

100US 提供**双端统一 C ABI（Windows x64）**外部 `.dll` 插件接口，可用 C、C++、Rust、Zig、Delphi 或 Go (`-buildmode=c-shared`) 编写插件，无需修改或重编主服务端与客户端桩。

---

## 一、目录与部署路径

| 端 | 宿主模块 | 插件放置目录 | 禁用单个插件 |
| --- | --- | --- | --- |
| **服务端** | `wireprobe-us100.exe` (`internal/pluginhost`) | `plugins/server/*.dll`（可通过 `-plugins-dir` 覆盖） | 文件名前加 `_` 或改扩展名 |
| **客户端** | `NGClient64.aes` (`ngs_stub`) | `<DFO_DIR>\plugins\*.dll`（可通过 `ngs_stub.ini` 的 `plugins_dir=` 与 `plugins_enabled=0/1` 控制） | 文件名前加 `_` 或改扩展名 |

> 头文件位于 `sdk/plugin/`：
> - `us100_plugin_common.h` — ABI 版本、错误码、日志等级与包拦截动作枚举
> - `us100_server_plugin.h` — 服务端插件 C ABI 规范
> - `us100_client_plugin.h` — 客户端插件 C ABI 规范

---

## 二、服务端 DLL 插件接口（`us100_server_plugin.h`）

### 导出函数

| 符号 | 必需 | 说明 |
| --- | --- | --- |
| `us100_server_plugin_init(host, out_info)` | **必需** | 初始化插件并填充 `US100ServerPluginInfo`，返回 `US100_OK (0)` |
| `us100_server_plugin_shutdown()` | 可选 | 服务端退出时逆序调用 |
| `us100_server_plugin_on_session_open(session)` | 可选 | 客户端 TCP 建连时触发 |
| `us100_server_plugin_on_session_close(session)` | 可选 | 客户端断连时触发 |
| `us100_server_plugin_on_character_enter(session)` | 可选 | 角色进入城镇/游戏世界后触发 |
| `us100_server_plugin_on_client_packet(session, pkt)` | 可选 | 收到客户端解密包时触发；返回 `US100_ACTION_HANDLED(1)` 或 `US100_ACTION_BLOCK(2)` 跳过服务端原生处理 |
| `us100_server_plugin_on_server_packet(session, pkt)` | 可选 | 服务端下发业务包前触发；返回 `US100_ACTION_BLOCK(2)` 抑制发送 |
| `us100_server_plugin_on_tick(now_unix_ms)` | 可选 | 每秒触发一次后台心跳 |

### 宿主 API（`US100ServerHostAPI`）

- `log(level, plugin_name, message)`：写入服务端日志与 `events.jsonl`
- `send_packet(session_id, kind, packet_id, payload, payload_len)`：向指定会话加密封帧发包（线程安全，`kind=0` NOTI / `kind=1` CMD/ACK）
- `broadcast_packet(kind, packet_id, payload, payload_len)`：向全部在线会话广播
- `get_session_info(session_id, out_info)` / `list_sessions(out_ids, max_count, out_count)`：查询在线会话
- `grant_item(account_id, character_id, template_id, amount, reason)`：事务性发放道具并自动触发在线背包刷新
- `exec_admin_op_json(op_json, out_buf, out_cap, out_len)`：通用执行 `admin.CharacterOp` JSON（点券/积分/金币/等级/转职/契约/时装/任务/背包操作），自动同步在线玩家
- `get_character_json(account_id, character_id, out_buf, out_cap, out_len)`：读取角色实时 JSON 快照
- `kv_get / kv_put / kv_delete`：基于服务端单文件 `db100` 的隔离 KV 持久存储（表名固定 `plugin_<ns>`）
- `notify_character_sync(character_id)`：主动唤醒在线连接执行背包/资金/经验/活动全量同步

---

## 三、客户端 DLL 插件接口（`us100_client_plugin.h`）

`NGClient64.aes` 严格保留仅导出序号 `1..6`，并在**离开 `DllMain` 后的首次 NGS 导出调用**安全加载外部插件（彻底规避 Windows 加载器锁死锁）。

### 导出函数

| 符号 | 必需 | 说明 |
| --- | --- | --- |
| `us100_client_plugin_init(host, out_info)` | **必需** | 初始化插件，返回 `US100_OK (0)` |
| `us100_client_plugin_shutdown()` | 可选 | 进程退出前逆序调用 |

### 宿主 API（`US100ClientHostAPI`）

- 只读元数据：`game_base`（`DFO.exe` 基址）、`stub_module`、`game_pid`、`game_dir`、`plugins_dir`
- `log(level, plugin_name, message)`：线程安全写入 `ngs_stub.log` / `US100_LOG_PATH`
- `patch_bytes(rva, expected, replacement, len)`：校验原字节后安全写入 `DFO.exe` 内存补丁
- `read_memory(rva, out_buf, len)`：基于 `ReadProcessMemory` 安全读内存
- `install_trampoline(rva, expected, prologue_len, hook_fn, out_trampoline)`：安装 x64 序言复制 + RIP 间接跳回 trampoline 钩子（自动拒绝包含 RIP 相对寻址或相对跳转指令的序言并返回 `US100_ERR_UNSUPPORTED`）
- `register_wndproc_hook / unregister_wndproc_hook`：共享挂接游戏主窗口 `WndProc`（多插件共存，不冲突启动 30s 防关保护；游戏主窗口 `WM_NCDESTROY` 时会在加载器锁外提前优雅调用 `us100_client_plugin_shutdown`）
- `register_tick_hook / unregister_tick_hook`：挂接 NGS 帧轮询（`ord6`）
- `register_stat_hook / unregister_stat_hook`：按需安装属性 getter trampoline 并拦截面板属性显示值
- `get_ini_int / get_ini_string`：读取配置项，支持作用域键名 `"plugin_name:key"`（依次查找 `<plugins_dir>\<plugin_name>.ini` → `ngs_stub.ini` 的 `[plugin_name]` 节）及 `ngs_stub.ini` 顶层键

> **登录器 Mod 工坊规范**：可将外接 DLL 与 `PVF/`、`NPK/` 打包到 90JP Mod ZIP 中的 `DLL/client/*.dll`、`DLL/server/*.dll`（及可选 `.ini` 配置）或直接导入单文件 `.dll`。登录器会自动校验 x64 PE 与 ABI v1 导出符号，并按 Mod 优先级添加 `001_`、`002_` 前缀部署与回滚。

---

## 四、快速编译示例插件

```cmd
cd sdk\plugin\examples
build_examples.bat
```

生成产物：
- `sdk\plugin\examples\build\server_hello_plugin.dll` → 复制到 `plugins\server\` 生效
- `sdk\plugin\examples\build\client_hello_plugin.dll` → 复制到 `<DFO_DIR>\plugins\` 生效
