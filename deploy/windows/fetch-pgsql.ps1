# 阳和财务系统 · 开发机依赖抓取
# 下载便携版 PostgreSQL 与 VC++ 运行库到 deploy\windows\vendor\，供 package.ps1 使用。
# 只需在首次打包前执行一次。

[CmdletBinding()]
param(
    [string]$PgVersion = '16.13-1',
    [switch]$SkipVcRedist
)

$ErrorActionPreference = 'Stop'

$vendor = Join-Path $PSScriptRoot 'vendor'
if (-not (Test-Path -LiteralPath $vendor)) {
    New-Item -ItemType Directory -Path $vendor -Force | Out-Null
}

function Get-RemoteFile {
    param(
        [Parameter(Mandatory)][string]$Url,
        [Parameter(Mandatory)][string]$Target
    )

    if (Test-Path -LiteralPath $Target) {
        $size = [math]::Round((Get-Item -LiteralPath $Target).Length / 1MB, 1)
        Write-Host ('  已存在，跳过下载：{0}（{1} MB）' -f (Split-Path $Target -Leaf), $size) -ForegroundColor Gray
        return
    }

    Write-Host ('  下载：{0}' -f $Url) -ForegroundColor Cyan
    $tmp = $Target + '.partial'
    $curl = 'curl.exe'
    & $curl -L --fail --retry 3 --retry-delay 2 -o $tmp $Url
    if ($LASTEXITCODE -ne 0) {
        if (Test-Path -LiteralPath $tmp) { Remove-Item -LiteralPath $tmp -Force }
        throw ('下载失败：{0}' -f $Url)
    }
    Move-Item -LiteralPath $tmp -Destination $Target -Force
    $size = [math]::Round((Get-Item -LiteralPath $Target).Length / 1MB, 1)
    Write-Host ('  完成：{0} MB' -f $size) -ForegroundColor Green
}

Write-Host ''
Write-Host '阳和财务系统 · 抓取打包依赖' -ForegroundColor White
Write-Host ''

$pgZip = Join-Path $vendor ('postgresql-{0}-windows-x64-binaries.zip' -f $PgVersion)
Get-RemoteFile -Url ('https://get.enterprisedb.com/postgresql/postgresql-{0}-windows-x64-binaries.zip' -f $PgVersion) -Target $pgZip

if (-not $SkipVcRedist) {
    $vc = Join-Path $vendor 'VC_redist.x64.exe'
    Get-RemoteFile -Url 'https://aka.ms/vs/17/release/vc_redist.x64.exe' -Target $vc
}

Write-Host ''
Write-Host '校验值（可粘贴到交付记录里）：' -ForegroundColor White
Get-ChildItem -LiteralPath $vendor -File | ForEach-Object {
    $hash = (Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash
    Write-Host ('  {0}  {1}' -f $hash, $_.Name)
}
Write-Host ''
