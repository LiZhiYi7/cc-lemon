# 柠檬账 · Windows 启动脚本公共部分
# 由 start.ps1 / stop.ps1 / status.ps1 / backup.ps1 / restore.ps1 点引用（dot-source）。
# 编码约定：本文件为 UTF-8 带 BOM + CRLF，PowerShell 5.1 才不会把中文当 ANSI 解码。

$ErrorActionPreference = 'Stop'
# 不要设置 [Console]::OutputEncoding：默认的 ANSI 代码页在中文 Windows 上
# 能正确显示中文；强行改 UTF-8 会让重定向到文件/管道时的输出变成乱码。

# ---------------------------------------------------------------- 模式定义

$script:Modes = @{
    demo = [pscustomobject]@{
        Name    = 'demo'
        Label   = '离线单机运行'
        AppPort = 18080
        PgPort  = 55432
        Listen  = '127.0.0.1'
    }
    prod = [pscustomobject]@{
        Name    = 'prod'
        Label   = '正式使用'
        AppPort = 8080
        PgPort  = 55433
        Listen  = '0.0.0.0'
    }
}

$script:DbName = 'lemon'
$script:DbUser = 'lemon'

# ---------------------------------------------------------------- 输出

function Write-Step {
    param([string]$Message)
    Write-Host ''
    Write-Host ('[{0}] {1}' -f (Get-Date -Format 'HH:mm:ss'), $Message) -ForegroundColor Cyan
}

function Write-Ok {
    param([string]$Message)
    Write-Host ('  [OK]   {0}' -f $Message) -ForegroundColor Green
}

function Write-Warn2 {
    param([string]$Message)
    Write-Host ('  [注意] {0}' -f $Message) -ForegroundColor Yellow
}

function Write-Fail {
    param([string]$Message)
    Write-Host ('  [失败] {0}' -f $Message) -ForegroundColor Red
}

function Write-Info {
    param([string]$Message)
    Write-Host ('         {0}' -f $Message) -ForegroundColor Gray
}

# ---------------------------------------------------------------- 路径

function Test-AsciiPath {
    param([string]$Path)
    return ($Path -notmatch '[^\x00-\x7F]')
}

