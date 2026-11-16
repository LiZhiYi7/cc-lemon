# 柠檬账 · 应用进程封装
# 依赖 common.ps1 已点引用。

function Start-LemonApp {
    param(
        [Parameter(Mandatory)]$Paths,
        [Parameter(Mandatory)]$Endpoint,
        [Parameter(Mandatory)][string]$Password
    )

    $dsn = New-Dsn -Port $Paths.Mode.PgPort -Password $Password
    $stamp = Get-Date -Format 'yyyyMMdd-HHmmss'

    if (-not (Test-Path -LiteralPath $Paths.LogDir)) {
        New-Item -ItemType Directory -Path $Paths.LogDir -Force | Out-Null
    }

    $errLog = Join-Path $Paths.LogDir ('app-' + $stamp + '.log')
    $outLog = Join-Path $Paths.LogDir ('app-' + $stamp + '.out.log')

    $env:LEMON_CONFIG = $Paths.Config
    $env:LEMON_SERVER_ADDR = $Endpoint.Addr
    $env:LEMON_SERVER_STATIC_DIR = $Paths.Web
    $env:LEMON_DATABASE_DSN = $dsn

    Write-Step '启动柠檬账服务'
    $proc = Invoke-WithCleanEnv {
        Start-Process -FilePath $Paths.Exe -WorkingDirectory $Paths.Root -PassThru `
            -RedirectStandardOutput $outLog -RedirectStandardError $errLog
    }

    $proc.Id | Set-Content -LiteralPath $Paths.PidFile -Encoding ascii
    Set-Content -LiteralPath $Paths.SessionFile -Value $proc.Id -Encoding ascii

    return [pscustomobject]@{
        Process = $proc
        Pid     = $proc.Id
        Log     = $errLog
        OutLog  = $outLog
    }
}

function Wait-AppReady {
    param(
        [Parameter(Mandatory)]$Endpoint,
        [Parameter(Mandatory)][System.Diagnostics.Process]$Process,
        [int]$TimeoutSeconds = 120
    )

    $url = $Endpoint.LocalUrl + '/healthz'
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)

    while ((Get-Date) -lt $deadline) {
        if ($Process.HasExited) { return $false }
        $status = Get-HttpStatus -Url $url -TimeoutSeconds 3
        if ($status -eq 200) { return $true }
        Start-Sleep -Milliseconds 800
    }
    return $false
}

function Show-AppLogTail {
    param(
        [Parameter(Mandatory)]$App,
        [int]$Lines = 25
    )

    Show-LogTail -Path $App.Log -Lines $Lines
}

function Stop-LemonApp {
    param(
        [Parameter(Mandatory)]$Paths,
        # 外部脚本（停止/恢复）调用时为真：先撤销会话标记，
        # 让 start.ps1 的退出清理知道"数据库由调用方负责"，不要顺手停掉。
        [switch]$External
    )

    if ($External) {
        Remove-Item -LiteralPath $Paths.SessionFile -Force -ErrorAction SilentlyContinue
    }

    $procId = Get-LemonPid -Paths $Paths
    if ($procId -eq 0) {
        Remove-Item -LiteralPath $Paths.PidFile -Force -ErrorAction SilentlyContinue
        return $false
    }

    Write-Step '停止应用服务'
    try {
        Stop-Process -Id $procId -ErrorAction Stop
        $deadline = (Get-Date).AddSeconds(10)
        while ((Get-Date) -lt $deadline) {
            if (-not (Get-Process -Id $procId -ErrorAction SilentlyContinue)) { break }
            Start-Sleep -Milliseconds 300
        }
        $still = Get-Process -Id $procId -ErrorAction SilentlyContinue
        if ($still) { Stop-Process -Id $procId -Force -ErrorAction SilentlyContinue }
        Write-Ok '应用服务已停止'
    } catch {
        Write-Warn2 ('停止应用时出错：{0}' -f $_.Exception.Message)
    }

    Remove-Item -LiteralPath $Paths.PidFile -Force -ErrorAction SilentlyContinue
    return $true
}

function Open-LemonBrowser {
    param([Parameter(Mandatory)]$Endpoint)

    try {
        Start-Process $Endpoint.LocalUrl | Out-Null
        return $true
    } catch {
        return $false
    }
}
