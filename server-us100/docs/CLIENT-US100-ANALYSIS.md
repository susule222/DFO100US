# US100 客户端全面分析（D:\DNF\client\US100）

日期：2026-09-22。方法：全目录清点（tools/client_inventory.py → runtime/client_inventory.json）、
PE 版本资源、SHA256 身份表、容器魔数判别（tools/client_triage2.py）、活进程全量脱壳
（tools/dump_unpacked_pe.py → runtime/dump/DFO_unpacked.exe，已验证）。客户端目录零修改
（本项目规则：原始客户端文件永不改动；目录内本项目产物单独列出）。

## 一、客户端身份

| 项 | 值 |
|---|---|
| 主程序 | DFO.exe，171,276,720 字节，文件版本 **2.24.0.124**，Neople |
| SHA256（前32） | 5d276d08faca85a19846ea7e595e9993… |
| 壳 | Themida（.themida 节 9.9MB，rwx；TLS 节 0x67000 为壳服务） |
| 磁盘代码段 | .text 熵 5.95 ≈ 内存熵 → **代码段在磁盘上基本明文**（历次静态扫描直接读文件即有效的原因）；.rsrc 7.94（压缩资源） |
| 内存映像 | base 0x140000000，SizeOfImage 0xb963000（185.4MB），415 个内存段 |
| 版本对应 | 2.24 ⇔ 本项目所有 "v224" 命名（characters_v224、next39-entry-addition-v224） |
| 发行包来源 | 目录内 注册.bat（2016-11-19，中文"DNF美服吧"发布物）；文件时间戳两批：2021-05-26（原始美服文件）与 2023-02-27（DFO.exe/Script.pvf/sk.dat 等重打包批次） |

## 二、脱壳产物（本次新增）

- `runtime/dump/DFO_unpacked.exe`（185.4MB）：活进程全镜像重建 PE。
  - 10 节全部按 **文件偏移=RVA** 重建（FileAlignment=SectionAlignment=0x1000）；
  - .pdata 250,300 个函数条目（异常目录指向 .themida 内的运行时副本 0xb675028）；
  - IAT 为本次运行的已解析状态（绝对地址指向当次加载的 DLL，仅供分析不可重跑）；
  - 验证：面板 getter 0x143c00f6a、编码器 0x144f1f570 反汇编与活内存逐字节一致（tools/verify_unpacked.py）。
- 旧的部分转储（2026-09-21，27 个大段）仍在 runtime/dump/，dis_range.py 会自动优先用 r-x 转储。
- 元数据：runtime/dump/DFO_unpacked.meta.json（0 个读取失败块）。

## 三、反作弊栈（两套）

1. **NGS（Nexon Game Security）**：NGClient64.aes.real（4,784,640B，v5.3.3.7，NEXON Korea）。
   已被本项目 stub 替换（NGClient64.aes 189,440B，源码 ngs_stub/ngs_stub.c）：
   吞掉 MessageBox、NGS 初始化判定字节补丁（0x4EC40B7 je→jmp）、可经 ngs_stub.ini 关钩子。
   NGS 初始化在真机上恒返回 ffffffff（失败），stub 路线为工程上唯一可行解。
2. **BlackCipher（WiseLogic）**：BlackCipher/ 目录，BlackCall64.aes 23MB + BlackCipher64.aes 32MB +
   BlackXchg.aes 8.8MB + config.bc（ASCII-hex 加密配置）+ CrashReporter_64.dll。
   日志（*.log）全为加密二进制；BlackXchg.log 为 hex 编码交换记录（2023-02-27 后无新增
   —— 现行 stub 流程下 BlackCipher 不再产生活动记录）。
3. 辅助：SecureEngineSDK64.dll（Themida SDK 壳配套）、MachineIdLib.dll（机器指纹）、
   NPS.dll、bdvid32/64.dll + bdcap32/64.dll（BlackDesert 系视频/捕获组件）、DumpReport.dll。

## 四、数据文件与容器格式

