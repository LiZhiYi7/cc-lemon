# 柠檬账 · 立即备份
# 用法：backup.ps1 [-Mode prod] [-KeepDays 30]
# 备份为 pg_dump 自定义格式（.dump），先写 .partial 再改名，避免半截文件被误认。

[CmdletBinding()]
param(
    [ValidateSet('demo', 'prod')][string]$Mode = 'prod',
    [int]$KeepDays = 30
)

$ErrorActionPreference = 'Stop'

$Root = Split-Path -Parent $PSScriptRoot
if ($env:LEMON_ROOT) { $Root = $env:LEMON_ROOT }

. (Join-Path $PSScriptRoot 'lib\common.ps1')
. (Join-Path $PSScriptRoot 'lib\pg.ps1')

$Paths = Get-LemonPaths -Root $Root -Mode $Mode

try {
    Write-Host ''
    Write-Host '============================================================' -ForegroundColor DarkCyan
    Write-Host ('  柠檬账 · 备份数据（{0}）' -f $Paths.Mode.Label) -ForegroundColor White
    Write-Host '============================================================' -ForegroundColor DarkCyan

    $password = Get-ModePassword -Paths $Paths
    if (-not $password) {
        Write-Fail '数据库尚未初始化，没有可备份的数据。'
        Write-Info '请先双击「1-离线单机运行.bat」或「2-正式使用.bat」完成初始化。'
        exit 1
    }

    $startedHere = $false
    if (-not (Test-PgRunning -Paths $Paths)) {
        Write-Info '数据库当前未运行，临时启动后备份。'
        Start-PgCluster -Paths $Paths
        $startedHere = $true
    }

    $result = Backup-LemonDatabase -Paths $Paths -Password $password -KeepDays $KeepDays

    if ($startedHere) {
        Stop-PgCluster -Paths $Paths
    }

    Write-Host ''
    Write-Ok '备份完成'
    Write-Info ('文件：{0}' -f $result.File)
    Write-Info ('大小：{0:N1} MB' -f ($result.Size / 1MB))
    if ($result.Removed -gt 0) {
        Write-Info ('已清理 {0} 个超过 {1} 天的旧备份' -f $result.Removed, $KeepDays)
    }
    Write-Host ''
    Write-Host '  建议每周把 backups 目录复制到 NAS 或网盘，本机备份不算真正的备份。' -ForegroundColor Yellow
    Write-Host ''
} catch {
    Write-Host ''
    Write-Fail $_.Exception.Message
    exit 1
}
