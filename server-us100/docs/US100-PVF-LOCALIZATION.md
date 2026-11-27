# US100 PVF 简体中文候选

这套流程修改 US100 内层 PVF 中现有的 UTF-16 `.uv.str` 值、白名单 XUI 显示属性，以及已逐单元验证的模拟器和剧情直写显示值，再按客户端原有的封装格式生成单独的 `Script.pvf`。不替换客户端文件，不移植 CN100 玩法脚本，不改条目 ID、路径、玩法参数或资源。

## 产物

已完成离线验证的检查点在工作区的 `runtime/localize-us100-zh-CN-r23/`。该包修改 716,950 条 STR，尚有 705 条待译，另外 489 条命令、内部标识和占位符原样保留，其中 170 条没有可用原文。待译记录包含 650 种原文，其中 560 条带有直接数值，542 条有静态脚本引用。它不是全量发布版；后续批次的完成状态以各版本的 `completion-status.json` 为准。

R12 修改 2,391 个 XUI 直写显示值，已扫描的显示字段无待译。另在智慧的引导模拟器的两份编译脚本中修改 229 个名称及说明，在保持原有字符串池内容的前提下追加宽字符值。R13 增补 11 份剧情脚本中的 20 处台词与角色称号。重开内层包后，逐项确认了共 249 处译文及其余 7,680 个保持原样的单元。

R5 在 R2 的基础上增加 97,891 条：43,357 条通过国服 100/115 和美服 115 的对应文本补齐，36,734 条来自自行翻译的装扮组合词表和文案，另外 17,800 条是 `Test` / `test` 测试占位符。R6 再补 2,392 条同类别拼写变体、克隆装扮和物品名。R8 在 R6 的基础上导入通过数字、控制标记、段落结构及部分术语检查的 108,556 条本机模型译文，共修改 579,267 条，138,877 条仍保留原文。模型译文是待复核稿，不等于逐条人工验收。

R8 另在 785 个 XUI 中替换 2,209 个直写显示值，确认 XML 节点、控制 ID 和所有非显示属性不变。明确属于数字占位、编辑器占位、已有中文或运行时字符串引用的值不算缺译。

R8a 增至 631,338 条字符串和 2,294 个 XUI 直写显示值，包内仍有 86,806 条字符串及 408 个 XUI 值待译。R8a 对本机模型译文另做了冷却共用、叠加上限和专有名词检查，修正了 579 个去重文本值，另有 77 个被拒绝。后续批次继续使用这些检查，并补充了确定性的装备词条、称号宝珠及 XUI 时间、等级、积分模板。

- `Script.pvf`：客户端格式候选，已完成外层加解包往返校验。
- `Script.manifest.json`：来源和候选 SHA-256、封装分段数和验收状态。
- `coverage.json`：按表统计纳入范围、译文和缺译条目。
- `translations.jsonl`：每个改动的表、键、译文来源、原文和译文。
- `untranslated.jsonl`：仍保留原文的记录及原因。
- `preserved.jsonl`：明确不能翻译的命令、符号、字体名和内部标识。
- `verification.json`：键、换行、控制标记和机械描述数值校验；另列脚本静态引用次数和高频缺译项。引用次数不是游戏内出现频率。
- `xui-coverage.json`、`xui-untranslated.jsonl`：XUI 直写显示值的覆盖和待翻清单，不混入字符串表的分母。
- `xui-preserved.jsonl`：后续版本列出的缩写、编辑器占位或需要人工确认显示用途的条目。
- `completion-status.json`：原文件与候选哈希、两类待译数量及原版缺值引用；只有所有已跟踪的可见文本和待分类条目都无缺项时，才认定静态译稿覆盖完毕。
- `inline-verification.json`：对允许修改的直写字段复核其译文和相邻单元。
- `residual-audit.json`、`residual-by-source.jsonl`：待译条目去重、按表/原文形式分组，并附相关静态引用。该数量不是运行时显示次数。

已通过内层重开、改动表和相邻未改条目的字节比对。未进入游戏验证载入、系统字体回退或显示效果。客户端附带的 Calibri/Tahoma 未覆盖抽查汉字。客户端独立的 `dstr.dat`、白名单之外的直写字符串、素材里的文字，以及其他语言回退表仍未修改。另用 `pvflocalize -inline` 检查了 3,215,960 个具有足够长度的编译脚本；候选清单在 `runtime/localize-us100-audit/inline-display-full.json`，其中包括内部名称、空值和文本引用，并不等同于实际可见条目。Lua/COS/JSON 旁路检查在 `runtime/localize-us100-audit/aux-text-audit-r2.json`。原包的缺值引用见 `runtime/localize-us100-audit/missing-value-references.jsonl`，与存在原文但尚未汉化的条目分开。

## 任务界面与宋体

