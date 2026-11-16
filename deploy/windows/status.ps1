# 柠檬账 · 运行状态
# 用法：status.ps1

[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'

$Root = Split-Path -Parent $PSScriptRoot
if ($env:LEMON_ROOT) { $Root = $env:LEMON_ROOT }

. (Join-Path $PSScriptRoot 'lib\common.ps1')
. (Join-Path $PSScriptRoot 'lib\pg.ps1')
. (Join-Path $PSScriptRoot 'firewall.ps1')

function Get-DirSizeMb {
    param([string]$Path)

    if (-not (Test-Path -LiteralPath $Path)) { return 0 }
    try {
        $bytes = (Get-ChildItem -LiteralPath $Path -Recurse -File -ErrorAction SilentlyContinue |
            Measure-Object -Property Length -Sum).Sum
        if (-not $bytes) { return 0 }
        return [math]::Round($bytes / 1MB, 1)
    } catch {
        return 0
    }
}

try {
    Write-Host ''
    Write-Host '============================================================' -ForegroundColor DarkCyan
    Write-Host '  柠檬账 · 运行状态' -ForegroundColor White
    Write-Host '============================================================' -ForegroundColor DarkCyan
    Write-Host ('  目录：{0}' -f $Root) -ForegroundColor Gray
    Write-Host ''

    foreach ($m in @('demo', 'prod')) {
        $Paths = Get-LemonPaths -Root $Root -Mode $m
        $endpoint = Get-AppEndpoint -Paths $Paths

        $appPid = Get-LemonPid -Paths $Paths
        $pgRunning = Test-PgRunning -Paths $Paths

        $health = '-'
        if ($appPid -gt 0) {
            $code = Get-HttpStatus -Url ($endpoint.LocalUrl + '/healthz') -TimeoutSeconds 2
            if ($code -eq 200) { $health = '200' } elseif ($code -eq 0) { $health = '无响应' } else { $health = [string]$code }
        }

        $color = 'Gray'
        if ($appPid -gt 0) { $color = 'Green' }

        Write-Host ('[{0}]' -f $Paths.Mode.Label) -ForegroundColor $color
        Write-Host ('  应用端口   {0}   进程 {1}' -f $endpoint.Port, $(if ($appPid -gt 0) { $appPid } else { '未运行' }))
        Write-Host ('  数据库端口 {0}   状态 {1}' -f $Paths.Mode.PgPort, $(if ($pgRunning) { '运行中' } else { '未运行' }))
        Write-Host ('  健康检查   {0}' -f $health)
        Write-Host ('  访问地址   {0}' -f $endpoint.LocalUrl)
        Write-Host ('  数据大小   {0} MB' -f (Get-DirSizeMb -Path $Paths.DataDir))

        if ($m -eq 'prod') {
            $ruleOk = Test-FirewallRule -AppPort $endpoint.Port
            Write-Host ('  防火墙     {0}' -f $(if ($ruleOk) { '已放行 TCP ' + $endpoint.Port } else { '未放行' }))
            $lan = Get-LanIPv4
            if ($lan.Count -gt 0) {
                Write-Host ('  局域网     {0}' -f (($lan | ForEach-Object { 'http://{0}:{1}' -f $_, $endpoint.Port }) -join '  '))
            }
            $backs = @(Get-ChildItem -LiteralPath $Paths.BackupDir -Filter 'lemon-*.dump' -ErrorAction SilentlyContinue |
                Sort-Object LastWriteTime -Descending | Select-Object -First 3)
            if ($backs.Count -gt 0) {
                Write-Host '  最近备份：'
                foreach ($b in $backs) {
                    Write-Host ('    {0}   {1:N1} MB   {2}' -f $b.Name, ($b.Length / 1MB), $b.LastWriteTime.ToString('yyyy-MM-dd HH:mm'))
                }
            } else {
                Write-Host '  最近备份   暂无'
            }
        }

        Write-Host ''
    }

    Write-Host '------------------------------------------------------------' -ForegroundColor DarkCyan
    Write-Host '  1 = 离线单机运行   2 = 正式使用   3 = 停止运行   4 = 立即备份' -ForegroundColor Gray
    Write-Host ''
} catch {
    Write-Host ''
    Write-Fail $_.Exception.Message
    exit 1
}
