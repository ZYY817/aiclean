# AIClean 界面问题清单

检查日期：2026-11-13。检查方式：真实 exe 逐页截图 + 浏览器版（模拟数据）补看。截图在 `_shots/review/`。

修复状态：2026-11-13 全部完成并截图验收（真实 exe + 模拟点击）。

## 必须修

- [x] **1. 赞赏码太小，手机扫不出来**（关于页）
  - 已裁切：`assets/tip-wx.jpg` → (320,180)–(830,700)；`assets/tip-zfb.jpg` → (175,645)–(1105,1675)。
  - 页面显示 280px（`08-settings.css` `.tipbox img`，object-fit:contain 不裁码）。
  - 点击图片弹出 ~420px 大图（`tipZoom()`），保留"微信/支付宝"标签。

- [x] **2. "只统计占用"的项被算进了"可清理"**（总览、数据）
  - `kindOf()` 新增 `stat` 类（reportOnly → 'stat'，KINDS.stat = "仅统计"）。
  - 不计入 `summary().count`、不进 `homeClean()`；计入 `s.ai`（真实占用）与格子阵（独立 `.c-stat` 描边格）。
  - 数据页独立"仅统计"分组 + 筛选 chip；图例、排行条同步显示。

## 应该修

- [x] **3. 首页说"AI 工具占了 67.8 GB"不准确**
  - 改为"已识别 X GB 占用数据"；扫描中/空态文案同步去 AI 化（`20-home.js`、`30-data.js` 空态）。

- [x] **4. 底部排行条出现"各应用 4.65 GB"**
  - `rules.json` `local-programs` 规则 `tool` 改名"已装应用"（截图验证排行条显示新名）。

- [x] **5. 数量对不上：导航"数据 333"，进页面后"全部 356"**
  - 统一口径：`summary().listAll`（含已迁移+仅统计+大文件），导航徽标和"全部"chip 同用它。副标题注明"X 项可处理，另有 Y 项已迁移，Z 项仅统计"。

- [x] **6. 设置页副标题还写着"外观和关于。"**
  - 改为"外观和数据。"（`50-settings.js`）。

- [x] **7. 关于页版式和其他页不一致**
  - 改为 `.pg` + `.pg-h` 结构：34px"关于"标题 + 副标题 + 右侧 `v1.0.0` 大版本号；`.about` 去掉居中，居中 980px 栏宽。

- [x] **8. 关于页的功能描述需要核对**
  - 两条后端都已实现：**自动清理**（设置页原有计划任务入口，描述补"设置页开启"）；**残留还原**（`ops.List` 超期自动清）。
  - 设置页新增"清理残留"行：保留期选择（7/30/90 天 → `prefs.quarRetainDays` → `cleanItem` 使用）+ "查看残留"入口。

## 顺带

- [x] **9.** 截图验收改用 `_shots/review/cap.ps1`（窗口移动+PrintWindow），配合合成点击脚本验收交互。

## 验收记录

- `go test ./...` 全绿；前端逻辑 `47 passed, 0 failed`（新增 stat/仅统计/listAll 断言）。
- 真实 exe 截图验证：首页"仅统计 38 项 · 29.7 GB"独立图例行；数据页"全部 355"= 导航徽标 355；关于页版式与其他页一致、双码 280px 显示、点击弹 420px 大图；设置页"清理残留"行含保留期段选和查看入口。

## 第二轮（2026-11-14）

- [x] **10. 支付宝赞赏码下方个人姓名需隐藏**
  - 重新裁切 `assets/tip-zfb.jpg` → 930×905 纯码区（名字"XX奇"已裁掉），页面 + 放大图同步生效。

- [x] **11. 应用自身数据不能占 C 盘**
  - `storeRoot()`：优先选"固定 NTFS 且非 C 盘"中可用空间最大的盘（本机 F:），存 `X:\AIClean`；没有第二块盘才回退 `%LOCALAPPDATA%\AIClean`。
  - 首次启动把旧 C 盘数据目录一次性搬到新根：复制完整→删旧目录；复制失败→清半成品、保留 C 盘原样、下次重试；目标盘掉了/建不了→自动回退 LOCALAPPDATA，不会打不开。
  - 实测：`F:\AIClean` 已建（logs/quarantine/prefs.json），`%LOCALAPPDATA%\AIClean` 已删除，顶栏显示"F 盘 149.0 GB 可用"。
  - `drives()` 枚举从 C–J 扩到 C–Z。

