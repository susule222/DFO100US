# 美服 100 级服务端（DFO100US）搭建与开发项目

将美服 DFO 客户端 `D:\DNF\client\100US\DFO`（`v2.24.0.124`，2023-02-27，Lv100 内容）接入本地自研 Go 服务端 `wireprobe-us100` 的完整工程，包含服务端源码、离线客户端补丁（`ngs_stub`）、PVF 导入/本地化工具链及一键启动脚本。

> 架构与协议资料索引：[架构说明](server-us100/docs/architecture.md) · [重建记录](server-us100/docs/REBUILD-100US.md) · [PVF 本地化说明](server-us100/docs/US100-PVF-LOCALIZATION.md) · [客户端分析](server-us100/docs/CLIENT-US100-ANALYSIS.md) · [DNF100 客户端结构](DNF100客户端结构.md)

---

## 当前开发进度（2026-11-27）

### 1. 基础接入、存储与启动链
- [x] **客户端离线补丁（`ngs_stub`）**：6 个序号导出桩（`1=0, 5=1`）、`en-US` locale 补丁、`MessageBox` 抑制、NGS 初始化检查绕过（`0x4EC40B7 je→jmp`）及 `WndProc` 窗口关闭保护（RVA `0x38B7350`）
- [x] **登录与频道选择**：主监听 `127.0.0.2:<随机端口>` + 频道目录刷新 `127.0.0.1:7001`，完成 `channelinfo`、挑战 `318`、预检/登录 `1554`、角色列表（v2.24 95 字节名单行）、创角/删角与改名历史（`CMD 8 mode 5`）
- [x] **单文件内置存储（`db100`）**：采用项目内 `server-us100/runtime/us100.db`（首次由 `us100_seed.json` 初始化），不再依赖外部数据库守护进程
- [x] **一键启动与预检**：`一键启动开发环境.cmd` / `启动服务端.cmd` / `启动客户端.cmd` / `停止服务端.cmd` + `tools/us100_preflight.py` 预检 + 自动增量编译（内置 `tools/go`）

### 2. 城镇、角色、任务与副本核心循环
- [x] **进城与属性恢复**：正常模式发送带角色属性的 `USERINFO mode 1`，城镇 HUD 红球/蓝球 100% 恢复；快捷栏与基础按键同步（`quickslot_flow` / `basic_keys`）
- [x] **城镇移动与世界/教学流程**：区域移动、地图门禁、城镇 NPC、教学引导与剧情副本（`world_flow` / `tutorial_flow` / `mirkwood_story` / `lorien`）
- [x] **任务系统**：主线/支线任务接取、进度推进、完成与奖励发放（`quest_flow`）
- [x] **副本与结算**：多层/多候选房间解析与 US100 副本地图刷新、进图战斗流程、怪物掉落、通关翻牌与结算（`dungeon_flow` / `loot_flow` / `card_flow` / `settlement_flow`）
- [x] **技能系统**：按转职最大等级门控学习，支持多档 SP 消耗（`skill_flow`）

### 3. 装备、分解机、外观、商城与活动
- [x] **装备与外观同步**：装备/称号穿戴、替换与卸下，城镇内实时同步头顶称号与角色外观（`equipment_flow`）；支持登录器/在线发放称号正常穿戴
- [x] **分解机与晶块仓库**：装备分解生效，分解产出的无色及各色小晶块正确入库至晶块仓库（`disjoint_flow`）
- [x] **消耗品与在线管理**：消耗品使用、在线 GM/物品注入监听与菜单交互（`consume_flow` / `online_gm` / `inject_watch` / `menu_flow`）
- [x] **点券商城与契约**：加载完整 US100 点券商城目录（含堆叠物品与装扮，`premium_catalog` / `premium_cart`）；同步 Neo Premium 契约状态（`NOTI 2 mode 0`）并支持租借武器穿戴（`neo_claim`）
- [x] **活动系统（推进中）**：已激活 DANJI 罐子活动（`NOTI 108/2279`）并实现开罐协议（`CMD 2052`，`danji_flow`）；通用活动框架（`internal/events` / `storage/activities.go`）持续扩展中

