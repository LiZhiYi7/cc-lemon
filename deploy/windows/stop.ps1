# 阳和财务系统 · 停止服务
# 用法：stop.ps1 [-Mode demo|prod|all]

[CmdletBinding()]
param(
    [ValidateSet('demo', 'prod', 'all')][string]$Mode = 'all'
)

$ErrorActionPreference = 'Stop'

$Root = Split-Path -Parent $PSScriptRoot
if ($env:YANGHE_FINANCE_ROOT) { $Root = $env:YANGHE_FINANCE_ROOT }

. (Join-Path $PSScriptRoot 'lib\common.ps1')
. (Join-Path $PSScriptRoot 'lib\pg.ps1')
. (Join-Path $PSScriptRoot 'lib\app.ps1')

$targets = @('demo', 'prod')
if ($Mode -ne 'all') { $targets = @($Mode) }

try {
    Write-Host ''
    Write-Host '============================================================' -ForegroundColor DarkCyan
    Write-Host '  阳和财务系统 · 停止服务' -ForegroundColor White
    Write-Host '============================================================' -ForegroundColor DarkCyan

    $stoppedAnything = $false

    foreach ($m in $targets) {
        $Paths = Get-YanghePaths -Root $Root -Mode $m
        if (-not (Test-Path -LiteralPath $Paths.Root)) { continue }

        $appPid = Get-YanghePid -Paths $Paths
        $pgRunning = Test-PgRunning -Paths $Paths

        if ($appPid -eq 0 -and -not $pgRunning) { continue }

        Write-Host ''
        Write-Host ('[{0}]' -f $Paths.Mode.Label) -ForegroundColor Cyan
        Stop-YangheApp -Paths $Paths -External | Out-Null
        Stop-PgCluster -Paths $Paths
        $stoppedAnything = $true
    }

    Write-Host ''
    if ($stoppedAnything) {
        Write-Ok '已全部停止'
    } else {
        Write-Info '没有正在运行的服务。'
    }
    Write-Host ''
} catch {
    Write-Host ''
    Write-Fail $_.Exception.Message
    exit 1
}