- [x] **12. 帮用户快速把数据搬到其他盘**
  - 新增迁移专用校验 `guardPathMig`：reportOnly 汇总目录（文档/桌面/微信文件/Devin 数据等大目录）允许整体搬家，删除路径仍被 `guardPath` 拒绝。
  - 数据页"仅统计"行尾按钮改「搬到其他盘」→ 走迁移向导（选盘→逐项预检→按住确认→复制校验→留链接→备份可删可迁回）。
  - 详情卡补充说明搬家是可逆操作、软件照常使用。
  - 回归：`TestGuardPathMig`（reportOnly 放行/锁定/已迁移/未知路径拒绝）；前端断言 rowHTML stat 行出"搬到其他盘"且不出"清理"。
  - 截图验收：`#items f-stat` 直达仅统计筛选页，38 行全部显示「搬到其他盘」、复选框禁用。

- [x] **13. 缺陷修复（本轮自查）**
  - 拷贝旧数据目录中途失败会留下半成品新根 → 现在失败即 `RemoveAll(root)` 下次重试。
  - 新根所在盘被拔掉（移动硬盘）会导致 `mustStore` 永久失败、应用打不开 → `NewStoreAt` 失败自动回退 `NewStore()`。
  - 增加调试口令 `f-stat`（`#items f-stat` 直达指定筛选页），替代真实鼠标模拟做截图验收，不干扰用户操作。

## 第三轮（2026-11-14 晚）

- [x] **14. 窗口无法拖动、边框不可用**
  - 三层排查才到真根因：①旧 `winDrag`→`ReleaseCapture` 跨线程天然失效；②改 `WM_NCHITTEST`→`HTCAPTION` 后 `SendMessage` 探针证明父窗口命中正确，但实测仍拖不动；③查子窗口属主发现 **`Chrome_RenderWidgetHostHWND`（真实接鼠标的输入窗）属 msedgewebview2.exe 外部进程**——跨进程不能子类化，鼠标命中根本到不了本窗口。
  - 最终修法：**JS 在 `[data-drag]` 区 pointerdown → `go('winDrag')` → Go 轮询 `GetCursorPos`+`SetWindowPos` 手动跟手移动，`GetAsyncKeyState` 检左键松开即停**——完全不依赖窗口消息，与进程内窗口层级无关。带防重入、最大化态先还原再跟手。
  - 拖动区域 = 顶栏空白（logo 区、导航与按钮间隙）。边缘缩放、双击顶栏最大化保留。父窗口 `HTCAPTION` 命中测试保留作未被覆盖区域的补充。

- [x] **15. 软件没有图标**
  - 新画 `assets/icon.ico`（`gen_icon.go` 生成：2×2 圆角方块，同应用内 .lg 品牌——两格墨色+一格靛蓝+一格描边，16→256px 七档 PNG 内嵌）。
  - `go:embed` 进 exe，启动时 `CreateIconFromResourceEx` + `WM_SETICON` 设大/小图标——任务栏、Alt+Tab、任务管理器都有 logo，不需要 .syso 资源文件。
  - 验证：`WM_GETICON` 返回非零 HICON（big/small 都已设上）。

- [x] **16. 设置页"数据目录"行左右不对齐**
  - 第二个 `<dd>` 在 `200px+1fr` 网格里掉回标签列 → 合并进单个 dd，按钮和说明与右列对齐。截图验证通过。

## 第四轮（2026-11-14 晚，逐页终审）

- [x] **17. 已迁移页三处缺陷**
  - 大小列整列"-"：账本 `migrations.json` 不在当前数据目录（ junction 检出迁移态但无记账）→ 无账本时回退显示「已迁到 X 盘」（junction 实际目标）。
  - 已迁移且可 prune 的项（.codex\sessions）行尾错显「清理旧文件」→ act 链 `moved` 提前，已迁移项统一「迁回」。
  - **`rollbackMigration` 强依赖账本 → 无账本迁回必报错**（死按钮）→ 现在无账本时 `junction.Read` 推导目标盘，`migrate.Rollback` 只需 link+dst 即可跑通。

- [x] **18. 锁定/管理员项误渲操作按钮**
  - "正在使用"组里 prune 语义项（.claude\projects）显示「清理旧文件」——点了必然失败。
  - act 链 `lock || needsAdmin` 提前：锁定→"使用中"，管理员→"需管理员"，不再出按钮；`selectable` 同步排除 needsAdmin。
  - 回归断言：lock+prune 无按钮、moved+prune 只给迁回、admin 无清理不可选（55 断言全绿）。

