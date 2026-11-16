# 柠檬账 · 便携 PostgreSQL 生命周期封装
# 依赖 common.ps1 已点引用。
#
# 设计要点：
# - 每个模式一个独立 cluster（独立数据目录、独立端口），互不影响；
# - 参数一律写进 postgresql.conf，命令行保持最简，避免 Windows 上的引号转义问题；
# - 启动前先判断"是不是已经在跑"，在跑就复用，绝不重复 initdb / start；
# - 所有原生调用走 Invoke-Native（重定向到文件），不用管道，避免子进程继承句柄卡死；
# - 成败一律用"可观测结果"判断（PG_VERSION 是否存在、pg_isready 的输出、
#   查询结果、文件大小），不依赖进程退出码——PowerShell 5.1 下 Start-Process
#   返回的进程对象取不到 ExitCode。

function Get-PgExe {
    param(
        [Parameter(Mandatory)]$Paths,
        [Parameter(Mandatory)][string]$Name
    )

    $exe = Join-Path $Paths.PgBin ($Name + '.exe')
    if (-not (Test-Path -LiteralPath $exe)) {
        throw "缺少数据库程序：$exe"
    }
    return $exe
}

function Get-PgDataPath {
    param([Parameter(Mandatory)]$Paths)

    # 非 ASCII 路径时尽量换成 8.3 短路径，PostgreSQL 对中文路径支持不稳。
    return (Resolve-PgPath -Path $Paths.PgData)
}

function Test-PgInitialized {
    param([Parameter(Mandatory)]$Paths)

    return (Test-Path -LiteralPath (Join-Path $Paths.PgData 'PG_VERSION'))
}

function Test-PgRunning {
    param([Parameter(Mandatory)]$Paths)

    if (-not (Test-PgInitialized -Paths $Paths)) { return $false }

    $pgCtl = Get-PgExe -Paths $Paths -Name 'pg_ctl'
    $data = Get-PgDataPath -Paths $Paths
    $r = Invoke-Native -FilePath $pgCtl -Arguments @('-D', $data, 'status') -TimeoutMs 15000
    $text = [string]$r.Output
    if ($text -match 'no server running') { return $false }
    return ($text -match 'server is running')
}

function Clear-StalePid {
    param([Parameter(Mandatory)]$Paths)

    $pidFile = Join-Path $Paths.PgData 'postmaster.pid'
    if (-not (Test-Path -LiteralPath $pidFile)) { return }

    $first = Get-Content -LiteralPath $pidFile -TotalCount 1 -ErrorAction SilentlyContinue
    if (-not $first) {
        Remove-Item -LiteralPath $pidFile -Force -ErrorAction SilentlyContinue
        return
    }

    $oldId = 0
    if (-not [int]::TryParse($first.Trim(), [ref]$oldId)) {
        Remove-Item -LiteralPath $pidFile -Force -ErrorAction SilentlyContinue
        return
    }

    $proc = Get-Process -Id $oldId -ErrorAction SilentlyContinue
    if (-not $proc -or $proc.ProcessName -ne 'postgres') {
        # 上次异常退出（关窗口、任务管理器结束、断电）留下的僵尸 pid 文件
        Remove-Item -LiteralPath $pidFile -Force -ErrorAction SilentlyContinue
        Write-Warn2 '检测到上次未正常停止，已清理残留的数据库进程记录。'
    }
}

