# 美服 100 级服务端（DFO100US）搭建与开发项目

将美服 DFO 客户端 `D:\DNF\client\100US\DFO`（`v2.24.0.124`，2023-02-27，Lv100 内容）接入本地/局域网自研 Go 服务端 `wireprobe-us100` 的完整工程，包含 Go 主服务端、PySide6 桌面登录器与 GM 管理终端（`100US登录器.exe` / `tools/atelier/`）、离线客户端补丁（`ngs_stub`）、PVF 合并与中文本地化工具链及一键启动脚本。

> 开发与协议文档索引：[架构说明](server-us100/docs/architecture.md) · [重建记录](server-us100/docs/REBUILD-100US.md) · [PVF 本地化说明](server-us100/docs/US100-PVF-LOCALIZATION.md) · [客户端分析](server-us100/docs/CLIENT-US100-ANALYSIS.md) · [登录器使用与维护说明](tools/atelier/使用说明.md) · [DNF100 客户端结构](DNF100客户端结构.md)

---

## 当前开发进度（2026-11-27 晚更新）

### 1. 基础接入、网络、账号认证与存储
- [x] **客户端离线补丁与选频道优化（`ngs_stub`）**：6 个序号导出桩（`1=0, 5=1`）、`en-US` locale 补丁、`MessageBox` 抑制、NGS 初始化跳过（`0x4EC40B7 je→jmp`）及 `WndProc` 窗口保护（RVA `0x38B7350`）；已消除选频道黑屏重试循环，默认 `skip_server_select=0`（流畅进入选大区 → 选频道，零黑屏），启动 `DFO.exe` 前自动应用暂存的频道 UI `Script.pvf` 补丁，并修复 `selectchannelver2/3.xui` 红叉与 `ChannelBase` 错位。
- [x] **局域网与多地址绑定**：已移除仅回环（loopback-only）网络限制，支持 `0.0.0.0` / 局域网绑定与按客户端连接地址动态通告频道 IP。
- [x] **账号认证与单文件存储（`db100`）**：采用项目内单文件数据库 `server-us100/runtime/us100.db`（无外部数据库守护进程）；登录器主页内置账号注册、PBKDF2 密码校验、建库门禁（创建账号密码后生成数据库）、旧开发存档迁移（`migrate_account_data`）以及「删除数据库」一键重置功能（防忘记密码，自动停服并清理残留待发队列）。
- [x] **一键启动与预检**：`100US登录器.exe` / `一键启动开发环境.cmd` / `启动服务端.cmd` / `启动客户端.cmd` / `停止服务端.cmd` + `tools/us100_preflight.py` 预检 + 内置 `tools/go` 自动增量编译；启动失败时直接在登录器弹窗显示网关预检日志摘要。

### 2. 选角、城镇、背包、装备、任务与副本
- [x] **选角与城镇出生**：角色选择列表（`NOTI 2 mode 2`）完整编码已穿戴装扮与武器外观；支持角色改名历史（`CMD 8 mode 5`）；每次进城自动出生在对应区域的赛丽亚旅馆（Seria's Room）；城镇 HUD 红球/蓝球 100% 恢复，基础按键与快捷栏持久化同步。
- [x] **背包整理、锁定与分解**：
  - 普通背包与装扮背包一键整理（`CMD 20 / ACK 20`，静默 `NOTI 13` 刷新不刷获得提示）
  - 背包整理锁（`Ctrl+Click`，`CMD 705/706`）与物品安全锁（`Alt+Click`，`CMD 267-270`），通过 `NOTI 13/251` 持久化
  - 装备分解机完整修复（`disjoint_flow`），产出的无色及各色小晶块正确进入晶块仓库
