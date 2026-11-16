# 柠檬账 · 打包 Windows 客户端（Win7 / Win10 / Win11 通用）
#
# 产物：build\release\lemon-client-<版本>.zip
# 内容：绿色版 Firefox ESR 115（Win7 最后一个可用版本）+ 启动器 + 说明
#
# 用法：
#   powershell -ExecutionPolicy Bypass -File deploy\windows\package-client.ps1 -Version 1.0.0
#
# 前置：把 Firefox ESR 115 的绿色版目录放到 deploy\windows\vendor\firefox-115.45.0\
#       （获取方式见 docs\运维手册.md 的「构建客户端包」一节）

[CmdletBinding()]
param(
    [string]$Version = '0.1.0',
    [string]$FirefoxDir = ''
)

$ErrorActionPreference = 'Stop'

$winDir = $PSScriptRoot
$repo = Split-Path (Split-Path $winDir -Parent) -Parent
$buildDir = Join-Path $repo 'build'
$stageRoot = Join-Path $buildDir 'stage'
$releaseDir = Join-Path $buildDir 'release'
$folderName = 'lemon-client-' + $Version
$stage = Join-Path $stageRoot $folderName

if (-not $FirefoxDir) { $FirefoxDir = Join-Path $winDir 'vendor\firefox-115.45.0' }

function Write-Head {
    param([string]$Text)
    Write-Host ''
    Write-Host ('== {0}' -f $Text) -ForegroundColor Cyan
}

function Write-Done {
    param([string]$Text)
    Write-Host ('   OK  {0}' -f $Text) -ForegroundColor Green
}

function Fail {
    param([string]$Text)
    Write-Host ('   !!  {0}' -f $Text) -ForegroundColor Red
    exit 1
}

function Remove-Tree {
    param([string]$Path)
    if (Test-Path -LiteralPath $Path) { [System.IO.Directory]::Delete($Path, $true) }
}

Write-Host ''
Write-Host '============================================================' -ForegroundColor DarkCyan
Write-Host ('  柠檬账 · 打包客户端 {0}' -f $Version) -ForegroundColor White
Write-Host '============================================================' -ForegroundColor DarkCyan

Write-Head '检查浏览器'
if (-not (Test-Path -LiteralPath (Join-Path $FirefoxDir 'firefox.exe'))) {
    Fail ('找不到 Firefox：{0}\firefox.exe' -f $FirefoxDir)
}
$ffVersion = 'unknown'
$ini = Join-Path $FirefoxDir 'application.ini'
if (Test-Path -LiteralPath $ini) {
    $line = Select-String -LiteralPath $ini -Pattern '^Version=' | Select-Object -First 1
    if ($line) { $ffVersion = ($line.Line -replace '^Version=', '').Trim() }
}
$ffSize = (Get-ChildItem -LiteralPath $FirefoxDir -Recurse -File | Measure-Object Length -Sum).Sum / 1MB
Write-Done ('Firefox ESR {0}（{1:N0} MB）' -f $ffVersion, $ffSize)

Write-Head '准备目录'
Remove-Tree -Path $stage
New-Item -ItemType Directory -Path $stage -Force | Out-Null
if (-not (Test-Path -LiteralPath $releaseDir)) { New-Item -ItemType Directory -Path $releaseDir -Force | Out-Null }

Write-Head '组装'
$ffDst = Join-Path $stage 'firefox'
New-Item -ItemType Directory -Path $ffDst -Force | Out-Null
Copy-Item -Path (Join-Path $FirefoxDir '*') -Destination $ffDst -Recurse -Force

$tpl = Join-Path $winDir 'templates\client'
Copy-Item -Path (Join-Path $tpl '*') -Destination $stage -Recurse -Force

foreach ($f in @('1-打开柠檬账.bat', 'server.ini', '使用说明.txt', 'profile\user.js')) {
    if (-not (Test-Path -LiteralPath (Join-Path $stage $f))) { Fail ('缺少文件：{0}' -f $f) }
}
Write-Done '启动器、配置、说明、浏览器配置已就位'

$versionText = @(
    '柠檬账 · 客户端'
    ('版本      : {0}' -f $Version)
    ('构建时间  : {0}' -f (Get-Date -Format 'yyyy-MM-dd HH:mm:ss'))
    ('内置浏览器: Firefox ESR {0}' -f $ffVersion)
    ''
    '支持的客户端系统：Windows 7 SP1 / 10 / 11（64 位）'
    '用法：修改 server.ini 里的 url，双击 1-打开柠檬账.bat'
) -join "`r`n"
Set-Content -LiteralPath (Join-Path $stage 'VERSION.txt') -Value $versionText -Encoding UTF8
Write-Done 'VERSION.txt'

Write-Head '校验'
if (-not (Test-Path -LiteralPath (Join-Path $stage 'firefox\firefox.exe'))) { Fail 'firefox.exe 缺失' }
$leaked = Get-ChildItem -LiteralPath $stage -Recurse -File -Filter '*.log' -ErrorAction SilentlyContinue
if ($leaked) { Write-Host '   提示：包内存在 .log 文件，已忽略' -ForegroundColor Yellow }
Write-Done '包结构正常'

Write-Head '打包'
$zipPath = Join-Path $releaseDir ('lemon-client-' + $Version + '.zip')
if (Test-Path -LiteralPath $zipPath) { [System.IO.File]::Delete($zipPath) }

# 用 Compress-Archive：bsdtar 写中文文件名不带 UTF-8 标志位，跨语言环境会乱码
Compress-Archive -Path $stage -DestinationPath $zipPath -CompressionLevel Optimal -Force
if (-not (Test-Path -LiteralPath $zipPath)) { Fail '压缩失败' }

$zipSize = (Get-Item -LiteralPath $zipPath).Length
$hash = (Get-FileHash -LiteralPath $zipPath -Algorithm SHA256).Hash
Set-Content -LiteralPath ($zipPath + '.sha256.txt') -Value ('{0}  {1}' -f $hash, (Split-Path $zipPath -Leaf)) -Encoding ascii

Write-Done ('{0}  {1:N1} MB' -f (Split-Path $zipPath -Leaf), ($zipSize / 1MB))
Write-Done ('SHA256 {0}' -f $hash)

Write-Host ''
Write-Host '============================================================' -ForegroundColor DarkCyan
Write-Host ('  客户端包：{0}' -f $zipPath) -ForegroundColor White
Write-Host '  提醒：交付前把 server.ini 里的示例地址改成客户的实际地址' -ForegroundColor Yellow
Write-Host '============================================================' -ForegroundColor DarkCyan
Write-Host ''
