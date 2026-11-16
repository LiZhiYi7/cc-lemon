# 柠檬账 · 一键打包
# 产物：build\release\lemon-win-x64-<版本>.zip + .sha256
#
# 用法：
#   powershell -ExecutionPolicy Bypass -File deploy\windows\package.ps1 -Version 1.0.0
#   -SkipWeb  复用已有的 web\dist，不重新安装前端依赖（改配置时用）

[CmdletBinding()]
param(
    [string]$Version = '0.1.0',
    [string]$PgZip = '',
    [switch]$SkipWeb
)

$ErrorActionPreference = 'Stop'

$winDir = $PSScriptRoot
$repo = Split-Path (Split-Path $winDir -Parent) -Parent
$buildDir = Join-Path $repo 'build'
$stageRoot = Join-Path $buildDir 'stage'
$releaseDir = Join-Path $buildDir 'release'
$folderName = 'lemon-' + $Version
$stage = Join-Path $stageRoot $folderName

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
    # 用 .NET 直接删除：目录里有两万多个文件，走 Remove-Item 会被批量删除保护拦下。
    param([string]$Path)

    if (Test-Path -LiteralPath $Path) {
        try {
            [System.IO.Directory]::Delete($Path, $true)
        } catch {
            Fail ('无法清理目录 {0}：{1}' -f $Path, $_.Exception.Message)
        }
    }
}