- [x] **装备、称号与外观实时同步**：装备与称号穿戴、替换、卸下实时同步角色外观与城镇头顶称号（`equipment_flow`）；支持登录器在线发物、在线发时装与称号正常穿戴。
- [x] **礼盒、开罐与消耗品**：接入完整 **41,394 个** US100 PVF 礼盒目录（覆盖全部时装/装备/礼包/自选盒），支持 DANJI & Neo 时装自选盒、Space-1 装扮栏直发、`ACK 2052` 开罐布局、契约直接激活及活动道具使用（`CMD 507`）。
- [x] **任务、技能与副本循环**：任务链（接取/目标推进/发奖）、技能按转职最大等级门控与多档 SP 消耗、多层/多候选房间解析与 US100 副本地图刷新、怪物掉落、深渊掉落与翻牌结算。

### 3. 活动中心、商城、契约与中文本地化
- [x] **服务器活动系统与活动中心**：新增 `internal/events` 与登录器「活动中心」页面，支持经验/掉率/金币/翻牌倍率活动、深渊派对、每日/每周挑战、积分（Mileage）兑换与契约增益实时开关。
- [x] **游戏内活动日历、HUD 与互动领奖**：同步服务端时钟，通过 `NOTI 108/2279` 填充活动日历与 HUD 活动图标，修复 `EventDesignWindow` 详情渲染并隐藏异常背景贴图，支持游戏内互动活动奖励领取与 DANJI 开罐活动（`CMD 2052`）。
- [x] **点券商城与契约**：加载完整 US100 点券商城目录（含堆叠消耗品与装扮，`premium_catalog` / `premium_cart`）；同步 Neo Premium 契约状态（`NOTI 2 mode 0`）并支持租赁武器穿戴（`neo_claim`）。
- [x] **PVF 合并与活动 UI 中文化管线**：修复 `pvfmerge` 无 BOM UTF-16LE 双重编码问题并严格保留 UTF-16LE NUL 终止符，补齐空白 PVF 活动字符串；活动 UI NPK 帧严格原地替换以消除红叉；HUD 活动悬浮提示、活动日历与详情面板已全部中文化。

### 4. 待完善事项
- [ ] 更多高阶副本/团本房间协议与怪物细节补全（参考 [next39-dungeon-packets.md](server-us100/docs/protocol/next39-dungeon-packets.md)）
- [ ] 更多运营活动玩法与时装试衣间/全量目录体验持续打磨
- [ ] 替换 `configs/us100/rules.*.json` 中仍沿用 90 级参考数值的部分门禁参数
- [ ] 可选 WFP 按 `APP_ID` 网络隔离开关（仅针对需要禁止公网回连的单机场景）

---

## 关键技术规格

### 客户端（`D:\DNF\client\100US\DFO`）
- 美服 DFO `v2.24.0.124`，`DFO.exe` 约 171 MB（Themida 壳，`.text` 未加密），与韩服 100 级同协议代（DNFPB protobuf 58/30）
- `Script.pvf` 约 390 MB，RSA + AES 双层包装
- NGS：`NGClient64.aes`（6 个序号导出）+ `BlackCipher` 目录
- 启动参数格式：`13?<host>?<port>?probe?000...?0?0?30?0?0?0`

### 服务端（`server-us100`，Go 1.26）
- 主监听（频道握手 + 游戏）：默认本地/可配置 `0.0.0.0` 绑定随机游戏端口，建连先发 `channelinfo`
- 频道目录刷新：`7001` 端口（独立 socket，11 字节头 + AES-ECB / zlib）
- 会话密钥：`channelinfo` protobuf `field 3`（1024 字节固定模式）
- 线协议：客户端 13 字节头 `[0]=type [1:3]=id [3:7]=len [7]=crc8 [11:13]=marker` + 加密 body；服务端 16 字节头（校验和位于 `[11]` 单字节）

---

## 目录结构

