# 100US 服务端重建审计（2026-09-22）

目标：以 `D:\DNF\client\US100\DFO.exe`（2.24.0.124）为**唯一真源**重建服务端；
其他版本（115us/2.38、KR100、CN90）的资料**只作参考**，不进入运行代码；
外部依赖（PostgreSQL/Redis/他盘工具链）全部移除，数据库改为自有单文件 `.db`。

## 一、现状组件盘点（server-us100）

| 组件 | 来源与验证状态 | 处置 |
|---|---|---|
| `internal/game/wire`（帧头、id%14 密码槽、会话密钥 (i%127)+1） | US100 实测（抓包+解密对拍） | **保留**，100us 自有 |
| `internal/game/protocol` 进城包（mode0 127 字节块、mode1 23 字节块+尾、mode6 45 字节块、select/roster/skills/vault/area/fatigue/quests 各包） | US100 实测（进场成功、事件对拍） | **保留** |
| `protocol.PackedEntryStats` / `EntryAdditionProbe`（91 字节记录、2.38 布局） | **借用 115**（2.38 客户端逆向） | 标记参考；US100 的对应物=40 加密槽展开（待逆向），未接通前不发 |
| `character.EntryAdditionStatsUS100`（body 0x1f 记录） | 借 115 布局的假设；US100 实测**阴性**（记录位被 0x23 计数字节撞坏、槽区未展开） | 保留代码但**默认不启用**，注释标明假设状态 |
| `internal/storage`（pgx + go-redis） | 外部依赖 | **替换为 internal/db100**（本次重建） |
| `internal/catalog`（PVF 目录：角色/城镇/副本/任务/装备/技能…） | US100 的 Script.pvf 实测解析 | **保留** |
| `internal/{dungeon,inventory,loot,progression,quest,world,channelrefresh,admin,character}` | 业务层，建立在 storage 接口上 | 保留，随 storage 换装编译验证 |
| `cmd/wireprobe`（网关+注入+探针开关） | US100 实测 | 保留；启动参数去掉 PG/Redis 配置 |
| 工具：`tools/*.py`（dis/peek/scan/hook/inject/截图） | 自建，针对 US100 地址 | **保留**，为 100us 自有工具集 |
| 工具：`tools/dbquery.py` 等含他盘路径者 | 硬编码 E/D 盘 115 路径 | 已改读自有配置 |
| Go 工具链 | 曾指向 115 的副本 | 复制一份到本项目 `tools/go`（编译器中立，但归属自有） |
| NGS 桩 `ngs_stub`（NGClient64.aes 替代） | 自建（US100 专用偏移 0x4ec40b7） | **保留** |

## 二、客户端侧已钉死的事实（US100 实测，供新服务端对齐）

1. 帧：客户端 13 字节头 / 服务端 16 字节头；密码槽 = id % 14；会话密钥 (i%127)+1。
2. 进场顺序依赖：mode6 → mode1（键命中触发结算 0x143f03330）；mode1 缺包则永不离开加载态。
3. 面板/血球读数 = stat 对象的 **40 个加密槽**（值+校验对，解码 (x^0x1f2a025c)-4）；
   槽区从未被我们的包写入 → 0/0 与 -nan(ind)% 的根因；展开函数待逆向（候选见普查）。
4. mode-0 的 127 字节块是**文本结构**（fame 等）；mode-1 的 23 字节块=等级/经验+标志位。
5. CMD 1959 = 游戏时钟同步（非 HP），已撤下。
6. 启动期崩溃循环根因 = `AppData\LocalLow\DNF` 共享配置被崩溃会话写坏；全新配置即恢复。

## 三、新存储 db100 设计（自有格式 v1）

- 单文件 `runtime/us100.db`：JSON 文档 + 原子写（tmp+fsync+rename）+ 头部
  `US100DB1` + u32 版本 + u64 revision；打开 = 一次读（毫秒级，无外部进程）。
- 表：accounts / characters / character_world / character_quests /
  character_quest_rewards / character_quest_repairs / character_fatigue /
  character_fatigue_rooms / character_map_clears / character_birth /
  character_tutorial_flags / character_vaults / account_currency /
  admin_grants / character_events。行 = JSON，键 = 主键字符串。
- 并发：单写者（服务端进程）+ 进程内 mutex（替 Redis 锁）；读走读锁。
- 种子：`tools/export_pg_seed.py` 从旧 PG 导出 JSON，首次建库时导入（一次性迁移）。

## 四、里程碑（每步验证后才进下一步）

1. ✅ 审计（本文）。
2. ✅ db100 包 + 单元测试（8 项全绿：写/读/事务回滚/原子性/崩溃后一致性/共享实例/角色锁/种子导入）。
3. ✅ 种子导出 + 建库（tools/export_pg_seed.py → runtime/us100_seed.json，15 表规范化：
   bytea→base64、时间戳→RFC3339、date 列保持 YYYY-MM-DD；首开自动导入，现有行优先）。
4. ✅ storage 换装：13 个文件全部改写为 db100；charactercheck 18 项全 PASS
   （临时库隔离=独立 .db 文件；故障注入改走 Store.Database() 行级操作，见 cmd/charactercheck/dbops.go）。
   新语义要点：ErrNoRows 哨兵沿用旧错误文本；MeetNPC 幂等结算走新增 SettleQuestObjective；
   clear.go 的经验聚合走新增 MonsterRunExperience；全局名字唯一（lower(name)）在 CreateCharacter 内实现。
5. ✅ 去掉 pgx/go-redis 依赖（go.mod 只剩 x/crypto、x/text；Go 工具链+模块缓存已复制进 tools\）。
6. ✅ 真客户端端到端（2026-09-22 实测）：连接→握手→角色列表（848B，来自 .db 种子）→选择→进场
   （教程/金币/疲劳/仓库/背包/装备恢复 + enter_gameworld）→写回 us100.db（rev 2→13，
   world_position_saved / tutorial_flag_saved / quest_saved_and_sent）。
   启动链：一键脚本无 PG/Redis/守护进程；preflight.build_server() 用项目自有 tools\go 自动重编。
7. 🔶 面板属性逆向——重大突破（详见 docs/protocol/next39-entry-addition-v224.md「M7 突破」节）：
   US100 不存在 115 式"记录→40槽展开"；面板是计算型管线：
   stat 计算管线 → 索引 getter（0x143edf9e0/0x143ed8e50/0x143edafc0）→
   面板写方 0x142eb48d0/0x142ebaf70（槽 +0x304..+0x334，值+guard 成对）→
   面板 getter（0x143c00f85 族）→ UI。下一步：索引→语义映射、getter 输入反查至 mode-1 尾部字段，
   然后由服务端填真值点亮面板。

