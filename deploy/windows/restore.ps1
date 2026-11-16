# 柠檬账 · 从备份恢复
# 用法：restore.ps1 -Mode prod -File "D:\...\lemon-20260101-020000.dump"
# 注意：会删除当前业务数据库后重建，恢复前会要求输入 YES 确认。

[CmdletBinding()]
param(
    [ValidateSet('demo', 'prod')][string]$Mode = 'prod',
    [Parameter(Mandatory)][string]$File,
    [switch]$Force
)

$ErrorActionPreference = 'Stop'

$Root = Split-Path -Parent $PSScriptRoot
if ($env:LEMON_ROOT) { $Root = $env:LEMON_ROOT }

. (Join-Path $PSScriptRoot 'lib\common.ps1')
. (Join-Path $PSScriptRoot 'lib\pg.ps1')
. (Join-Path $PSScriptRoot 'lib\lemon.ps1')

$Paths = Get-LemonPaths -Root $Root -Mode $Mode

try {
    Write-Host ''
    Write-Host '============================================================' -ForegroundColor DarkCyan
    Write-Host ('  柠檬账 · 恢复数据（{0}）' -f $Paths.Mode.Label) -ForegroundColor White
    Write-Host '============================================================' -ForegroundColor DarkCyan

    if (-not (Test-Path -LiteralPath $File)) {
        Write-Fail ('找不到备份文件：{0}' -f $File)
        exit 1
    }

    $password = Get-ModePassword -Paths $Paths
    if (-not $password) {
        Write-Fail '数据库尚未初始化，请先启动一次服务。'
        exit 1
    }

    Write-Warn2 '此操作会删除当前数据库中的全部数据，并用备份文件替换。'
    if (-not $Force) {
        Write-Host ''
        $answer = Read-Host '  确认继续请输入 YES'
        if ($answer -ne 'YES') {
            Write-Info '已取消。'
            exit 0
        }
    }

    Stop-LemonApp -Paths $Paths -External | Out-Null

    $startedHere = $false
    if (-not (Test-PgRunning -Paths $Paths)) {
        Start-PgCluster -Paths $Paths
        $startedHere = $true
    }

    Write-PgPassFile -Paths $Paths -Password $password

    $dropdb = Get-PgExe -Paths $Paths -Name 'dropdb'
    $createdb = Get-PgExe -Paths $Paths -Name 'createdb'
    $pgRestore = Get-PgExe -Paths $Paths -Name 'pg_restore'
    $port = $Paths.Mode.PgPort

    Write-Step '重建业务数据库'
    Invoke-Native -FilePath $dropdb -TimeoutMs 60000 -Arguments @(
        '-h', '127.0.0.1', '-p', $port, '-U', $script:DbUser, '--if-exists', $script:DbName
    ) | Out-Null

    Invoke-Native -FilePath $createdb -TimeoutMs 60000 -Arguments @(
        '-h', '127.0.0.1', '-p', $port, '-U', $script:DbUser, '-E', 'UTF8', '-T', 'template0', '-O', $script:DbUser, $script:DbName
    ) | Out-Null

    $verify = Invoke-Psql -Paths $Paths -Sql "select 1 from pg_database where datname='$($script:DbName)'"
    if ([string]$verify.Output -notmatch '1') {
        Write-Fail '创建数据库失败'
        Show-NativeOutput -Result $verify -Lines 10
        throw 'createdb 失败'
    }

    Write-Step '导入备份'
    $r = Invoke-Native -FilePath $pgRestore -TimeoutMs 1800000 -Arguments @(
        '-h', '127.0.0.1', '-p', $port, '-U', $script:DbUser, '-d', $script:DbName,
        '--clean', '--if-exists', '--no-owner', '--single-transaction', $File
    )

    $tableCount = Invoke-Psql -Paths $Paths -Database $script:DbName -Sql "select count(*) from information_schema.tables where table_schema='public'"
    if ([string]$tableCount.Output -notmatch '[1-9]') {
        Write-Fail '导入备份失败，数据库中没有表。'
        Show-NativeOutput -Result $r -Lines 15
        throw 'pg_restore 失败'
    }

    if ($startedHere) { Stop-PgCluster -Paths $Paths }

    Write-Host ''
    Write-Ok '恢复完成'
    Write-Info '重新双击「2-正式使用.bat」即可使用；已登录的用户需要重新登录。'
    Write-Host ''
} catch {
    Write-Host ''
    Write-Fail $_.Exception.Message
    exit 1
}