| 目录 / 文件 | 说明 |
| --- | --- |
| `server-us100/` | Go 主服务端（`cmd/wireprobe` 核心网关、`cmd/*` 导入与审计工具、`internal/*` 领域/协议/活动/存储层、`configs/` 配置、`docs/` 协议文档、`scripts/atelierlibrary` 目录生成器） |
| `tools/atelier/` | PySide6 桌面登录器源码（账号认证/建库删库、物品可视化发放、全局掉落、时装试衣间、Mod 工坊、角色属性、活动中心） |
| `ngs_stub/` | US100 客户端离线补丁源码（`build.bat` 编译并部署至客户端目录） |
| `tools/` | 启动/预检/停止脚本（`us100_interactive.py`、`us100_preflight.py`、`us100_stop.py`、`us100_client.py`）、频道/活动 UI 补丁与 PVF 本地化脚本 |
| `runtime/` | 运行期日志、会话状态（`interactive/ready.json`、`run.json`）、本地 PVF 解包与内存镜像（已由 `.gitignore` 排除，不入库） |
| `100US登录器.exe` | 桌面登录器入口（启动 `tools/atelier/bootstrap.pyw`） |
| `一键启动开发环境.cmd` / `启动服务端.cmd` | 命令行一键执行预检、按需重编并拉起服务端与客户端 |
| `启动客户端.cmd` / `停止服务端.cmd` | 单独拉起客户端接入运行中服务端 / 清理并停止会话 |

---

## 快速运行与测试

- **推荐方式（桌面登录器）**：双击 `100US登录器.exe` → 在主页输入账号与密码点击「创建账号」（自动生成 `us100.db`）或「登录账号」→ 点击「立即进入 100 级世界」。忘记账号密码时可直接点击主页「删除数据库」重置。
- **命令行一键启动**：双击 `一键启动开发环境.cmd` 或 `启动服务端.cmd`；仅启动服务端可运行：
  ```bash
  python tools/us100_interactive.py 13 normal --no-client
  ```
- **启动前预检**：
  ```bash
  python tools/us100_preflight.py
  ```
  默认客户端目录为 `D:\DNF\client\100US\DFO`，可通过环境变量 `DFO_CLIENT_DIR` 覆盖。
- **核心自动化测试**：
  ```bash
  # 服务端协议与核心流程测试
  cd server-us100 && ..\tools\go\bin\go.exe test ./cmd/wireprobe ./internal/game/protocol ./internal/admin

  # 登录器账号认证、删库与界面测试
  python -m unittest tools/atelier/test_account_auth.py tools/atelier/test_epic.py -v
  ```

---

## Git 分支与版本管理

仓库地址：`https://github.com/susule222/DFO100US`

| 分支 / 标签 | 用途 |
| --- | --- |
| `main` | 稳定主干分支（对外展示与阶段稳定版本） |
| `DFO100US` | 当前活跃开发主分支（日常服务端、登录器、活动与本地化迭代） |
| `dev` | 初始基线保留分支 |
| `v0.1.0-us100-baseline` | 初始稳定基线回滚标签（提交 `70d9423`） |

### 常用合并与回滚命令
```bash
# 1. 日常在 DFO100US 分支提交并推送
git checkout DFO100US
git status -sb
git add <改动文件>
git commit -m "feat/fix: 变更说明"
git push origin DFO100US

# 2. 阶段验收通过后合并到 main 并打新回滚标签
git checkout main
git merge --no-ff DFO100US
git tag -a v0.2.0-xxx -m "阶段里程碑说明"
git push origin main --tags

# 3. 安全撤销单次提交（保留历史）或从基线标签新建回滚分支
git revert <commit>
git checkout -b rollback/baseline v0.1.0-us100-baseline
```

---

## 内存注意事项

- PVF 读取采用 `mmap`（避免 390 MB 堆复制），解压块 LRU 上限 64 MB、文本 LRU 上限 16 MB、文件表惰性解析；批量导入工具（如 `equipmentbuild`）使用 `GOGC=10`，导入峰值由 ~2490 MB 降至 ~902 MB。
- `pvfinspect` 默认不写 `matches.json`（需显式加 `-index` 并受 `-max-matches` 限制），避免产生数百 MB 中间文件。