2026-11-24：在 `D:/DNF/client/100US/DFO` 安装独立于 PVF 的任务界面 R2。`Fonts/calibri.ttf` 和 `Fonts/tahoma.ttf` 使用原宋体的轮廓，移除小字号内嵌点阵；文件仍保留客户端需要的字体名称。4 个 NPK 只替换经对照核验的 88 个帧槽，包括任务标签（`Act` → `主线`）、章节和部分地图贴图。保留美服资源索引及未选中的帧。重新进游戏后的清晰度和渲染范围尚未验收。

安装前后文件指纹见 `runtime/us100-quest-ui-r2/deployment.json`，原文件保存在同目录的 `before/`。确需撤回时，退出客户端，在工作区执行 `python tools/us100_quest_ui_install.py --bundle runtime/us100-quest-ui-r2 --rollback`；脚本拒绝覆盖与记录不符的文件。这次没有修改 `Script.pvf` 或 `dstr.dat`。

实机反馈：R2 安装后任务列表及右上角追踪栏图标显示红叉。已单独从备份恢复 `sprite_interface_quest.NPK`，和原版哈希一致。该包中的汉化任务标签已撤回；字体、章节和地图包仍保持 R2 安装状态。不要重新部署 R2 的任务图标包，直至完成同版本客户端的视觉回归验证。单包恢复参数为 `--restore-quest-icons`，收据状态为 `quest_icons_restored`。

用户确认恢复原版后两处红叉均已消失。后续的 `runtime/us100-quest-act-fixed-r3/` 从这个原版包重新生成，只修改 `quest_tag.img` 的 39/44 号帧和 `questguide.img` 的 19/53 号帧文字区。NPK 大小、成员偏移、IMG 帧头、压缩区间长度和其余字节保持不变，四个 zlib 数据流均完整解码且无尾随数据。R3 只安装该任务图标包，不替换字体、PVF 或 `dstr.dat`。用户已实测任务列表与右上角追踪栏「主线」正常、无红叉。需要恢复原版图标时，退出客户端，在工作区执行 `python tools/us100_quest_act.py rollback --bundle runtime/us100-quest-act-fixed-r3`。

任务窗口文字试用包 `runtime/us100-quest-strings-diagnostic-r1/` 以 R23 为底本，对照 14 个 `string/dnf.uv.str` 键并实改 8 处。只重建了一个内层组，回读导出的表与校对表逐字节相同，外层封装已往返核验。图标 R3 已验收后才启用此包；只更换 `Script.pvf`，保持图标、宋体和 `dstr.dat` 原样。观察存档中 `[Main Quests]`、`<Complete condition>`、`Lv. 3 Quest` 是否分别变为「主线任务」「完成条件」「3 级任务」。没有实机结果前不得认定动态文案已修好。需要还原 R23 时，退出客户端，在工作区执行 `python tools/us100_quest_text_trial.py rollback --bundle runtime/us100-quest-strings-diagnostic-r1`。

任务截图中，正文已加载中文，但客户端运行时仍出现英文界面短语。R23 的 `string/dnf.uv.str` 内已查到同文键，不能因此认定该窗读取的是这张表。下表是下一次验证的对照，**不是已安装译文**。`dstr.dat` 尚无经原版回读验证的解包及重建工具，不直接套用其他版本的表。

| 界面原文 | PVF 对照键 | 校对用译文 |
| --- | --- | --- |
| Act Completed | 21771 | 已完成主线 |
| [Main Quests] / [Other Quests] | 100008129 / 100008152 | [主线任务] / [其他任务] |
| In progress | 100008121 | 进行中 |
| Lv. %d Quest | 100007208 | %d 级任务 |
| `<Complete condition>` | 65009 | `<完成条件>` |
| View Optimal Level quests only | 100008123 | 仅显示推荐等级任务 |
| Abandon / Close | 40005 / 40003 | 放弃 / 关闭 |
| Grand Flores / Hendon Myre | 30051 / 44141 | 格兰之森 / 赫顿玛尔 |

`Abandon` 的 R23 译文误用了同名技能译法，后续应按表和键修复，不做全局词替换。页签 `All` / `Guide`、竖排 `Rewards` 与底部 `Cancel` 的实际显示来源也尚未核定；地图背景两处英文的位置未做实机对照。

实机回传：文字试用包启动后的截图中，主线计数、完成条件和等级仍是英文；选角页的常用角色提示和底部按钮也是英文。内存只读检查同时命中试用 PVF 的中文计数和原版英文计数，不能把加载了译文等同于渲染成功。