| 文件/目录 | 格式 | 说明 |
|---|---|---|
| Script.pvf（390,977,429B） | 加密 PVF（无明文魔数） | 全部游戏数据源：技能/物品/任务/地下城/UI 脚本；密钥链=sk.dat；本项目已有解密与导入工具链（cmd/pvfinspect、tools/unwrap_us100_pvf.py、configs/*.generated.json 皆源于此） |
| sk.dat（1,408B） | 加密密钥块 | PVF 解密入口（tools/hunt_aes_key.py / pick_pvf_key.py 已攻克） |
| dstr.dat（1,429,744B） | 加密字符串表 | 客户端 UI/系统字符串（含能力表标签，"strength=物攻"证据源） |
| package.lst / localpackage.lst | 32B 加密头 + "LST\0" + 明文记录（55 项） | Neople 文件清单/更新校验表，两份哈希相同 |
| version.ini（4,680B） | 加密二进制（非纯文本） | 启动器版本清单 |
| Patch.trc（316,470B） | 补丁跟踪记录 | 2023-02-27 重打包批次产物 |
| ImagePacks2/（10,419 npk，24.4GB） | `NeoplePack_Bill` 魔数 | 精灵/贴图集 |
| SoundPacks/（121 npk，1.2GB） | `NeoplePack_Bill` | 音频集（FMOD） |
| Music/（648 ogg，1.0GB） | 标准 OggS | BGM |
| Video/（255+ avi，~5.4GB） | `Neople Video Fil` 自定义头 | 过场/职业视频 |
| Replay/（~1500 .rep） | 自定义二进制（d06cb3b1…头） | 技能演示/职业教程/PVP 连招录像（客户端脚本回放，非视频） |
| Shader/（8 fx + 8 fxo） | HLSL D3D9 | 渲染管线为 **Direct3D 9**（D3DX9_43/D3DCompiler_43） |
| Fonts/Cursor/Cef/controller_config | ttf / ani+cur / pak / vdf | 字体、光标（含 2022 冬季活动皮肤）、CEF 资源、手柄配置 |
| DebugConfig.txt | 韩文明文调试配置 | **开发者遗留**：[dungeon index] 144//11000、[map index] 17096//5013、[job]、[grow type] 等直入调试参数（潜在调试入口线索） |

## 五、可执行组件矩阵

| 组件 | 版本 | 角色 |
|---|---|---|
| DFO.exe | 2.24.0.124 | 游戏本体（Themida） |
| NeopleLauncher.exe / LauncherUpdator.exe | 2.1.1.26 | 官方启动器/更新器（本项目不用：直接 payload 启动 DFO.exe） |
| CEF.exe + libcef.dll | Chromium 96.0.4664.55 | 游戏内浏览器（商城/公告） |
| awesomium.dll + awesomium_process.exe | 1.7.5.0 | 旧版内嵌浏览器（与 CEF 并存） |
| fmod.dll / fmodex(64).dll | 2.2.9 | 音频引擎 |
| steam_api.dll / steam_api64.dll | Valve | Steam 接口（美服发行残留） |
| tbb.dll / tbbmalloc | Intel | 并行库 |
| libzmq-v100-* | ZeroMQ | NGS/后端通信组件 |
| msvcr71/100/100d、msvcp*、atl100、dbghelp、ijl15、xinput9_1_0、icudt(l) | — | 运行时支撑 |
| 7z.exe | — | 更新解包 |
| v8_context_snapshot.bin / snapshot_blob.bin / icudtl.dat / libGLESv2 / libEGL / vulkan-1 / d3dcompiler_47 | — | CEF 附属 |

## 六、本项目在客户端目录的产物（非原始文件）

- NGClient64.aes（stub，189,440B，2026-09-22 17:39 无 Sleep 构建）+ NGClient64.aes.20260920-133136.bak（原始备份）
- ngs_stub.ini（1=0…6=0, hooks=1；ord5=0 强制，防 22.7GB 内存泄漏）+ 两份 .bak
- ngs_stub.log（stub 运行日志）、LagLog.txt（客户端自带）、CrashDNF2.cra（1B 占位）
- ScreenShot/（客户端截图目录）

## 七、对服务端开发的直接价值

1. **DFO_unpacked.exe 是后续一切逆向的规范底本**：偏移=RVA，全部现有工具
   （dis_range/callers_of/find_ptr/encode_census/find_field_writers）可直接指向它
   （当前它们读原始 DFO.exe + dump 映射，结果一致；新工具建议直接用 unpacked）。
2. Script.pvf 的 **UI 脚本**是 M7"槽索引→面板字段"映射的权威来源（下一步：
   在 PVF 里检索状态面板脚本，与 getter 调用方 0x143c00f85 族读取的 +0x30c+8k 槽对号）。
3. DebugConfig.txt 证明客户端保留开发期直入调试通道（dungeon/map index 直跳），
   可作为后续地图/副本实验的备选入口。
4. dstr.dat（解密后）给出全部 UI 字符串 → 面板行名与槽位顺序的第二证据源。
5. 双反作弊均已失效化（NGS=stub、BlackCipher 无活动），启动路径稳定：
   payload 直启 + stub 补丁 + 秒级 db100 存储。

## 八、未解/风险项

- .rep 回放容器与 Patch.trc 结构未解析（当前无需求，仅登记）。
- IAT 为运行时快照：若需"可重跑"的脱壳 PE，还要做导入表重建（当前分析型使用无此需求）。
- package.lst 32B 加密头算法未逆（清单本体明文可读，暂无需求）。