function Get-LemonPaths {
    param(
        [Parameter(Mandatory)][string]$Root,
        [Parameter(Mandatory)][string]$Mode
    )

    $m = $script:Modes[$Mode]
    if (-not $m) { throw "未知模式：$Mode（只能是 demo 或 prod）" }

    $dataDir = Join-Path $Root ('data\' + $Mode)
    $logsDir = Join-Path $Root 'logs'

    [pscustomobject]@{
        Root      = $Root
        Mode      = $m
        Exe       = Join-Path $Root 'lemon.exe'
        Web       = Join-Path $Root 'web'
        PgBin     = Join-Path $Root 'pgsql\bin'
        Config    = Join-Path $Root ('config\' + $Mode + '.yaml')
        DataDir   = $dataDir
        PgData    = Join-Path $dataDir 'pg'
        PgLog     = Join-Path (Join-Path $logsDir $Mode) 'pg.log'
        LogDir    = Join-Path $logsDir $Mode
        Secret    = Join-Path (Join-Path $Root 'secrets') ($Mode + '.pass')
        PgPass    = Join-Path $dataDir 'pgpass.conf'
        RunDir    = Join-Path (Join-Path $Root 'run') $Mode
        PidFile   = Join-Path (Join-Path $Root 'run') ($Mode + '\lemon.pid')
        # 会话标记：存在的含义是"这个模式由 start.ps1 托管，退出时该由它停数据库"。
        # 外部脚本（停止/恢复）先删掉它，再杀应用，避免 start.ps1 的清理把手里的数据库停掉。
        SessionFile = Join-Path (Join-Path $Root 'run') ($Mode + '\session')
        BackupDir = Join-Path $Root 'backups\prod'
    }
}

function Initialize-LemonDirs {
    param([Parameter(Mandatory)]$Paths)

    foreach ($d in @($Paths.DataDir, $Paths.LogDir, $Paths.RunDir, (Join-Path $Paths.Root 'secrets'), $Paths.BackupDir)) {
        if (-not (Test-Path -LiteralPath $d)) {
            New-Item -ItemType Directory -Path $d -Force | Out-Null
        }
    }
}

# ---------------------------------------------------------------- 预检

function Get-ShortPath {
    param([Parameter(Mandatory)][string]$Path)

    try {
        $result = & cmd.exe /c ('for %I in ("{0}") do @echo %~sI' -f $Path) 2>$null
        if ($LASTEXITCODE -eq 0) {
            $s = ($result | Where-Object { $_ -and $_.Trim() } | Select-Object -First 1)
            if ($s) { return $s.Trim() }
        }
    } catch {
        # 取不到短路径不算错误，交给调用方决定
    }
    return $null
}

function Resolve-PgPath {
    # PostgreSQL 在 Windows 上对非 ASCII 路径支持不稳，能取到 8.3 短路径就换掉。
    param([Parameter(Mandatory)][string]$Path)

    if ($Path -notmatch '[^\x00-\x7F]') { return $Path }
    $short = Get-ShortPath -Path $Path
    if ($short -and $short -notmatch '[^\x00-\x7F]') { return $short }
    return $Path
}

function Test-PortInUse {
    param([Parameter(Mandatory)][int]$Port)

    $conn = Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue
    if ($conn) {
        $pid_ = ($conn | Select-Object -First 1).OwningProcess
        $name = 'unknown'
        try { $name = (Get-Process -Id $pid_ -ErrorAction Stop).ProcessName } catch { }
        return [pscustomobject]@{ InUse = $true; Pid = $pid_; Process = $name }
    }

    # 老系统兜底
    try {
        $client = New-Object System.Net.Sockets.TcpClient
        $client.Connect('127.0.0.1', $Port)
        $client.Close()
        return [pscustomobject]@{ InUse = $true; Pid = 0; Process = 'unknown' }
    } catch {
        return [pscustomobject]@{ InUse = $false; Pid = 0; Process = '' }
    }
}

function Test-LemonPreflight {
    param([Parameter(Mandatory)]$Paths)

    $root = $Paths.Root
    $fatal = $false

    if (-not [Environment]::Is64BitOperatingSystem) {
        Write-Fail '需要 64 位 Windows（10 或 11）。'
        $fatal = $true
    }

    if ($root.StartsWith('\\')) {
        Write-Fail '不能放在网络路径（\\ 开头）上运行，PostgreSQL 无法使用网络路径作为数据目录。'
        Write-Fail '请把整个文件夹复制到本机硬盘，例如 C:\lemon。'
        $fatal = $true
    } else {
        try {
            $drive = (Get-Item -LiteralPath $root).PSDrive
            if ($drive -and ($drive.DriveType -eq 'Network' -or $drive.DriveType -eq 'NoRootDirectory')) {
                Write-Fail ('当前路径在 {0} 盘（网络驱动器）上，PostgreSQL 无法使用。' -f $drive.Name)
                Write-Fail '请把整个文件夹复制到本机硬盘，例如 C:\lemon。'
                $fatal = $true
            }
            if ($drive -and $drive.DriveType -eq 'Removable') {
                Write-Warn2 '当前路径在可移动磁盘（U 盘）上，性能和稳定性都不理想，建议复制到本机硬盘。'
            }
        } catch {
            Write-Warn2 ('无法识别磁盘类型：{0}' -f $_.Exception.Message)
        }
    }

    if (-not (Test-AsciiPath -Path $root)) {
        Write-Fail '安装路径包含中文，PostgreSQL 无法在这种目录下启动。'
        Write-Fail ('当前路径：{0}' -f $root)
        Write-Host ''
        Write-Host '  请这样处理：' -ForegroundColor Yellow
        Write-Host '    1. 把整个文件夹剪切到纯英文路径，例如  C:\lemon' -ForegroundColor Yellow
        Write-Host '    2. 重新双击对应的启动图标' -ForegroundColor Yellow
        Write-Host '  （文件夹名和上级目录都不能含中文；空格是允许的）' -ForegroundColor Gray
        $fatal = $true
    }

    if (-not (Test-Path -LiteralPath (Join-Path $Paths.PgBin 'postgres.exe'))) {
        Write-Fail ('缺少数据库程序：{0}' -f (Join-Path $Paths.PgBin 'postgres.exe'))
        Write-Fail '安装包不完整，请重新解压全部文件后再运行。'
        $fatal = $true
    }

    if (-not (Test-Path -LiteralPath $Paths.Exe)) {
        Write-Fail ('缺少主程序：{0}' -f $Paths.Exe)
        $fatal = $true
    }

    if (-not (Test-Path -LiteralPath (Join-Path $Paths.Web 'index.html'))) {
        Write-Fail '缺少前端文件：web\index.html'
        $fatal = $true
    }

    $pgExe = Join-Path $Paths.PgBin 'postgres.exe'
    if (Test-Path -LiteralPath $pgExe) {
        $vc = Join-Path $env:SystemRoot 'System32\vcruntime140.dll'
        if (-not (Test-Path -LiteralPath $vc)) {
            Write-Warn2 '系统缺少 VC++ 运行库（vcruntime140.dll）。'
            Write-Info ('如提示数据库启动失败，请先安装：{0}' -f (Join-Path $Paths.Root 'pgsql\vcredist\VC_redist.x64.exe'))
        }
    }

    if ($fatal) { return $false }
    return $true
}

function Test-PortOrFail {
    param(
        [Parameter(Mandatory)][int]$Port,
        [Parameter(Mandatory)][string]$Role
    )

    $r = Test-PortInUse -Port $Port
    if ($r.InUse) {
        Write-Fail ('{0} 端口 {1} 已被占用（进程 {2}，PID {3}）。' -f $Role, $Port, $r.Process, $r.Pid)
        Write-Info '处理办法：关闭占用该端口的程序，或修改 config 目录下对应 yaml 里的端口后重试。'
        return $false
    }
    return $true
}

# ---------------------------------------------------------------- 密码

function New-LemonPassword {
    param([int]$Length = 32)

    $chars = 'ABCDEFGHIJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789'
    $bytes = New-Object byte[] $Length
    $rng = [System.Security.Cryptography.RandomNumberGenerator]::Create()
    $rng.GetBytes($bytes)
    $sb = New-Object System.Text.StringBuilder
    foreach ($b in $bytes) {
        [void]$sb.Append($chars[$b % $chars.Length])
    }
    return $sb.ToString()
}

function Get-ModePassword {
    param(
        [Parameter(Mandatory)]$Paths,
        [switch]$Create
    )

    if (Test-Path -LiteralPath $Paths.Secret) {
        $pw = (Get-Content -LiteralPath $Paths.Secret -TotalCount 1 -Encoding ascii)
        if ($pw) { return $pw.Trim() }
    }

    if (-not $Create) { return $null }

    $pw = New-LemonPassword
    Set-Content -LiteralPath $Paths.Secret -Value $pw -NoNewline -Encoding ascii
    try {
        & icacls.exe $Paths.Secret /inheritance:r /grant:r ("{0}:(R,W)" -f $env:USERNAME) | Out-Null
    } catch {
        Write-Warn2 '未能收紧密码文件权限，不影响使用。'
    }
    return $pw
}

function New-Dsn {
    param(
        [Parameter(Mandatory)][int]$Port,
        [Parameter(Mandatory)][string]$Password
    )

    return ('postgres://{0}:{1}@127.0.0.1:{2}/{3}?sslmode=disable&connect_timeout=5' -f `
            $script:DbUser, $Password, $Port, $script:DbName)
}

# ---------------------------------------------------------------- 网络

function Get-LanIPv4 {
    try {
        $ips = Get-NetIPAddress -AddressFamily IPv4 -ErrorAction Stop |
            Where-Object {
                $_.IPAddress -notlike '127.*' -and
                $_.IPAddress -notlike '169.254.*' -and
                $_.PrefixOrigin -ne 'WellKnown'
            } |
            Select-Object -ExpandProperty IPAddress
        return @($ips)
    } catch {
        return @()
    }
}

function Get-NetworkProfileName {
    try {
        $p = Get-NetConnectionProfile -ErrorAction Stop | Select-Object -First 1
        if ($p) { return $p.NetworkCategory.ToString() }
    } catch { }
    return '未知'
}

# ---------------------------------------------------------------- 配置

function Get-AppEndpoint {
    # 端口以 config\<mode>.yaml 的 server.addr 为准，客户可自行改；
    # 解析不到就退回模式默认值（demo 一定是回环，避免误暴露到局域网）。
    param([Parameter(Mandatory)]$Paths)

    $defaultAddr = ('{0}:{1}' -f $Paths.Mode.Listen, $Paths.Mode.AppPort)
    $addr = $defaultAddr

    if (Test-Path -LiteralPath $Paths.Config) {
        $m = Select-String -LiteralPath $Paths.Config -Pattern '^\s*addr\s*:\s*(.+?)\s*$' -ErrorAction SilentlyContinue |
            Select-Object -First 1
        if ($m) {
            $value = $m.Matches[0].Groups[1].Value.Trim().Trim('"').Trim("'")
            if ($value -match ':\d+$') { $addr = $value }
        }
    }

    $port = $Paths.Mode.AppPort
    if ($addr -match ':(\d+)$') { $port = [int]$Matches[1] }

    $host_ = '127.0.0.1'
    if ($addr -match '^([^:]*):') { $host_ = $Matches[1] }

    return [pscustomobject]@{
        Addr = $addr
        Host = $host_
        Port = $port
        LocalUrl = ('http://127.0.0.1:{0}' -f $port)
    }
}

# ---------------------------------------------------------------- 进程

function Invoke-WithCleanEnv {
    # PowerShell 5.1 的 Start-Process 在环境变量存在"仅大小写不同"的重复项时
    # 会抛「字典中的关键字已添加」（典型：HTTPS_PROXY 与 https_proxy 并存）。
    # 这里临时去掉多余的拼写，调用完再恢复。
    param([Parameter(Mandatory)][scriptblock]$Block)

    $saved = @{}
    $names = @([System.Environment]::GetEnvironmentVariables('Process').Keys)
    $groups = $names | Group-Object { $_.ToUpperInvariant() } | Where-Object { $_.Count -gt 1 }

    foreach ($g in $groups) {
        $keep = $g.Group | Where-Object { $_ -ceq $_.ToUpperInvariant() } | Select-Object -First 1
        if (-not $keep) { $keep = $g.Group[0] }
        foreach ($n in $g.Group) {
            # 必须用 -cne：PowerShell 的 -ne 大小写不敏感，
            # HTTPS_PROXY 与 https_proxy 会被判为相等，导致清理永远不生效。
            if ($n -cne $keep) {
                $saved[$n] = [System.Environment]::GetEnvironmentVariable($n, 'Process')
                [System.Environment]::SetEnvironmentVariable($n, $null, 'Process')
            }
        }
    }

    try {
        return (& $Block)
    } finally {
        foreach ($k in $saved.Keys) {
            [System.Environment]::SetEnvironmentVariable($k, $saved[$k], 'Process')
        }
    }
}

function Get-HttpStatus {
    # 用 HttpWebRequest 而不是 HttpClient：PowerShell 5.1 默认不加载
    # System.Net.Http 程序集，New-Object HttpClient 会抛"找不到类型"，
    # 接口一律变成"无响应"。（HttpWebRequest 属于 System.dll，一定有。）
    # Proxy 置空：本机若配了系统代理，访问 127.0.0.1 会被转发出去，导致假失败。
    param(
        [Parameter(Mandatory)][string]$Url,
        [int]$TimeoutSeconds = 3
    )

    $req = $null
    try {
        $req = [System.Net.HttpWebRequest]::Create($Url)
        $req.Proxy = $null
        $req.Method = 'GET'
        $req.AllowAutoRedirect = $false
        $req.Timeout = $TimeoutSeconds * 1000
        $req.ReadWriteTimeout = $TimeoutSeconds * 1000
        $resp = $req.GetResponse()
        $code = [int]$resp.StatusCode
        $resp.Close()
        return $code
    } catch [System.Net.WebException] {
        $r = $_.Exception.Response
        if ($r) {
            try { return [int]$r.StatusCode } catch { return 0 }
        }
        return 0
    } catch {
        return 0
    } finally {
        if ($req) { try { $req.Abort() } catch { } }
    }
}

function Invoke-Native {
    # 统一调用原生程序：输出重定向到临时文件，轮询等待退出。
    # 不能直接把 pg_ctl 的输出接管道——它启动的 postgres 会继承管道句柄，
    # 导致 PowerShell 永远等不到流关闭（表现为脚本卡死）。
    param(
        [Parameter(Mandatory)][string]$FilePath,
        [string[]]$Arguments = @(),
        [int]$TimeoutMs = 120000
    )

    $tmpOut = [System.IO.Path]::GetTempFileName()
    $tmpErr = [System.IO.Path]::GetTempFileName()
    $prev = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        # Start-Process 只是把数组用空格拼起来，不会自动加引号；
        # 含空格的参数（路径、SQL）必须自己包引号，否则会被拆成多个参数。
        $quoted = @($Arguments | ForEach-Object {
                if ($_ -match '\s') { '"' + $_ + '"' } else { $_ }
            })

        $sp = @{
            FilePath               = $FilePath
            NoNewWindow            = $true
            PassThru               = $true
            RedirectStandardOutput = $tmpOut
            RedirectStandardError  = $tmpErr
        }
        if ($quoted.Count -gt 0) { $sp['ArgumentList'] = $quoted }

        $p = Invoke-WithCleanEnv { Start-Process @sp }
        $exited = $p.WaitForExit($TimeoutMs)
        if (-not $exited) {
            try { $p.Kill() } catch { }
            return [pscustomobject]@{ ExitCode = -1; Output = ('命令超时（{0} ms）：{1}' -f $TimeoutMs, $FilePath); TimedOut = $true }
        }

        $text = ''
        if (Test-Path -LiteralPath $tmpOut) { $text += (Get-Content -LiteralPath $tmpOut -Raw -ErrorAction SilentlyContinue) }
        if (Test-Path -LiteralPath $tmpErr) { $text += (Get-Content -LiteralPath $tmpErr -Raw -ErrorAction SilentlyContinue) }

        return [pscustomobject]@{ ExitCode = $p.ExitCode; Output = $text; TimedOut = $false }
    } finally {
        $ErrorActionPreference = $prev
        # pg_ctl 启动的 postgres 会继承输出句柄，临时文件此时删不掉是正常的。
        # 这里绝不能让清理动作抛出异常，否则会把成功启动当成失败。
        foreach ($f in @($tmpOut, $tmpErr)) {
            try { Remove-Item -LiteralPath $f -Force -ErrorAction Stop } catch { }
        }
    }
}

function Show-NativeOutput {
    param(
        [Parameter(Mandatory)]$Result,
        [int]$Lines = 12
    )

    if (-not $Result.Output) { return }
    $Result.Output -split "`r?`n" | Where-Object { $_.Trim() } | Select-Object -Last $Lines | ForEach-Object {
        Write-Info $_
    }
}

function Get-LemonPid {
    param([Parameter(Mandatory)]$Paths)

    if (-not (Test-Path -LiteralPath $Paths.PidFile)) { return 0 }
    $raw = (Get-Content -LiteralPath $Paths.PidFile -TotalCount 1 -ErrorAction SilentlyContinue)
    if (-not $raw) { return 0 }
    $procId = 0
    if ([int]::TryParse($raw.Trim(), [ref]$procId)) {
        $p = Get-Process -Id $procId -ErrorAction SilentlyContinue
        if ($p -and $p.ProcessName -eq 'lemon') { return $procId }
    }
    return 0
}

function Show-LogTail {
    param(
        [Parameter(Mandatory)][string]$Path,
        [int]$Lines = 20
    )

    if (-not (Test-Path -LiteralPath $Path)) {
        Write-Info ('（没有日志文件：{0}）' -f $Path)
        return
    }
    Write-Host ''
    Write-Host ('--- {0} 最后 {1} 行 ---' -f (Split-Path $Path -Leaf), $Lines) -ForegroundColor DarkGray
    Get-Content -LiteralPath $Path -Tail $Lines -ErrorAction SilentlyContinue | ForEach-Object {
        Write-Host ('  ' + $_) -ForegroundColor DarkGray
    }
    Write-Host ''
}

# ---------------------------------------------------------------- 启动页回传

function Set-StartError {
    # 启动失败时把原因写给启动页（柠檬账.hta）。
    # 服务窗口在失败后会关闭，客户不一定看得到；启动页能直接显示这句话。
    # 用 UTF-16LE：启动页通过 Scripting.FileSystemObject 按 Unicode 读取，不会乱码。
    param(
        [Parameter(Mandatory)]$Paths,
        [string]$Message
    )

    try {
        if (-not (Test-Path -LiteralPath $Paths.RunDir)) {
            New-Item -ItemType Directory -Path $Paths.RunDir -Force | Out-Null
        }
        $text = $Message
        if ([string]::IsNullOrWhiteSpace($text)) { $text = '启动失败' }
        $text | Out-File -LiteralPath (Join-Path $Paths.RunDir 'start-error.txt') -Encoding Unicode -Force
    } catch { }
}

function Clear-StartError {
    param([Parameter(Mandatory)]$Paths)

    Remove-Item -LiteralPath (Join-Path $Paths.RunDir 'start-error.txt') -Force -ErrorAction SilentlyContinue
}