已用 100US 原执行文件中加载器引用的材料完整解开 `dstr.dat`：前缀为双层 AES-CBC，内层为 `SC01` 连续字异或。明文等于原版 `string/dnf.uv.str` 转为 UTF-8 再去除尾部 NUL；用原生链路重新编码得到逐字节相同的原始文件。参考线索为 [115US 公开格式说明](https://gitgud.io/fuckworld/115/-/raw/87a208c2888a673a51b3b7ebc88375055e49d9e8/analysis/dumps/README.md)，但 115US 参数不适用于本机 100US。`runtime/us100-dstr-ui-r1/` 仅替换 86 条与任务窗口或选角页相关的文案及通用「全部」「取消」，重新打开校验通过，不生成或改写字体、图标和 PVF。工具：`tools/us100_dstr_ui.py`；需退出客户端后才能安装或回退。

## 贝尔玛尔北部地图

截图中的 `Grand Flores`、`Elvenguard`、`Guild Hideout` 和 `Principality of Bel Myre - South` 属于 `sprite_interface2_worldmap_step_1(town).NPK` 内的 `north_belmire.img` 贴图。它们不是前一轮 `dstr.dat` 对应的文字。国服和美服整帧的道路位置不同，因此只把国服文字区域对齐到原来的美服地形及路牌内，不移植整个地图。

2026-11-24 安装 `runtime/us100-north-map-r2/`：局部修改 9 个帧的文字像素，保持 NPK 文件大小、成员索引偏移、帧头和各帧压缩长度。帧区间以外所有字节不变，`dstr.dat`、`Script.pvf`、宋体及任务图标不写入。预览 `comparison.png` 和部署收据同目录存档，实机待验。脚注 `Move to Area` / `Move to Different Village` 的实际资源未确定，本轮未改。

回退：退出客户端后，在工作区执行 `python tools/us100_map_north_belmire.py rollback --bundle runtime/us100-north-map-r2`。

## 复跑

从工作区编译 `server-us100/cmd/pvflocalize`。用 `-source <内层PVF> -dir <新目录>` 分别导出 US100 和 CN100 的字符串表。用 `-refs <新JSON>` 导出 US100 的静态脚本引用，另用 `pvfinspect` 导出 US100 的 `list/n_string.lst` 文本版以映射表 ID。

1. 用 `tools/localize_us100_tables.py --us <US表目录> --cn <CN表目录> --out <新目录> --glossary tools/us100_zh_glossary.json` 生成补丁。可用 `--overlay` 提供额外的原文到译文 JSON 字典。原版数值或控制标记不一致时，不把对应文本强行套进包中。
2. 用 `tools/verify_us100_localization.py` 比对表文本结构，参考 `--help` 指定原表、候选表、引用 JSON、列表文本和输出文件。
3. 用 `pvflocalize -source <US内层PVF> -dir <候选目录/replacement-tables> -output <新内层PVF>` 重建候选内层。
4. 用 `tools/rewrap_us100_pvf.py --original <原版Script.pvf> --baseline <US内层PVF> --candidate <新内层PVF> --output <新Script.pvf>` 复原外层封装。原版同目录需要客户端自带的 `DFO.exe` 和 `sk.dat`；工具不会改动或输出这些文件。

要续作而不重新改写旧译文，使用 `tools/extend_us100_localization.py --us <原US表目录> --previous <旧候选目录> --out <新目录>`，可提供 `--cn100`、`--cn115`、`--us115` 参考表或 `--manual tools/us100_zh_supplement.json --avatar-terms tools/us100_avatar_terms.txt`。随后依次执行上面的第 2 至 4 步。R3 比对国服 100/115 和美服 115，R4/R5 在 R3 的基础上补自译。模型译稿应先抽审再决定是否通过 `--machine` 导入，不等同于已校对文本。

## 本机批处理

将已核对发布 SHA-256 的 Hy-MT2 GGUF 加载到只监听回环地址的 llama.cpp 后端。批处理脚本通过 `US100_LOCAL_MODEL_TOKEN` 使用本地口令。游戏文本不发送到外部服务。

- `tools/run_us100_localization_batch.ps1` 处理首轮、复核并打包，然后使用保留原数字的方式重试被拒绝的单元。检查点在 `runtime/localize-us100-mt/hy-grouped.sqlite3`，可续跑不重复翻译已通过单元。
- `tools/run_us100_localization_residual.ps1` 对重试后仍有原文的条目补作整条译稿，在已有候选校验通过后才合并打包。
- `tools/run_us100_localization_rescue.ps1` 处理仍待译的句段，优先复用已通过的同句翻译，再用本机模型翻译剩余句段。新增句段通过条件、数值、单位和控制标记检查后才合并。可用 `-PreferPlain` 结合本机 7B 模型先尝试保留原数值的直译，使用单独的 SQLite 缓存。
- `-IsolateFormat` 只翻译控制标记之间的可见文本；`-ClauseTails` 将可确定的持续时间、间隔和消耗直接按原值填入；`-ExplicitLayout` 使用带格式范例的提示与独立缓存，在同样的校验下可切换至 [Qwen3-8B](https://huggingface.co/Qwen/Qwen3-8B-GGUF) 本机后端。
- 对带有多个段落的残留，可用 `-WholeValue` 保留整条上下文，使用 `-ReviseInvalid -RetryFailed` 将第一版稿的数值、术语或条件错误反馈给本机模型。反复未通过的条目保留原文，不以未经证实的数字或补全说明代替。
- `tools/us100_grouped_llm.py` 不带 `--translate` 时，只读导出已完成的检查点；它不会宣告队列已完成。
- `tools/pack_us100_localization.ps1` 把指定快照重建为新版本，分别生成 STR 和 XUI 报告，再复核内层和外层哈希。不写客户端目录。