function Set-PgConfig {
    param([Parameter(Mandatory)]$Paths)

    $conf = Join-Path $Paths.PgData 'postgresql.conf'
    if (-not (Test-Path -LiteralPath $conf)) { throw "找不到配置文件：$conf" }

    $begin = '# ==== lemon ===='
    $end = '# ==== lemon end ===='

    $original = Get-Content -LiteralPath $conf -Encoding UTF8
    $kept = New-Object System.Collections.Generic.List[string]
    $skip = $false
    foreach ($line in $original) {
        if ($line -eq $begin) { $skip = $true; continue }
        if ($line -eq $end) { $skip = $false; continue }
        if (-not $skip) { [void]$kept.Add($line) }
    }

    $buffers = '256MB'
    if ($Paths.Mode.Name -eq 'demo') { $buffers = '128MB' }

    $block = @(
        $begin
        "listen_addresses = '127.0.0.1'"
        ('port = {0}' -f $Paths.Mode.PgPort)
        "timezone = 'Asia/Shanghai'"
        "log_timezone = 'Asia/Shanghai'"
        "log_line_prefix = '%m [%p] '"
        'log_min_duration_statement = 2000'
        ('shared_buffers = {0}' -f $buffers)
        'max_connections = 60'
        'password_encryption = scram-sha-256'
        $end
    )

    $text = ($kept -join "`r`n") + "`r`n" + ($block -join "`r`n") + "`r`n"
    [System.IO.File]::WriteAllText($conf, $text, (New-Object System.Text.UTF8Encoding($false)))
}

function Initialize-PgCluster {
    param(
        [Parameter(Mandatory)]$Paths,
        [Parameter(Mandatory)][string]$Password
    )

    if (Test-PgInitialized -Paths $Paths) { return }

    # 目录存在但没有 PG_VERSION，说明上次初始化被中断
    if (Test-Path -LiteralPath $Paths.PgData) {
        $items = Get-ChildItem -LiteralPath $Paths.PgData -Force -ErrorAction SilentlyContinue
        if ($items) {
            Write-Warn2 '检测到未完成的数据库初始化（上次可能被中断）。'
            Write-Info ('将清空并重新初始化：{0}' -f $Paths.PgData)
            [System.IO.Directory]::Delete($Paths.PgData, $true)
        }
    }

    Write-Step '首次初始化数据库（约 10–60 秒，请勿关闭本窗口）'

    $initdb = Get-PgExe -Paths $Paths -Name 'initdb'
    $data = Get-PgDataPath -Paths $Paths

    # 密码文件放系统临时目录：安装路径可能含中文，initdb 对非 ASCII 路径支持不稳。
    $pwFile = [System.IO.Path]::GetTempFileName()
    Set-Content -LiteralPath $pwFile -Value $Password -NoNewline -Encoding ascii

    try {
        $r = Invoke-Native -FilePath $initdb -TimeoutMs 180000 -Arguments @(
            '-D', $data, '-U', $script:DbUser, ('--pwfile=' + $pwFile),
            '-E', 'UTF8', '--locale=C', '--data-checksums',
            '--auth-host=scram-sha-256', '--no-instructions'
        )
    } finally {
        Remove-Item -LiteralPath $pwFile -Force -ErrorAction SilentlyContinue
    }

    if (-not (Test-PgInitialized -Paths $Paths)) {
        Write-Fail '数据库初始化失败。'
        Show-NativeOutput -Result $r -Lines 15
        throw 'initdb 失败'
    }

    Set-PgConfig -Paths $Paths
    Write-Ok '数据库目录初始化完成'
}

function Start-PgCluster {
    param([Parameter(Mandatory)]$Paths)

    if (-not (Test-PgInitialized -Paths $Paths)) { throw '数据库尚未初始化' }

    if (Test-PgRunning -Paths $Paths) {
        Write-Ok ('数据库已在运行（端口 {0}），直接复用' -f $Paths.Mode.PgPort)
        return
    }

    Clear-StalePid -Paths $Paths

    $pgCtl = Get-PgExe -Paths $Paths -Name 'pg_ctl'
    $data = Get-PgDataPath -Paths $Paths
    $log = Resolve-PgPath -Path $Paths.PgLog

    if (-not (Test-Path -LiteralPath $Paths.LogDir)) {
        New-Item -ItemType Directory -Path $Paths.LogDir -Force | Out-Null
    }

    Write-Step '启动数据库'
    # 不用 -w：pg_ctl 的就绪判定依赖连接认证，失败时要干等超时；
    # 这里改成自己轮询 pg_isready，与认证无关，更快更准。
    $r = Invoke-Native -FilePath $pgCtl -TimeoutMs 60000 -Arguments @('-D', $data, '-l', $log, 'start')

    if (-not (Wait-PgReady -Paths $Paths -TimeoutSeconds 90)) {
        Write-Fail '数据库启动失败或超时。'
        Show-NativeOutput -Result $r -Lines 12
        Show-LogTail -Path $Paths.PgLog -Lines 25
        throw 'PostgreSQL 启动失败'
    }

    Write-Ok ('数据库已就绪（127.0.0.1:{0}）' -f $Paths.Mode.PgPort)
}

