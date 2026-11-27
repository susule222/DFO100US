# 美服100服务端 (US100) 搭建项目

国服 `D:\DNF\client\DNF100`（DNF.exe 37.8.13.0）的模拟端位于 [server-cn100](server-cn100/README.md)，使用该目录内的启动脚本。安装目录和资源指纹见 [DNF100 客户端结构](DNF100客户端结构.md)。

## 目标
把美服 DFO 客户端 `D:\DNF\client\100US\DFO`（v2.24.0.124, 2023-02-27, Lv100 内容）接入本地服务端，
参考已跑通的 115us 项目（E:\BaiduNetdiskDownload\115us，v2.38.2.34 客户端 + wireprobe Go 服务端）。

## 关键事实

### 客户端 (D:\DNF\client\100US\DFO)
- 美服 DFO v2.24.0.124，DFO.exe 171MB（Themida 壳，.text 未加密）
- 与韩服100 客户端同协议代（DNFPB protobuf 58/30 一致）
- Script.pvf 390MB，RSA+AES 双层包装（与 115us 的 760MB PVF 同格式）
- NGS：NGClient64.aes（6 个序号导出）+ BlackCipher 目录
- 启动参数格式：`13?<host>?<port>?probe?000...?0?0?30?0?0?0`（与 v2.38 相同！）

### 服务端 (wireprobe-us100, Go, 骨架来自 115us 参考项目)
- 主监听（频道握手+游戏）：127.0.0.2:随机端口，先发 channelinfo fixture
- 频道目录刷新：127.0.0.1:7001（独立 socket，11 字节头 + AES-ECB/zlib）
- 存储采用项目内的单文件数据库 `server-us100\runtime\us100.db`。首次初始化使用同目录下的 `us100_seed.json`。
- 会话密钥：channelinfo protobuf field3（1024 字节固定模式），服务端同样使用
- 线协议：13 字节客户端头 `[0]=type [1:3]=id [3:7]=len [7]=crc8 [11:13]=marker`
  + 加密 body；服务端 16 字节头（校验和是 **[11] 一个字节**）

### 已跑通（US100 客户端 ↔ 本地服务端）
1. 客户端接受 `13?127.0.0.2?<port>?probe?...` 参数并连接
2. 服务端发 channelinfo → 客户端回 318（挑战）→ 1554（precheck，"probe"）
3. 服务端回 1554/1（login）fixture → 客户端校验通过（会话密钥兼容！）
4. 客户端发 8（GET_USERINFO）→ 服务端回 2（v2.24 名单行，95 固定字节 + 名称）
5. 客户端继续发 848/433/637（roster 计数器/佣兵/删除挂起数）
6. 客户端到角色选择界面，持续发 465（NGS 进程上报，服务端采样忽略）
7. 客户端发 2（含本机 IP，服务端只记录不处理）
8. 选角 → 进城 → 走到副本门口（CMD15）→ 选择界面 → 选本 → 房间数据（进行中，
   见 docs/protocol/next39-dungeon-packets.md 末尾一节）

### 客户端离线补丁（ngs_stub，见 ngs_stub\ngs_stub.c）
- NGS 6 序号导出桩（ini 可配返回值：1=0, 5=1 必须）
- en-US locale 补丁（GetACP=1252 等）
- MessageBox 抑制 + 日志
- **NGS 初始化检查补丁**：0x4EC40B7 `je→jmp`（跳过 init 失败退出）
- **窗口保护**：hook 游戏 WndProc（RVA 0x38B7350），吞掉 WM_CLOSE/WM_DESTROY
  （否则 NGS 失败后窗口被销毁 → D3D CreateDevice 拿无效 hwnd → "Failed to set
  up the graphics system" → 客户端退出）

## 目录
- ngs_stub\    US100 客户端离线补丁（构建：build.bat → 部署到 D:\DNF\client\100US\DFO）
- tools\       启动/预检/停止 + 侦察与测试脚本（Go 工具链在 tools\go，模块缓存在 tools\gopath）
  - us100_interactive.py   一键启动（预检 → 检查 db100 → 服务端 → 客户端）
  - us100_preflight.py     启动前检查（客户端体积、二进制新旧、端口、存储、实例）
  - us100_stop.py          停止服务端和客户端
  - unwrap_us100_pvf.py    US100 Script.pvf 外层解密（RSA+AES，密钥取自 DFO.exe）
  - 其余为侦察期历史脚本（多数仍是只读分析工具）
- runtime\     运行输出与 US100 数据（interactive\ 本次会话、shots\、cursor\；
  pvf_source\ US100 内层 PVF、dump\ 客户端内存镜像、us100_equiplst\ 等分析产物。
  2026-09-23 已清理约 350 个一次性调试残留文件）
- cn100\       国服 CN100 客户端的 PVF 解密产物与工具，与本工程无关，独立存放
  （见 cn100\README.md）
- server-us100\ Go 服务端（cmd\ 各工具默认 -source runtime/pvf_source/Script.inner.pvf）

## 运行方法

- 只启动服务端，在项目目录运行 `python tools/us100_interactive.py 13 normal --no-client`。出现 `server ready` 后，可用 `启动客户端.cmd` 连接本次分配的游戏端口。
- 同时启动服务端和客户端，双击 `启动服务端.cmd`。启动器会检查构建、客户端、数据库和频道端口。
- 游戏端口每次可能变化。当前监听地址记录在 `runtime/interactive/ready.json`，本次构建和客户端路径记录在同目录的 `run.json`。
- 只做启动前检查，运行 `python tools/us100_preflight.py`。默认客户端目录为 `D:\DNF\client\100US\DFO`，也可用 `DFO_CLIENT_DIR` 指向包含 `DFO.exe` 的目录。
- 前台运行时可在启动器窗口按 Ctrl+C 停止服务端。

## 待办（2026-09-20 更新）
- [x] US100 PVF 解包、catalogs 生成、v2.24 名单行布局
- [x] 频道选择（3 模式）+ 选角 + 进城
- [x] 启动链重建：预检、存储随启动器拉起、服务端 Job 对象、停止脚本
- [ ] 副本房间：选择界面出现后选本 → 28/29 布局（已按本客户端读序列重写，
      待实测）→ 房间内怪物行（24 字节一行，未恢复）
- [x] 100US 进城恢复角色 HP/MP。正常模式发送带角色属性的 USERINFO mode 1。2026-11-23 在 us100a 的城镇 HUD 实测，红球和蓝球均显示 100%。
- [ ] WFP 网络隔离（防客户端连官方服）：可照 115us 的 `probe.cpp` 实现
      （WFP 动态会话 + 按 APP_ID 的非回环 BLOCK + 远连自检）
- [ ] 版本门禁目前是标签比较：`configs/us100/rules.*.json` 与几个 `*-probe.json`
      带的是 90 级参考模型数值，不是本客户端恢复值（其文件内已注明）

## 内存注意事项
- PVF 读取已 mmap（无 390MB 堆复制）、解压块 LRU 上限（64MB）、文本 LRU
  上限（16MB）、文件表惰性解析、路径索引为排序哈希表；导入器
  （equipmentbuild 等）设 GOGC=10。最重导入峰值实测 2490MB→902MB。
- pvfinspect 默认不再写 matches.json（`-index` 才写，且受 `-max-matches`
  上限），避免数百 MB 中间文件。
