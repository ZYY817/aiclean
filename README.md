# AIClean — C 盘空间管家

Windows 桌面清理工具（Go + WebView2，单文件 exe）。扫描 C 盘占用，分类成**可清理缓存 / 建议迁移 / 待确认 / 系统级**，安全释放空间。

## 功能

- **整盘扫描**：210+ 条规则库 + 启发式自动发现（Electron 应用缓存）+ ≥300MB 大文件猎手（含重复文件指纹比对）
- **安全清理**：缓存一键清、按龄清理、逐项确认；回收站/休眠文件/更新缓存需单独确认；系统目录需管理员
- **无损迁移**：数据搬去其他盘 + Junction 链接回跳，软件照常使用；可迁回、可清备份
- **残留管理**：删除失败的残留可恢复可再删，超期自动清
- **自动清理**：接入 Windows 计划任务定时静默清缓存；支持开机自启
- **忽略名单**：不关心的项目"不再提示"，可在设置中恢复

## 构建

版本号唯一来源是 `aiclean/VERSION`；发布构建一律走 `build.ps1`（自动同步 versioninfo.json → 重新生成 resource.syso → 打包前端 → `-X main.Version=` 注入 → 输出到 `release/`）：

```powershell
cd aiclean
powershell -ExecutionPolicy Bypass -File build.ps1        # 出 release\AIClean.exe + rules.json
```

手动构建（开发调试用，版本号显示 dev）：

```powershell
cd aiclean
go run pack.go                                             # 打包前端
go build -ldflags="-s -w -X main.Version=dev" -o AIClean.exe .
```

## 测试

```powershell
go test ./...          # 后端全部测试 + 前端逻辑（调 node 跑 web/test/logic.test.js）
```

## 布局

- `release/` —— 发布目录（AIClean.exe + rules.json）
- `aiclean/` —— Go 源码（main.go + internal/{model,scanner,ops,migrate,junction}）
- `aiclean/web/src/` —— 前端源码，经 pack.go 拼成 web/index.html 后 go:embed
- `rules.json` —— 规则库：exe 同目录或上级目录的外部副本优先生效，否则用内嵌副本

作者：智宇丶817（B 站 space.bilibili.com/41173761）