function Stop-PgCluster {
    param([Parameter(Mandatory)]$Paths)

    if (-not (Test-PgInitialized -Paths $Paths)) { return }
    if (-not (Test-PgRunning -Paths $Paths)) { return }

    $pgCtl = Get-PgExe -Paths $Paths -Name 'pg_ctl'
    $data = Get-PgDataPath -Paths $Paths

    Write-Step '停止数据库'
    Invoke-Native -FilePath $pgCtl -TimeoutMs 130000 -Arguments @('-D', $data, '-m', 'fast', '-w', '-t', '120', 'stop') | Out-Null

    $deadline = (Get-Date).AddSeconds(15)
    while ((Get-Date) -lt $deadline) {
        if (-not (Test-PgRunning -Paths $Paths)) {
            Write-Ok '数据库已安全停止'
            return
        }
        Start-Sleep -Milliseconds 500
    }

    Write-Warn2 '正常停止超时，执行强制停止（下次启动会自动恢复）'
    Invoke-Native -FilePath $pgCtl -TimeoutMs 70000 -Arguments @('-D', $data, '-m', 'immediate', '-w', '-t', '60', 'stop') | Out-Null
}

function Write-PgPassFile {
    param(
        [Parameter(Mandatory)]$Paths,
        [Parameter(Mandatory)][string]$Password
    )

    $line = '127.0.0.1:{0}:*:{1}:{2}' -f $Paths.Mode.PgPort, $script:DbUser, $Password
    Set-Content -LiteralPath $Paths.PgPass -Value $line -NoNewline -Encoding ascii
    $env:PGPASSFILE = $Paths.PgPass
    try {
        & icacls.exe $Paths.PgPass /inheritance:r /grant:r ("{0}:(R,W)" -f $env:USERNAME) 2>&1 | Out-Null
    } catch { }
}

function Invoke-Psql {
    param(
        [Parameter(Mandatory)]$Paths,
        [Parameter(Mandatory)][string]$Sql,
        [string]$Database = 'postgres'
    )

    $psql = Get-PgExe -Paths $Paths -Name 'psql'
    return Invoke-Native -FilePath $psql -TimeoutMs 30000 -Arguments @(
        '-h', '127.0.0.1', '-p', $Paths.Mode.PgPort, '-U', $script:DbUser,
        '-d', $Database, '-tAc', $Sql
    )
}

function Ensure-LemonDatabase {
    param(
        [Parameter(Mandatory)]$Paths,
        [Parameter(Mandatory)][string]$Password
    )

    Write-PgPassFile -Paths $Paths -Password $Password

    $check = Invoke-Psql -Paths $Paths -Sql "select 1 from pg_database where datname='$($script:DbName)'"
    if ([string]$check.Output -match '1') {
        Write-Ok '业务数据库已存在'
        return
    }

    Write-Step '创建业务数据库'
    $createdb = Get-PgExe -Paths $Paths -Name 'createdb'
    Invoke-Native -FilePath $createdb -TimeoutMs 60000 -Arguments @(
        '-h', '127.0.0.1', '-p', $Paths.Mode.PgPort, '-U', $script:DbUser,
        '-E', 'UTF8', '-T', 'template0', '-O', $script:DbUser, $script:DbName
    ) | Out-Null

    $verify = Invoke-Psql -Paths $Paths -Sql "select 1 from pg_database where datname='$($script:DbName)'"
    if ([string]$verify.Output -notmatch '1') {
        Write-Fail '创建业务数据库失败'
        Show-NativeOutput -Result $verify -Lines 10
        throw 'createdb 失败'
    }
    Write-Ok ('业务数据库 {0} 已创建' -f $script:DbName)
}