### 4. 待完善事项
- [ ] 更多活动模块与长期运营玩法（`internal/events` 持续接入）
- [ ] 副本怪物细节与更多高阶副本/团本房间协议补全（参考 [next39-dungeon-packets.md](server-us100/docs/protocol/next39-dungeon-packets.md)）
- [ ] WFP 网络隔离（按 `APP_ID` 阻断客户端非回环外连）
- [ ] 替换 `configs/us100/rules.*.json` 中仍沿用 90 级参考数值的部分门禁参数

---

## 关键技术规格

### 客户端（`D:\DNF\client\100US\DFO`）
- 美服 DFO `v2.24.0.124`，`DFO.exe` 约 171 MB（Themida 壳，`.text` 未加密），与韩服 100 级同协议代（DNFPB protobuf 58/30）
- `Script.pvf` 约 390 MB，RSA + AES 双层包装
- NGS：`NGClient64.aes`（6 个序号导出）+ `BlackCipher` 目录
- 启动参数格式：`13?<host>?<port>?probe?000...?0?0?30?0?0?0`

### 服务端（`server-us100`，Go 1.26）
- 主监听（频道握手 + 游戏）：`127.0.0.2:<随机端口>`，建连先发 `channelinfo`
- 频道目录刷新：`127.0.0.1:7001`（独立 socket，11 字节头 + AES-ECB / zlib）
- 会话密钥：`channelinfo` protobuf `field 3`（1024 字节固定模式）
- 线协议：客户端 13 字节头 `[0]=type [1:3]=id [3:7]=len [7]=crc8 [11:13]=marker` + 加密 body；服务端 16 字节头（校验和位于 `[11]` 单字节）

---

## 目录结构

| 目录 / 文件 | 说明 |
| --- | --- |
| `server-us100/` | Go 主服务端（`cmd/wireprobe` 核心服务、`cmd/*` 导入与审计工具、`internal/*` 领域与协议层、`configs/` 配置、`docs/` 协议文档） |
| `ngs_stub/` | US100 客户端离线补丁源码（`build.bat` 编译并部署至客户端目录） |
| `tools/` | 启动/预检/停止脚本（`us100_interactive.py`、`us100_preflight.py`、`us100_stop.py`、`us100_client.py`）、PVF 解密与本地化工具 |
| `runtime/` | 运行期日志、会话状态（`interactive/ready.json`、`run.json`）、本地 PVF 解包与内存镜像（已由 `.gitignore` 排除，不入库） |
| `一键启动开发环境.cmd` / `启动服务端.cmd` | 一键执行预检、按需重编并拉起服务端与客户端 |
| `启动客户端.cmd` / `停止服务端.cmd` | 单独拉起客户端接入运行中服务端 / 清理并停止会话 |
| `100US登录器.exe` | 本地配套登录器与发物工具 |

---

## 快速运行

- **一键启动（服务端 + 客户端）**：双击 `一键启动开发环境.cmd` 或 `启动服务端.cmd`。启动器会自动执行预检、在源码更新时重编 `wireprobe-us100.exe`、打开 `server-us100/runtime/us100.db` 并拉起客户端。
- **仅启动服务端**：在项目根目录运行：
  ```bash
  python tools/us100_interactive.py 13 normal --no-client
  ```
  看到 `server ready` 后，再双击 `启动客户端.cmd` 或使用 `100US登录器.exe` 连接（当前端口记录在 `runtime/interactive/ready.json`）。
- **仅执行启动前预检**：
  ```bash
  python tools/us100_preflight.py
  ```
  默认客户端路径为 `D:\DNF\client\100US\DFO`，可通过环境变量 `DFO_CLIENT_DIR` 覆盖。
- **停止服务**：前台按 `Ctrl+C` 或运行 `停止服务端.cmd`。

---

## 分支与版本管理

- `main`：稳定基线分支（初始回滚标签：`v0.1.0-us100-baseline`）
- `dev` / `DFO100US`：服务端日常功能迭代分支，验收稳定后合入 `main` 并打新标签便于随时回滚

## 内存注意事项

- PVF 读取采用 `mmap`（避免 390 MB 堆复制），解压块 LRU 上限 64 MB、文本 LRU 上限 16 MB、文件表惰性解析；批量导入工具（如 `equipmentbuild`）使用 `GOGC=10`，导入峰值由 ~2490 MB 降至 ~902 MB。
- `pvfinspect` 默认不写 `matches.json`（需显式加 `-index` 并受 `-max-matches` 限制），避免产生数百 MB 中间文件。
