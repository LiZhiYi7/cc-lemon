# 柠檬账 · 启动入口
# 用法：powershell -ExecutionPolicy Bypass -File start.ps1 -Mode demo|prod
#       由根目录的 "1-离线单机运行.bat" / "2-正式使用.bat" 调用，一般不需要手工执行。

[CmdletBinding()]
param(
    [ValidateSet('demo', 'prod')][string]$Mode = 'demo',
    [switch]$NoBrowser
)

$ErrorActionPreference = 'Stop'

$Root = Split-Path -Parent $PSScriptRoot
if ($env:LEMON_ROOT) { $Root = $env:LEMON_ROOT }

. (Join-Path $PSScriptRoot 'lib\common.ps1')
. (Join-Path $PSScriptRoot 'lib\pg.ps1')
. (Join-Path $PSScriptRoot 'lib\lemon.ps1')
. (Join-Path $PSScriptRoot 'firewall.ps1')

$Paths = Get-LemonPaths -Root $Root -Mode $Mode
$Endpoint = Get-AppEndpoint -Paths $Paths

try {
    $Host.UI.RawUI.WindowTitle = ('柠檬账 · {0}' -f $Paths.Mode.Label)

    Write-Host ''
    Write-Host '============================================================' -ForegroundColor DarkCyan
    Write-Host ('  柠檬账 · {0}' -f $Paths.Mode.Label) -ForegroundColor White
    Write-Host '============================================================' -ForegroundColor DarkCyan

    Initialize-LemonDirs -Paths $Paths

    if (-not (Test-LemonPreflight -Paths $Paths)) {
        Write-Host ''
        Write-Fail '环境检查未通过，已停止启动。'
        exit 1
    }

    $runningPid = Get-LemonPid -Paths $Paths
    if ($runningPid -gt 0) {
        Write-Host ''
        Write-Ok ('服务已经在运行（PID {0}）' -f $runningPid)
        Write-Info ('访问地址：{0}' -f $Endpoint.LocalUrl)
        if (-not $NoBrowser) { Open-LemonBrowser -Endpoint $Endpoint | Out-Null }
        Write-Host ''
        Write-Info '如需停止，请双击「3-停止运行.bat」。'
        exit 0
    }

    if (-not (Test-PortOrFail -Port $Endpoint.Port -Role '应用')) {
        exit 1
    }

    if (-not (Test-PgRunning -Paths $Paths)) {
        if (-not (Test-PortOrFail -Port $Paths.Mode.PgPort -Role '数据库')) {
            exit 1
        }
    }

    $password = Get-ModePassword -Paths $Paths -Create

    Initialize-PgCluster -Paths $Paths -Password $password
    Start-PgCluster -Paths $Paths
    Ensure-LemonDatabase -Paths $Paths -Password $password

    if ($Mode -eq 'prod') {
        Assert-FirewallRule -AppPort $Endpoint.Port
    }

    $app = Start-LemonApp -Paths $Paths -Endpoint $Endpoint -Password $password

    Write-Step '等待服务就绪'
    if (-not (Wait-AppReady -Endpoint $Endpoint -Process $app.Process -TimeoutSeconds 120)) {
        Write-Fail '服务未能在 120 秒内就绪。'
        if ($app.Process.HasExited) {
            Write-Fail ('主程序已退出，退出码 {0}' -f $app.Process.ExitCode)
        }
        Show-AppLogTail -App $app -Lines 30
        Write-Host ''
        Write-Info '常见原因：端口被占用、VC++ 运行库缺失、数据库密码不匹配。'
        Write-Info ('详细日志：{0}' -f $app.Log)
        throw '服务启动失败'
    }

    Write-Ok '服务已就绪'
    Write-Host ''
    Write-Host '------------------------------------------------------------' -ForegroundColor DarkCyan
    Write-Host ('  本机访问    {0}' -f $Endpoint.LocalUrl) -ForegroundColor White
    if ($Mode -eq 'prod') {
        $lan = Get-LanIPv4
        if ($lan.Count -gt 0) {
            foreach ($ip in $lan) {
                Write-Host ('  局域网访问  http://{0}:{1}' -f $ip, $Endpoint.Port) -ForegroundColor White
            }
        } else {
            Write-Info '未检测到局域网地址，请确认网络连接。'
        }
        $category = Get-NetworkProfileName
        if ($category -eq 'Public') {
            Write-Warn2 '当前网络被识别为「公用网络」，其他电脑可能无法访问。'
            Write-Info '请在 Windows 设置 → 网络和 Internet 中把本网络改为「专用网络」。'
        }
    }
    Write-Host '------------------------------------------------------------' -ForegroundColor DarkCyan
    Write-Host ''
    Write-Info ('数据目录：{0}' -f $Paths.DataDir)
    Write-Info ('日志文件：{0}' -f $app.Log)
    Write-Host ''
    Write-Host '  首次使用请在浏览器里创建系统管理员账号。' -ForegroundColor Yellow
    Write-Host '  按 Ctrl+C 或关闭本窗口即可停止服务。' -ForegroundColor Yellow
    Write-Host ''

    if (-not $NoBrowser) { Open-LemonBrowser -Endpoint $Endpoint | Out-Null }

    # 跟随应用日志输出，直到进程退出或用户中断
    $seen = 0
    while (-not $app.Process.HasExited) {
        if (Test-Path -LiteralPath $app.Log) {
            $lines = @(Get-Content -LiteralPath $app.Log -ErrorAction SilentlyContinue)
            if ($lines.Count -gt $seen) {
                $lines[$seen..($lines.Count - 1)] | ForEach-Object {
                    Write-Host ('  ' + $_) -ForegroundColor DarkGray
                }
                $seen = $lines.Count
            }
        }
        Start-Sleep -Milliseconds 700
    }

    Write-Host ''
    Write-Warn2 '服务进程已退出。'
    Show-AppLogTail -App $app -Lines 20
} catch {
    Write-Host ''
    Write-Fail $_.Exception.Message
} finally {
    # 无论正常退出还是 Ctrl+C，都要把应用和数据库停干净。
    # 但如果这次退出是外部脚本（停止/恢复）触发的，会话标记已被撤销，
    # 数据库由其接管，这里就不要再停一次。
    try { Stop-LemonApp -Paths $Paths | Out-Null } catch { }
    if (Test-Path -LiteralPath $Paths.SessionFile) {
        try { Stop-PgCluster -Paths $Paths } catch { }
    }
    Remove-Item -LiteralPath $Paths.SessionFile -Force -ErrorAction SilentlyContinue
}