function Wait-PgReady {
    param(
        [Parameter(Mandatory)]$Paths,
        [int]$TimeoutSeconds = 60
    )

    $isReady = Get-PgExe -Paths $Paths -Name 'pg_isready'
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        $r = Invoke-Native -FilePath $isReady -TimeoutMs 10000 -Arguments @('-h', '127.0.0.1', '-p', $Paths.Mode.PgPort)
        if ([string]$r.Output -match 'accepting connections') { return $true }
        Start-Sleep -Milliseconds 500
    }
    return $false
}

function Get-PgVersionText {
    param([Parameter(Mandatory)]$Paths)

    $pgCtl = Get-PgExe -Paths $Paths -Name 'pg_ctl'
    $r = Invoke-Native -FilePath $pgCtl -TimeoutMs 15000 -Arguments @('--version')
    if ($r.Output) { return ([string]$r.Output).Trim() }
    return 'unknown'
}

function Backup-LemonDatabase {
    param(
        [Parameter(Mandatory)]$Paths,
        [Parameter(Mandatory)][string]$Password,
        [int]$KeepDays = 30
    )

    Write-PgPassFile -Paths $Paths -Password $Password

    $pgDump = Get-PgExe -Paths $Paths -Name 'pg_dump'
    if (-not (Test-Path -LiteralPath $Paths.BackupDir)) {
        New-Item -ItemType Directory -Path $Paths.BackupDir -Force | Out-Null
    }

    $stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
    $final = Join-Path $Paths.BackupDir ('lemon-' + $stamp + '.dump')
    $tmp = $final + '.partial'

    $r = Invoke-Native -FilePath $pgDump -TimeoutMs 600000 -Arguments @(
        '-h', '127.0.0.1', '-p', $Paths.Mode.PgPort, '-U', $script:DbUser,
        '-Fc', '-Z', '6', '-f', $tmp, $script:DbName
    )

    if (-not (Test-Path -LiteralPath $tmp)) {
        Write-Fail '备份失败'
        Show-NativeOutput -Result $r -Lines 10
        throw 'pg_dump 未生成文件'
    }

    $size = (Get-Item -LiteralPath $tmp).Length
    if ($size -le 0) {
        Remove-Item -LiteralPath $tmp -Force -ErrorAction SilentlyContinue
        throw '备份文件为空'
    }

    Move-Item -LiteralPath $tmp -Destination $final -Force

    # 校验文件可被 pg_restore 读取（只列目录，不落数据）
    $pgRestore = Get-PgExe -Paths $Paths -Name 'pg_restore'
    $check = Invoke-Native -FilePath $pgRestore -TimeoutMs 120000 -Arguments @('-l', $final)
    if (-not $check.Output) {
        Write-Warn2 '备份文件已生成，但校验未通过，请检查磁盘空间。'
    }

    $expired = @(Get-ChildItem -LiteralPath $Paths.BackupDir -Filter 'lemon-*.dump' -ErrorAction SilentlyContinue |
        Where-Object { $_.LastWriteTime -lt (Get-Date).AddDays(-$KeepDays) })
    foreach ($f in $expired) {
        Remove-Item -LiteralPath $f.FullName -Force -ErrorAction SilentlyContinue
    }

    Get-ChildItem -LiteralPath $Paths.BackupDir -Filter '*.partial' -ErrorAction SilentlyContinue |
        Where-Object { $_.LastWriteTime -lt (Get-Date).AddDays(-1) } |
        ForEach-Object { Remove-Item -LiteralPath $_.FullName -Force -ErrorAction SilentlyContinue }

    return [pscustomobject]@{ File = $final; Size = $size; Removed = $expired.Count }
}