- [x] **19. 规则库两项危险分类**
  - `local-programs`（Local\Programs 全部 per-user 应用本体）可"清理"→ 改 `reportOnly`，只统计+可搬家，杜绝一键卸掉所有应用。
  - `codeium-dotdir`（`.codeium` = Windsurf 配置/记忆/扩展状态根目录）action=delete+risk=safe → 改 review，note 写明"整体删会丢配置"。

- [x] **20. 设置页"迁移位置"反斜杠被 JS 吃掉**
  - 模板字面量 `目标盘:\AIClean-Moved\工具名\` 中 `\A`/`\工` 被当转义序列吞掉 → 显示粘连。改 `\` 转义。
  - 全库扫同型 bug：30-data.js 迁移盘选项 `:\AIClean-Moved` 已是双反斜杠（正确）；20-home.js `rules\.json` 是正则转义（正确）。

- [x] **21. 逐页验收截图**（PrintWindow 被动捕获，零输入模拟）
  - 总览/数据(全部+仅统计+已迁移+待确认+正在使用)/残留/记录/设置/关于，双主题核对居中与对齐。
  - 高窗截图（1300px）验证设置页全部 9 行网格对齐。

## 第五轮（子代理深度审计）

- [x] **22. release/rules.json 与根 rules.json 过期漂移（发布阻断）**——外部副本优先于内嵌加载，而两份副本停在旧版（local-programs 可删、codeium-dotdir 标"安全可删"）。已同步三份 + 新增一致性测试：外部副本必须与内嵌逐字节相同，漂移即 FAIL。
- [x] **23. `ops.Delete` reparse 防护是死代码**——Lstat 对 junction 返回 IsDir()==false（ModeIrregular），检查在目录分支内永远到不了；junction 被当"文件"静默解链，已迁移数据失去入口。reparse 检查提到 IsDir 之前 + `TestDeleteRejectsJunction` 回归。
- [x] **24. `migrate.Rollback` 数据全丢路径**——目标被外部删除时仍删 junction+删 bak；现改：dst 没了从 bak 还原，两边都没了直接拒绝（保 junction 留线索）。`TestRollbackDstGone`/`TestRollbackBothGone` 回归。
- [x] **25. `ops.Purge("")` 清空整个隔离区**——Join(quarDir,"")==quarDir 过前缀校验。加空 id/`p==quarDir`/强制子路径三重拒绝；`Restore` 同款路径穿越一并修 + 跨卷还原补字节/文件数校验（与 Quarantine 对称）。
- [x] **26. 删除没干净的残留不进隔离区**——`s.Quarantine` 唯一调用方是死绑定 `cleanItem`，残留页永远空。`deleteItem` 删除失败且源目录仍在时自动移入隔离区（可恢复、期满自动清）——设置页描述的功能现在是真的。
- [x] **27. `runAutoClean` 不检查 ReportOnly**——`action:delete + reportOnly` 组合会被计划任务静默整删汇总目录。补 `it.ReportOnly` 跳过，防线前移。
- [x] **28. `pruneAppL` 释放量虚报**——`os.Remove` 前就计入字节数；改为删成功才计数。
- [x] **29. 规则库整改（13 条）**
  - `yarn-cache`：`Local\Yarn` 整目录（含 yarn 本体+全局包）→ 收窄 `Yarn\Cache`；
  - `windows-old`：review 可删 TrustedInstaller 目录 → `reportOnly`（不给删除，只统计/可整体搬走）；
  - 游戏存档三件套（mc-saves/saved-games/my-games）+ hf-cache/ollama-models/lmstudio/dingtalk-files：note 写"建议迁移"但 review 只有删除按钮 → 全部改 `action:migrate`；
  - wsl-distros/docker-wsl：改 migrate + 补 vmmem/wslservice 等进程锁定；
  - antigravity-updater：整目录 delete 可能卸更新器本体 → review；
  - spotify-data/vscode-ws/m2-repo：risk safe→caution + note 写实（离线下载/工作区数据/私有构件）；
  - `heuristics.deletePatterns` 去掉 "updater"（会把更新器程序目录误判为可清缓存）；
  - C:\ 根目录补 requiresAdmin（perflogs/driver-intel/amd/nvidia）；ea-desktop-cache 补 requiresAdmin；
  - codex-cache 补 requiresClosed；jetbrains-caches 补 datagrip64/phpstorm64/rubymine64/studio64/fleet；
  - wechat-roaming 补 wemeetapp 锁定 + note 写明含腾讯会议数据；
  - 新增 `wechat-files`（Documents\WeChat Files 微信 3.x 主目录）与 `npm-cache-roaming`。
- [x] **30. 杂项**：mock 硬编码开发路径 `E:\C盘清理` → 中性占位；`var _ = json.Marshal` 死行；`.ab-hero` 死 CSS；加 `.gitignore`；`schedSet` 参数经 argv 数组传递无注入（schtasks 自行校验 HH:MM）。

## 第五轮：子代理审计 + 深度复查

22. **WSL 规则误匹配系统组件（高危）** — `Packages\*\LocalState` 通配把 ~90 个 UWP 系统包
    （GetHelp/WindowsStore/XboxGamingOverlay 等）当"WSL 发行版"列入建议迁移。
    新增规则字段 `matchFile`：目录须含 `*.vhdx` 才算 WSL → 迁移组 95→3 项，全部真实。
23. **Program Files 下 4 条删除规则缺 requiresAdmin** — steam-shadercache/depotcache/dumps、
    uplay-cache 补 `requiresAdmin`，非提权时锁定。
24. **包管理器缓存不加 requiresClosed（有意决策）** — npm/pip/gradle 等进程名（node/python/java）
    在开发机常驻，锁定会长期误报"使用中"；缓存储库本身设计为可随时重建，保持不锁。

回归：go test 全绿；前端 55 断言；三份 rules.json 哈希一致。

## 第六轮：四子代理深审 + 手工修复（发布前安全审计）

后端高危（全部修复+测试）：
- finishMigration 不再信前端传的 bak 路径——要求精确匹配未完成账本记录 + junction 目标匹配，防遍历删任意目录
- rollbackMigration 仅限账本在册迁移；跨卷迁回从 os.Rename 改为复制+校验（生产必败修复）
- pruneItem 校验规则确实 prunable，拒绝任意 {path,days} 调用
- runAutoClean 跳过 Special（回收站/休眠/WinUpdate）且只处理 risk=safe，加 scanning 互斥
- loadRules 外部损坏回退内嵌；rulesPath 不再返回字面 "embedded"
- isAdmin 改 TokenElevation 正规判定；evalJS recover；migDst 消毒；findBigFiles 根 reparse 跳过；migPreflight drive 校验
- guardMigOK 加 noMigrate + 祖先 junction 检查（防嵌套迁移）；migrateItem 加 migMu 互斥
- copyTree 跳过 FILE_ATTRIBUTE_REPARSE_POINT 文件（防 OneDrive 占位符水合）；Quarantine/Restore 跨卷失败清半成品
- prefs.json/migrations.json 损坏先备份再重建（不再静默覆盖）

前端高危（全部修复+测试）：
- 顶栏拖拽加 5px 阈值：单击不再误还原最大化窗口
- 扫描按钮 pointerdown 锁定意图：按下到触发之间扫描完成不会把"取消"变成"清理"
- go() 加 120s 超时 + 挂起清理，WebView 回调丢失不再永久挂起
- Home.busy / Dlg.locked 全部 try/finally；ST.op 全局互斥（清理/迁移/猎大文件/残留 互斥）
- 迁移完成页"删除备份"改 confirmDlg hold:true（与其他不可逆操作一致）
- 批量勾选排除 special+big（行内单独确认保留）；切筛选清空勾选
- 清理结果区分"全部成功/部分残留/失败"，residual 正确计数并提示去「清理残留」
- --warn 三主题补定义（.c-big 格子不再 fallback）；日志 OPS 补 autoclean 映射
- rulesPath "embedded" 显示友好文案并禁"打开位置"；版本号不再二次 esc

规则库（212→228）：
- noMigrate：onedrive×2 / local-programs / windows-old / desktop-dir / documents-dir
- 降 review（出一键清理+自动清理桶）：m2-repo / spotify-data / vscode-ws×2；降 caution：nvidia-dl / minidump / memory-dmp / crashdumps-user
- reportOnly+scanHeuristics：vbox-vms / vmware-vms / android-sdk / lmstudio-local
- office-filecache×2 改 user-data + requiresClosed Office 全家（挂起更改保护）
- requiresAdmin：steam-downloading / steam-workshop-dl / steam-screenshots / delivery-opt
- requiresClosed 补齐：dingtalk/feishu/battlenet(Agent)/wsl(wslhost)/epic/roblox/steam-dumps/nuget(msbuild,VBCSCompiler)/continue(Code,Cursor)/mc×3(javaw)/workbuddy/jetbrains(+rustrover,dataspell等9款)/adobe(+MediaEncoder,Audition)/qq+TIM/wechat+TIM/ollama+app
- delivery-opt 改真实缓存路径 ServiceProfiles\...\DeliveryOptimization\Cache
- 浏览器缓存改 * 通配覆盖多 profile（Default/Profile 1...）
- windows-old note 改引导磁盘清理；gamedvr/mc-screenshots/steam-screenshots 改 user-data
- 新增 15 条：DO 真实缓存 / WER 用户队列+存档 / 新 Teams 缓存 / 新 WSL 格式 / nuget v3+plugins / cargo src+index / gradle dists / yarn berry / pnpm 旧 store / SquirrelTemp / vscode-insiders 扩展 / hyperv-vhd

验证：go test 全绿 / 前端 55 断言 / 三副本哈希一致 / 截图确认 noMigrate 隐藏搬家按钮、使用中项正确锁定、待确认组全是真实风险项

## 第七轮：发布前功能审查（14 项，逐条对照代码核实）

| # | 问题 | 修法 | 回归测试 |
|---|------|------|----------|
| 1 | 数据目录每次启动重挑盘会漂移 | `storeRoot` 先读 `%LOCALAPPDATA%\AIClean-root.txt` 指向文件，盘在就固定用；盘不在才 `pickStoreRoot` 重选并写回指针 | `TestStoreRootPointer` |
| 2 | prefs/migrations 损坏被静默覆盖、写一半文件坏 | `backupCorrupt`（改名 .bad-时间戳留证）+ `writeFileAtomic`（tmp→Rename）应用于 migReadAll/migWriteAll/prefsL/SetPrefs | `TestCorruptFileBackedUp` |
| 3 | 自动清理不看忽略名单 | `runAutoClean` 调 `applyIgnoreFilters(nil, scanner.Scan(rules))` | `TestApplyIgnoreFilters`（已有）+ 过滤函数抽出 `autoCleanCandidates` 供 `TestAutoCleanCandidates` |
| 4a | copyTree 跳过内部链接，校验照样过→删备份丢链接 | `migrate.Preflight` 新增 `findLinks` 检查：源内有 junction/symlink/reparse 整体拒绝并在检查项说明 | `TestMigrateRejectsLinksInside` |
| 4b | 父目录可迁移已迁过的子目录 | `guardMigOK` 查账本：`done!=true` 记录的 src 是 it.Path 后代 → 拒绝（删除路径同样拦截，防止孤儿数据） | `TestGuardMigDescendant` |
| 5 | go() 10 分钟硬超时杀死长迁移 | 进度感知超时：`__aicProg` 推送刷新 `__aicLastProg`，PROG_OPS 按"闲置时间"判超时 | 前端逻辑测试 |
| 6 | WebView2 缓存 234MB 在 C 盘 | `WebViewOptions.DataPath` = `<storeRoot>\webview`；`cleanupOldWebViewData` 检测 EBWebView 标记后删旧 `%APPDATA%\<exe>` | 实机验证 F:\AIClean\webview |
| 7 | "程序运行中"只在扫描时检查 | `ScanItem.RequiresClosed` 带规则进程名；`scanner.ProcsRunning` 导出；`checkStillClosed` 在 deleteItem/cleanItem/pruneItem/migrateItem/migPreflight 执行前复查 | `TestCheckStillClosed` |
| 8 | 普通/提权双实例 + auto-clean 不查 + FindWindow 按标题误命中 | 统一 `Global\AIClean-SingleInstance`（提权重启轮询等锁 ≤8s）；`--auto-clean` 检测到锁→记日志跳过；hwnd 写 `%TEMP%\AIClean-hwnd.txt`，唤起前 IsWindow 校验 | 逻辑审查（单实例需双进程，无法单测） |
| 9 | 迁回需要 C 盘一整份空间 | `migrate.RollbackPreflight`：bak 在且与 dst 等尺寸→快速路径（直接改名不占空间）；跨盘复制前查 freeBytes 拒绝半截迁移；后端 `checkOnly` 参数 + 前端确认框显示空间需求/不足时直接拦截 | `TestRollbackPreflightFast` / `TestRollbackModifiedDst` |
| 10 | 超期残留只在打开残留页时清 | `ops.PurgeExpired` 独立方法；启动时 + runAutoClean 各调一次；List() 仍内联 | `TestPurgeExpiredDirect` |
| 11 | teams-new-cache 标 safe 会掉登录态 | risk 改 caution + note 写明需重新登录/设置可能重置；caution 不进自动清理 | `TestEmbeddedRulesValid`（generic） |
| 12 | loadRules 查 exe 上级目录会误读 | 只查 exe 同目录 | `TestLoadRulesEmbeddedFallback` |
| 13 | tMainStore 建真实 LOCALAPPDATA 目录 | 改 `ops.NewStoreAt(t.TempDir())` | 全部使用处 |
| 14 | ops.jsonl 无上限 + Logs O(n²) | logMaxBytes/logKeepN var（2MB/5000行）轮转；Logs 正序读完整体反转 | `TestLogRotation` |

验证：go test ./... 全绿 / 前端 55 断言 / 三副本哈希一致 / exe 重建 4.5MB / 实机确认 AIClean-root.txt=F:\AIClean + webview 在 F 盘 + 旧 WebView 缓存目录已不在

## 第八轮：用户复审 6 项（2025-11-15）

| # | 问题 | 修法 | 回归测试 |
|---|------|------|----------|
| 1 | 迁回快速路径只比总字节/文件数——同尺寸改写（SQLite/聊天库原地更新）会丢数据 | `dirManifest` 逐文件清单（相对路径→[size,mtime]），`sameContent` 精确比对；Rollback 和 RollbackPreflight 共用。有一个文件对不上就走正常复制回迁 | `TestRollbackSameSizeEditNotLost`（同尺寸改写→迁回后新内容在）+ `TestManifestDetectsSameSizeEdit` |
| 2 | 指向盘不在时悄悄换盘并覆盖指向文件 | 盘不在→临时回退 %LOCALAPPDATA%\AIClean，指向文件原样保留，`storePinnedMissing` 记原目录；getState 暴露 pinnedMissing，前端顶部警示条"数据目录所在的盘未连接"；盘回来自动恢复，`mergeTreeAny` 把回退期数据并回（ops.jsonl 追加不丢） | `TestStoreRootDeadDriveKeepsPointer` + `TestStoreRootLiveDriveUsesPinned` + `TestMergeTreeAnyOpsAppend` |
| 3a | 单个大文件复制期间无进度→10 分钟超时 | `copyFile` 8MB 块循环里每块 `Progress(done,total)` 上报；前端 150ms 节流推 `__aicProg('migrate')` | 代码审查+构建 |
| 3b | delete/rollback/findBigFiles/prune/finishMigration 无进度→仍硬超时 | `startHeartbeat()` 每 2s 推 `__aicProg('busy')`，9 个入口全接（含 autoCleanNow）；Rollback 跨盘复制也接字节进度并归零 copyDone/copyTotal | `TestRollbackSameSizeEditNotLost` 覆盖 |
| 3c | dirStat/findLinks 预检无心跳 | migPreflight/migrateItem 函数级心跳覆盖预检阶段 | 同上 |
| 4 | 界面常开→定时清理永远跳过 | 命名事件 `Global\AIClean-AutoClean`：headless `--auto-clean` 发现锁被占→SetEvent 通知界面（记日志"已通知界面代为执行"）；界面 `watchAutoCleanEvent` 收到→`queuedAutoClean` 等 scanning/migMu 空闲后执行，日志"由界面代为执行"，完毕 `__aicAutoDone` 刷新前端 | 逻辑审查（跨进程需实测，已留日志可验） |
| 5 | "父::h" 启发式子项没继承 RequiresClosed，执行前复查被跳过 | `ch.RequiresClosed = it.RequiresClosed`（scanner.go L374） | 扫描回归测试既有覆盖 |
| 6 | aiclean/nul 垃圾文件 | 已删除（Windows 保留名，fd/dir 双确认不存在） | — |

验证：go test ./... 6 包全绿（新增 4 个测试）/ 前端 55 断言 / repack 394794B / 三副本哈希 7eca24c0 一致 / release=AIClean.exe(4.5MB)+rules.json 无残留

## 第九轮：断盘账本丢失 + 提权事件失败（2025-11-15）

| # | 问题 | 修法 | 回归测试 |
|---|------|------|----------|
| 1a | 数据盘未连接期间仍可迁移——盘回来后账本"目标优先"规则覆盖临时目录记录 | `pinnedStoreBlocked()`：`storePinnedMissing` 非空时 migPreflight/migrateItem/finishMigration/rollbackMigration 四入口统一拒绝，提示"数据目录所在的 X 盘未连接"；前端行按钮（迁回/搬到其他盘/迁移/删除备份）和批量迁移按钮全部 `disabled` | `TestMigrateBlockedWhenPinnedMissing`（四入口+文案断言） |
| 1b | 兜底：两边账本都存在时 mergeTreeAny 丢弃回退目录记录 | `mergeMigrationsFile`：按 `src+bak` 键并集去重，目标记录在前、回退记录追加；原子写回 | `TestMergeMigrationsUnion`（各侧独有+重复记录→并集3条） |
| 2 | 管理员界面创建的事件带高完整性标签，普通计划任务 OpenEvent 被拒→定时清理静默跳过 | `lowIntegritySA()`：SDDL `D:(A;;0x1F0003;;;WD)S:(ML;;NW;;;LW)` 转 SECURITY_ATTRIBUTES；`watchAutoCleanEvent` 创建事件挂低完整性 SD；同一 SA 也挂到单实例互斥锁（否则普通进程探测不到提权实例占锁，ACCESS_DENIED 亦按"已存在"处理），跨完整性单实例/通知双通 | `TestLowIntegritySA`（转换+句柄断言）；跨进程链路实机待验 |

验证：go test ./... 全绿（新增 3 测试）/ 前端 55 断言 / repack 395137B / 三副本 7eca24c0 一致 / release=AIClean.exe(4.5MB)+rules.json

## 第九轮补遗（2025-11-15）

- `securityAttributes.Length` 写死 12（32 位值）→ `uint32(unsafe.Sizeof(...))`，64 位下正确为 24
- `TestLowIntegrityNamedObjects`：随机名命名事件 Create(带低完整性SA)→OpenEventW(EVENT_MODIFY_STATE)→SetEvent→WaitForSingleObject==0 全链路断言 + CreateMutexW 同 SA 创建——验证"提权界面+普通计划任务"跨完整性通信真实可用

## 第十轮：发布配套 4 项（2025-11-15）

| # | 功能 | 改法 | 测试 |
|---|------|------|------|
| 1 | 彻底移除+残留自检 | `uninstallCheck` 前置检查（`splitUninstallState` 纯函数：账本 done!=true→已迁移项，bak 在盘→待决备份）；`uninstallRun` 删计划任务/自启/残留区/数据目录/指向文件/hwnd 文件，webview 用 `cmd /c ping -n4 & rd` 延迟删；前端设置页底部危险区入口 + 已迁移/备份三选对话框 + 按住确认 + `quitApp` 退出。启动自检 `fixStartupDrift`：reg query + schtasks /XML 读登记路径，`exePathDrift` 与 os.Executable() 不一致→按 prefs 排程重建并记日志 | `TestSplitUninstallState`（有迁移/无迁移/bak 缺失三分支）`TestExePathDrift` `TestRegRunValueAndXMLCommand` |
| 2 | 崩溃日志 | `writeCrash`：时间/版本/Windows版本(RtlGetVersion)/admin/args/panic/debug.Stack 追加写 <store>\logs\crash.log（store 不可用→%TEMP%\AIClean-crash.log），>1MB 留尾 200KB；`crashNote` defer 挂进 main(含 MessageBox+Exit2)/winDrag/watchAutoCleanEvent/queuedAutoClean/heartbeat/quitApp/桥接 dispatch/evalJS 两处 recover；设置页"打开日志目录"→`openLogsDir` | `TestWriteCrash`（故意 panic 断言版本号+堆栈）`TestWriteCrashTruncation` |
| 3 | 版本单一来源 | `var Version="dev"`+`-X main.Version=` 注入；`VERSION` 文件；`build.ps1` 全流程（VERSION→versioninfo.json→goversioninfo→pack→build→release）；启动日志记 `v<ver>`；README 构建说明更新 | `TestVersionFilesAligned`（VERSION↔versioninfo.json 一致性） |
| 4 | 首次启动说明 | `70-welcome.js`：shouldShowWelcome/welcomeBody/welcomeDlg（勾选"我已了解"才能开始，写 prefs.welcomeAcked=版本号）；90-init 扫描后弹；关于页"查看使用说明"可重看（manual 模式免勾选）；自动清理保存计划时 freq!=off 弹确认框说明清理范围 | 前端 `welcome 无记录要弹/已确认不弹/内容完整/无emoji` 4 断言 |

验证：go test ./... 6 包全绿（新增 8 测试）/ 前端 60 断言 / build.ps1 全管线出包 AIClean.exe 4.56MB / FileVersion=ProductVersion=1.0.0.0 / 三副本 rules 哈希 7eca24c0 一致
限制：彻底移除端到端需真实卸载场景验证（建议新 Windows 用户下跑）；welcomeAcked 已在本机 prefs 写入后无法再触发（可用测试账号或删 prefs 复验）

## 第十一轮：彻底移除/启动自检修缺（2025-11-15）

| # | 问题 | 修法 | 测试 |
|---|------|------|------|
| 1 | 卸载删备份无校验：dst 丢失时删掉唯一副本 | `checkBakDeletable`（finishMigration 抽出共用）：validateBak + junction 指向账本 dst + dst 存在，三重通过才删；`uninstallDeleteBaks` 返回 skipped 列表，前端"已移除"弹窗逐条展示原因 | `TestUninstallKeepsBakWhenDstMissing` `TestUninstallDeletesValidBak` |
| 2 | 延迟删除从未生效：exec.Command 参数把内嵌引号转成 \"，cmd 不认 | `delayedRmDir(dir, emptyParent)`：`SysProcAttr.CmdLine` 整串传 `cmd /C ping…& rd /s /q "dir" & rd "父"`，HideWindow 隐藏窗口；父目录空了顺手删 | `TestDelayedRmDir`（中文+空格路径，轮询确认消失） |
| 3 | GBK 编码导致中文路径自检每次误判重建 | a) 自启改 `x/sys/windows/registry` API（Unicode，无编码问题）；b) 计划任务和 prefs.schedExe 比（schedSet 成功时登记，`schedNeedsRebuild` 纯函数判定），不再解析 reg/schtasks 的 ANSI 输出 | `TestSchedNeedsRebuild`（5 分支）`TestExePathDrift`（补中文+空格+引号用例） |
| 4 | 断盘期间可卸载→迁移记录无法检查 | `storePinnedMissing` 非空时 uninstallCheck/uninstallRun 拒绝（提示连接后再移除）；前端按钮置灰+提示 | `TestUninstallBlockedWhenPinnedMissing` |
| 5 | 保留的迁移/备份没有清单 | `writeKeptDataList` → 桌面 `AIClean-保留的迁移数据.txt`：原位置/数据位置/备份路径/大小+手动处理说明；"已移除"弹窗显示文件位置 | `TestWriteKeptDataList` |

验证：go test ./... 6 包全绿（新增 6 测试）/ 前端 60 断言 / build.ps1 出包 AIClean.exe 4.58MB / 三副本哈希一致
限制：彻底移除端到端仍需测试账号验证；延迟删除已用真实 cmd 在含中文+空格的临时目录验证生效

## 第十一轮补遗：保留清单健壮性（2025-11-15）

| # | 问题 | 修法 | 测试 |
|---|------|------|------|
| 1 | 清单写 %USERPROFILE%\Desktop：OneDrive 接管/桌面改位置时不存在 | `windows.KnownFolderPath(FOLDERID_Desktop)` 取系统真实位置；候选链 桌面→文档(FOLDERID_Documents)→exe 目录，`knownFolderPath`/`exeParentDir` 可注入 | `TestWriteKeptDataList`（桌面失败→落文档/桌面优先/全失败报错/空清单不写 四分支） |
| 2 | 清单写失败时仍继续删账本 | uninstallRun 重排：算保留项（含 planBakDeletion 预校验的 skipped）→ 写清单失败→`ok:false` "无法生成保留数据清单，已取消移除" 提前返回 → 之后才碰计划任务/自启/备份/数据目录 | `TestUninstallAbortsWhenListWriteFails`（注入三处全失败→ok:false+账本仍在） |
| — | planBakDeletion 抽出"只校验不删" | 卸载写清单时先算清谁会留（与 finishMigration 同套 checkBakDeletable），清单内容和实际删除结果一致 | 同上链路覆盖 |

验证：go test ./... 6 包全绿 / build.ps1 出包 4.59MB / release 干净（exe+rules）/ 三副本哈希 7eca24c0 一致
