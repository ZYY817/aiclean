# AIClean 发布构建：VERSION 是版本号唯一来源
#   powershell -ExecutionPolicy Bypass -File build.ps1
# 流程：VERSION → versioninfo.json → resource.syso → pack 前端 → ldflags 注入版本 → release/
param([string]$Out = "$PSScriptRoot\..\release")
$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot

$ver = (Get-Content VERSION -Raw).Trim()
if ($ver -notmatch '^\d+\.\d+\.\d+$') { throw "VERSION 需要 x.y.z 格式，当前：$ver" }
$p = $ver.Split('.')

# 1) versioninfo.json 字段同步
$vij = Get-Content versioninfo.json -Raw -Encoding UTF8 | ConvertFrom-Json
foreach ($k in 'FileVersion', 'ProductVersion') {
    $vij.FixedFileInfo.$k.Major = [int]$p[0]
    $vij.FixedFileInfo.$k.Minor = [int]$p[1]
    $vij.FixedFileInfo.$k.Patch = [int]$p[2]
    $vij.FixedFileInfo.$k.Build = 0
}
$vij.StringFileInfo.FileVersion = "$ver.0"
$vij.StringFileInfo.ProductVersion = "$ver.0"
[System.IO.File]::WriteAllText("$PSScriptRoot\versioninfo.json", ($vij | ConvertTo-Json -Depth 6), (New-Object System.Text.UTF8Encoding $false))

# 2) resource.syso + 3) 前端打包
goversioninfo -64 -o resource.syso versioninfo.json
if ($LASTEXITCODE -ne 0) { throw "goversioninfo failed" }
go run pack.go
if ($LASTEXITCODE -ne 0) { throw "pack failed" }

# 4) 编译（版本号注入二进制）+ 5) 复制规则库
New-Item -ItemType Directory -Force $Out | Out-Null
go build -ldflags "-s -w -X main.Version=$ver" -o "$Out\AIClean.exe" .
if ($LASTEXITCODE -ne 0) { throw "go build failed" }
Copy-Item rules.json "$Out\rules.json" -Force
Remove-Item -LiteralPath "$Out\AIClean.exe~" -Force -ErrorAction SilentlyContinue
Get-ChildItem "$Out" -Filter "*.exe~" | Remove-Item -Force
Get-ChildItem $Out | Select-Object Name, Length
Write-Output "AIClean $ver -> $Out"