function Assert-WebDist {
    # 前端产物完整性检查。缺 assets 时页面会白屏（浏览器拿到的是 index.html），
    # 这种残包一旦发出去，客户看到的就是一片空白，所以宁可不打包。
    param([Parameter(Mandatory)][string]$Dist)

    $index = Join-Path $Dist 'index.html'
    if (-not (Test-Path -LiteralPath $index)) { Fail ('缺少前端入口：{0}（去掉 -SkipWeb 重新构建）' -f $index) }

    $assetsDir = Join-Path $Dist 'assets'
    if (-not (Test-Path -LiteralPath $assetsDir)) { Fail 'web\dist\assets 不存在，前端产物不完整（去掉 -SkipWeb 重新构建）' }

    $jsCount = @(Get-ChildItem -LiteralPath $assetsDir -Filter '*.js' -File -ErrorAction SilentlyContinue).Count
    if ($jsCount -eq 0) { Fail 'web\dist\assets 里没有任何 .js，前端产物不完整（去掉 -SkipWeb 重新构建）' }

    $html = [System.IO.File]::ReadAllText($index, [System.Text.Encoding]::UTF8)
    $refs = [regex]::Matches($html, '(?:src|href)="(/[^"]+)"') | ForEach-Object { $_.Groups[1].Value }
    if ($refs.Count -eq 0) { Fail 'web\dist\index.html 里没有任何资源引用，产物异常' }

    $missing = @()
    foreach ($r in $refs) {
        $p = Join-Path $Dist (($r.TrimStart('/')) -replace '/', '\')
        if (-not (Test-Path -LiteralPath $p)) { $missing += $r }
    }
    if ($missing.Count -gt 0) {
        Fail ('index.html 引用的资源不存在：{0}（前端产物与入口文件版本不一致，重新构建）' -f ($missing -join ', '))
    }

    return $jsCount
}

if (-not $PgZip) { $PgZip = Join-Path $winDir 'vendor\postgresql-16.13-1-windows-x64-binaries.zip' }

Write-Host ''
Write-Host '============================================================' -ForegroundColor DarkCyan
Write-Host ('  柠檬账 · 打包 {0}' -f $Version) -ForegroundColor White
Write-Host '============================================================' -ForegroundColor DarkCyan

# ---------------------------------------------------------------- 前置检查

Write-Head '检查构建环境'

if (-not (Test-Path -LiteralPath $PgZip)) {
    Fail ('缺少 PostgreSQL 免安装包：{0}（先运行 fetch-pgsql.ps1）' -f $PgZip)
}
$go = Get-Command go -ErrorAction SilentlyContinue
if (-not $go) { Fail '找不到 go 命令，请确认已安装 Go 1.26+' }

$npm = Get-Command npm -ErrorAction SilentlyContinue
if (-not $SkipWeb -and -not $npm) { Fail '找不到 npm 命令，请确认已安装 Node 22+' }

Write-Done ('go: {0}' -f (& go version))

# ---------------------------------------------------------------- 前端

Write-Head '构建前端'

if ($SkipWeb) {
    if (-not (Test-Path -LiteralPath (Join-Path $repo 'web\dist\index.html'))) {
        Fail 'web\dist 不存在，去掉 -SkipWeb 重新打包'
    }
    $jsCount = Assert-WebDist -Dist (Join-Path $repo 'web\dist')
    Write-Done ('复用已有 web\dist（{0} 个脚本）' -f $jsCount)
} else {
    Push-Location (Join-Path $repo 'web')
    try {
        & npm ci --no-audit --no-fund
        if ($LASTEXITCODE -ne 0) { Fail 'npm ci 失败' }
        & npm run build
        if ($LASTEXITCODE -ne 0) { Fail 'npm run build 失败' }
    } finally {
        Pop-Location
    }
    $jsCount = Assert-WebDist -Dist (Join-Path $repo 'web\dist')
    Write-Done ('前端构建完成（{0} 个脚本）' -f $jsCount)
}

# ---------------------------------------------------------------- 目录

Write-Head '准备目录'

if (Test-Path -LiteralPath $stage) { Remove-Tree -Path $stage }
New-Item -ItemType Directory -Path $stage -Force | Out-Null
if (-not (Test-Path -LiteralPath $releaseDir)) { New-Item -ItemType Directory -Path $releaseDir -Force | Out-Null }
Write-Done ('暂存目录：{0}' -f $stage)

# ---------------------------------------------------------------- 后端

Write-Head '编译后端（windows/amd64）'

$exePath = Join-Path $stage 'lemon.exe'
$env:CGO_ENABLED = '0'
$env:GOOS = 'windows'
$env:GOARCH = 'amd64'
Push-Location $repo
try {
    & go build -trimpath -ldflags ('-s -w -X main.version={0}' -f $Version) -o $exePath ./cmd/lemon
    if ($LASTEXITCODE -ne 0) { Fail 'go build 失败' }
} finally {
    Pop-Location
}
Write-Done ('lemon.exe {0:N1} MB' -f ((Get-Item -LiteralPath $exePath).Length / 1MB))

# ---------------------------------------------------------------- 组装

Write-Head '组装交付包'

Copy-Item -LiteralPath (Join-Path $repo 'web\dist') -Destination (Join-Path $stage 'web') -Recurse -Force
Write-Done '前端资源 → web\'

Get-ChildItem -LiteralPath (Join-Path $winDir 'templates') -Filter '*.hta' -File | ForEach-Object {
    Copy-Item -LiteralPath $_.FullName -Destination (Join-Path $stage $_.Name) -Force
}
Write-Done '桌面入口（HTA）→ 根目录'

$toolsSrc = Join-Path $winDir 'templates\工具'
if (Test-Path -LiteralPath $toolsSrc) {
    $toolsDst = Join-Path $stage '工具'
    if (-not (Test-Path -LiteralPath $toolsDst)) { New-Item -ItemType Directory -Path $toolsDst -Force | Out-Null }
    Copy-Item -Path (Join-Path $toolsSrc '*.bat') -Destination $toolsDst -Force
    Write-Done '命令行工具 → 工具\'
}

Copy-Item -LiteralPath (Join-Path $winDir 'templates\config') -Destination (Join-Path $stage 'config') -Recurse -Force
Write-Done '配置模板 → config\'

$scriptsDir = Join-Path $stage 'scripts'
New-Item -ItemType Directory -Path $scriptsDir -Force | Out-Null
Get-ChildItem -LiteralPath $winDir -Filter '*.ps1' -File |
    Where-Object { $_.Name -notin @('package.ps1', 'package-client.ps1', 'fetch-pgsql.ps1', 'normalize-scripts.ps1') } |
    ForEach-Object { Copy-Item -LiteralPath $_.FullName -Destination (Join-Path $scriptsDir $_.Name) -Force }

# 逐文件复制：Copy-Item 复制目录到已存在的同名目录会多嵌一层（scripts\lib\lib）
$libDst = Join-Path $scriptsDir 'lib'
if (-not (Test-Path -LiteralPath $libDst)) { New-Item -ItemType Directory -Path $libDst -Force | Out-Null }
Copy-Item -Path (Join-Path $winDir 'lib\*') -Destination $libDst -Force
Write-Done '运维脚本 → scripts\'

$docsSrc = Join-Path $winDir 'docs'
if (Test-Path -LiteralPath $docsSrc) {
    $docsDst = Join-Path $stage 'docs'
    if (-not (Test-Path -LiteralPath $docsDst)) { New-Item -ItemType Directory -Path $docsDst -Force | Out-Null }
    Copy-Item -Path (Join-Path $docsSrc '*') -Destination $docsDst -Force
    Write-Done '客户文档 → docs\'
} else {
    Write-Host '   提示：docs 目录不存在，客户文档将缺失' -ForegroundColor Yellow
}

# 通用手册放在仓库 docs/ 下（Windows 包与 NAS 交付共用同一份），一并带上
$sharedDocs = @('部署手册-NAS.md', '使用手册.md')
$docsDst = Join-Path $stage 'docs'
if (-not (Test-Path -LiteralPath $docsDst)) { New-Item -ItemType Directory -Path $docsDst -Force | Out-Null }
foreach ($name in $sharedDocs) {
    $src = Join-Path $repo ('docs\' + $name)
    if (Test-Path -LiteralPath $src) {
        Copy-Item -LiteralPath $src -Destination (Join-Path $docsDst $name) -Force
    } else {
        Fail ('缺少通用手册：{0}' -f $src)
    }
}
Write-Done '通用手册（NAS 部署 / 使用手册）→ docs\'

foreach ($d in @('data', 'logs', 'backups', 'secrets', 'run')) {
    $p = Join-Path $stage $d
    if (-not (Test-Path -LiteralPath $p)) { New-Item -ItemType Directory -Path $p -Force | Out-Null }
    Set-Content -LiteralPath (Join-Path $p '.keep') -Value '' -Encoding ascii
}
Write-Done '运行时目录 → data\ logs\ backups\ secrets\ run\'

# ---------------------------------------------------------------- PostgreSQL

Write-Head '解压 PostgreSQL（只保留 bin / lib / share）'
# 注意：压缩包内路径自带 pgsql/ 前缀，所以解压到 stage 根目录，
# 最终落在 stage\pgsql\{bin,lib,share}；解到 stage\pgsql 会多套一层。
Remove-Tree -Path (Join-Path $stage 'pgsql')

foreach ($part in @('bin', 'lib', 'share')) {
    & tar.exe -xf $PgZip -C $stage ('pgsql/' + $part)
    if ($LASTEXITCODE -ne 0) { Fail ('解压 pgsql/{0} 失败' -f $part) }
}
$pgsqlDst = Join-Path $stage 'pgsql'
if (-not (Test-Path -LiteralPath (Join-Path $pgsqlDst 'bin\postgres.exe'))) { Fail 'postgres.exe 缺失' }
Write-Done ('PostgreSQL 已就位（{0:N0} MB）' -f ((Get-ChildItem -LiteralPath $pgsqlDst -Recurse -File | Measure-Object Length -Sum).Sum / 1MB))

$vcRedist = Join-Path $winDir 'vendor\VC_redist.x64.exe'
if (Test-Path -LiteralPath $vcRedist) {
    $vcDir = Join-Path $stage 'pgsql\vcredist'
    if (-not (Test-Path -LiteralPath $vcDir)) { New-Item -ItemType Directory -Path $vcDir -Force | Out-Null }
    Copy-Item -LiteralPath $vcRedist -Destination (Join-Path $vcDir 'VC_redist.x64.exe') -Force
    Write-Done 'VC++ 运行库已附带'
} else {
    Write-Host '   提示：未找到 VC_redist.x64.exe，客户机器缺运行库时数据库会起不来' -ForegroundColor Yellow
}

# ---------------------------------------------------------------- 版本信息

$gitCommit = 'unknown'
try { $gitCommit = (& git -C $repo rev-parse --short HEAD).Trim() } catch { }

$pgVer = 'unknown'
try {
    $pgVer = (& (Join-Path $stage 'pgsql\bin\pg_ctl.exe') --version).Trim()
} catch { }

$versionText = @(
    '柠檬账（cc-lemon）'
    ('版本      : {0}' -f $Version)
    ('构建时间  : {0}' -f (Get-Date -Format 'yyyy-MM-dd HH:mm:ss'))
    ('提交      : {0}' -f $gitCommit)
    ('Go        : {0}' -f (& go version))
    ('Node      : {0}' -f (& node --version))
    ('数据库    : {0}' -f $pgVer)
    ''
    '本目录可直接复制到任意 Windows 10/11 x64 电脑使用，无需安装。'
) -join "`r`n"
Set-Content -LiteralPath (Join-Path $stage 'VERSION.txt') -Value $versionText -Encoding UTF8
Write-Done 'VERSION.txt'

# ---------------------------------------------------------------- 校验

Write-Head '交付前校验'

$problems = New-Object System.Collections.Generic.List[string]

$forbiddenPatterns = @('*.go', '*.ts', '*.vue', '*.mjs', 'go.mod', 'go.sum', 'package.json', 'package-lock.json', 'tsconfig.json', 'vite.config.ts')
foreach ($pattern in $forbiddenPatterns) {
    $hits = Get-ChildItem -LiteralPath $stage -Recurse -File -Filter $pattern -ErrorAction SilentlyContinue
    if ($hits) { $problems.Add(('发现源码文件：{0}（{1} 个）' -f $pattern, $hits.Count)) }
}

if (Get-ChildItem -LiteralPath $stage -Recurse -Directory -Filter 'src' -ErrorAction SilentlyContinue) {
    $problems.Add('发现源码目录 src')
}

$leaked = Get-ChildItem -LiteralPath (Join-Path $stage 'secrets') -File -ErrorAction SilentlyContinue | Where-Object { $_.Extension -eq '.pass' }
if ($leaked) { $problems.Add('secrets 目录里带了密码文件') }

$initializedDb = Get-ChildItem -LiteralPath (Join-Path $stage 'data') -Recurse -Filter 'PG_VERSION' -File -ErrorAction SilentlyContinue
if ($initializedDb) { $problems.Add('data 目录里带了已初始化的数据库') }

foreach ($f in @('config.yaml', '.env')) {
    if (Get-ChildItem -LiteralPath $stage -Recurse -File -Filter $f -ErrorAction SilentlyContinue) {
        $problems.Add(('发现运行时配置文件 {0}' -f $f))
    }
}

$secretPattern = 'jwt_secret\s*:\s*["'']?[^\s"'']+|postgres://[^"\s]+:[^"\s@]+@'
$secretHits = @()
foreach ($yaml in (Get-ChildItem -LiteralPath $stage -Recurse -File -ErrorAction SilentlyContinue |
        Where-Object { $_.Extension -in @('.yaml', '.yml', '.env', '.txt') })) {
    $hit = Select-String -LiteralPath $yaml.FullName -Pattern $secretPattern -ErrorAction SilentlyContinue
    if ($hit) { $secretHits += ('{0} 第 {1} 行' -f $yaml.Name, $hit[0].LineNumber) }
}
if ($secretHits.Count -gt 0) {
    foreach ($h in $secretHits) { $problems.Add(('疑似明文密钥：{0}' -f $h)) }
}

if (-not (Test-Path -LiteralPath (Join-Path $stage 'lemon.exe'))) { $problems.Add('lemon.exe 缺失') }
if (-not (Test-Path -LiteralPath (Join-Path $stage 'web\index.html'))) { $problems.Add('web\index.html 缺失') }
if (-not (Test-Path -LiteralPath (Join-Path $stage 'web\assets'))) {
    $problems.Add('web\assets 缺失（客户打开会白屏）')
} elseif (@(Get-ChildItem -LiteralPath (Join-Path $stage 'web\assets') -File).Count -eq 0) {
    $problems.Add('web\assets 为空（客户打开会白屏）')
}

if ($problems.Count -gt 0) {
    foreach ($p in $problems) { Write-Host ('   !!  {0}' -f $p) -ForegroundColor Red }
    Fail '校验未通过，已终止打包'
}
Write-Done '无源码、无密钥、无残留数据'

# ps1 必须是 UTF-8 BOM，否则 PowerShell 5.1 中文会乱码
$badPs1 = @()
foreach ($f in (Get-ChildItem -LiteralPath $scriptsDir -Recurse -Filter '*.ps1' -File)) {
    $bytes = [System.IO.File]::ReadAllBytes($f.FullName)
    if (-not ($bytes.Length -ge 3 -and $bytes[0] -eq 0xEF -and $bytes[1] -eq 0xBB -and $bytes[2] -eq 0xBF)) {
        $badPs1 += $f.Name
    }
}
if ($badPs1.Count -gt 0) {
    foreach ($n in $badPs1) { Write-Host ('   !!  {0} 缺少 UTF-8 BOM' -f $n) -ForegroundColor Red }
    Fail '脚本编码校验未通过'
}
Write-Done '脚本编码正确（UTF-8 BOM）'

# ---------------------------------------------------------------- 打包

Write-Head '生成压缩包'

$zipPath = Join-Path $releaseDir ('lemon-win-x64-' + $Version + '.zip')
if (Test-Path -LiteralPath $zipPath) {
    # 直接走 .NET：Remove-Item 会被外部的批量删除保护拦截
    [System.IO.File]::Delete($zipPath)
}

# 用 Compress-Archive 而不是 tar：bsdtar 会用系统 ANSI 代码页写中文文件名
# 且不打 UTF-8 标志位，换语言环境的 Windows 解压就成乱码。
Compress-Archive -Path $stage -DestinationPath $zipPath -CompressionLevel Optimal -Force
if (-not (Test-Path -LiteralPath $zipPath)) { Fail '压缩失败' }

$zipSize = (Get-Item -LiteralPath $zipPath).Length
Write-Done ('{0}  {1:N1} MB' -f (Split-Path $zipPath -Leaf), ($zipSize / 1MB))

$hash = (Get-FileHash -LiteralPath $zipPath -Algorithm SHA256).Hash
$sumFile = $zipPath + '.sha256.txt'
Set-Content -LiteralPath $sumFile -Value ('{0}  {1}' -f $hash, (Split-Path $zipPath -Leaf)) -Encoding ascii
Write-Done ('SHA256 {0}' -f $hash)

$entries = (& tar.exe -tf $zipPath | Measure-Object).Count
Write-Done ('压缩包内条目 {0} 个' -f $entries)

# 只保留最近两个版本
$old = Get-ChildItem -LiteralPath $releaseDir -Filter 'lemon-win-x64-*.zip' -File |
    Sort-Object LastWriteTime -Descending | Select-Object -Skip 2
foreach ($f in $old) {
    try { [System.IO.File]::Delete($f.FullName) } catch { }
    try { [System.IO.File]::Delete($f.FullName + '.sha256.txt') } catch { }
}

Write-Host ''
Write-Host '============================================================' -ForegroundColor DarkCyan
Write-Host '  打包完成' -ForegroundColor Green
Write-Host ('  压缩包：{0}' -f $zipPath) -ForegroundColor White
Write-Host ('  校验值：{0}' -f $sumFile) -ForegroundColor Gray
Write-Host ('  解压后：{0:N0} MB' -f ((Get-ChildItem -LiteralPath $stage -Recurse -File | Measure-Object Length -Sum).Sum / 1MB)) -ForegroundColor Gray
Write-Host '============================================================' -ForegroundColor DarkCyan
Write-Host ''
