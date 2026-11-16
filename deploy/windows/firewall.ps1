# 柠檬账 · 防火墙规则
# 作为库被 start.ps1 点引用；被提权单独调用时（-AppPort）只做加规则这一件事。

[CmdletBinding()]
param(
    [int]$AppPort = 0
)

function Test-IsAdmin {
    $id = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = New-Object Security.Principal.WindowsPrincipal($id)
    return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Get-FirewallRuleName {
    param([Parameter(Mandatory)][int]$AppPort)
    return ('柠檬账-正式使用-TCP{0}' -f $AppPort)
}

function Test-FirewallRule {
    param([Parameter(Mandatory)][int]$AppPort)

    $name = Get-FirewallRuleName -AppPort $AppPort
    & netsh.exe advfirewall firewall show rule name="$name" > $null 2>&1
    return ($LASTEXITCODE -eq 0)
}

function Add-FirewallRule {
    param([Parameter(Mandatory)][int]$AppPort)

    $name = Get-FirewallRuleName -AppPort $AppPort
    & netsh.exe advfirewall firewall delete rule name="$name" > $null 2>&1
    & netsh.exe advfirewall firewall add rule name="$name" dir=in action=allow `
        protocol=TCP localport=$AppPort profile=private,domain > $null 2>&1
    return ($LASTEXITCODE -eq 0)
}

function Assert-FirewallRule {
    # 保证局域网能访问；失败不阻塞启动，只给出可执行的补救说明。
    param([Parameter(Mandatory)][int]$AppPort)

    if (Test-FirewallRule -AppPort $AppPort) {
        Write-Ok ('防火墙已放行 TCP {0}' -f $AppPort)
        return $true
    }

    Write-Step ('添加防火墙规则（放行 TCP {0}）' -f $AppPort)

    if (Test-IsAdmin) {
        if (Add-FirewallRule -AppPort $AppPort) {
            Write-Ok ('已放行 TCP {0}（仅专用网络和域网络）' -f $AppPort)
            return $true
        }
        Write-Warn2 '添加防火墙规则失败（可能被组策略限制）。'
        return $false
    }

    $script = $PSCommandPath
    if (-not $script) { $script = $MyInvocation.MyCommand.Path }
    $psExe = (Get-Process -Id $PID).Path

    try {
        $argList = @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', ('"{0}"' -f $script), '-AppPort', $AppPort)
        $p = Start-Process -FilePath $psExe -Verb RunAs -PassThru -Wait -ArgumentList $argList
        Start-Sleep -Milliseconds 500
        if (Test-FirewallRule -AppPort $AppPort) {
            Write-Ok ('已放行 TCP {0}' -f $AppPort)
            return $true
        }
    } catch {
        # 用户在 UAC 弹窗点了"否"，或系统禁止提权
    }

    Write-Warn2 '未添加防火墙规则，其他电脑可能无法访问。'
    Write-Info '如果局域网打不开，请用管理员身份运行以下命令：'
    Write-Info ('netsh advfirewall firewall add rule name="{0}" dir=in action=allow protocol=TCP localport={1}' -f (Get-FirewallRuleName -AppPort $AppPort), $AppPort)
    return $false
}

# 被提权单独调用时的入口
if ($MyInvocation.InvocationName -ne '.' -and $AppPort -gt 0) {
    if (-not (Test-IsAdmin)) {
        Write-Host '需要管理员权限才能添加防火墙规则。' -ForegroundColor Yellow
        exit 1
    }
    if (Add-FirewallRule -AppPort $AppPort) {
        Write-Host ('已放行 TCP {0}' -f $AppPort) -ForegroundColor Green
        exit 0
    }
    Write-Host '添加防火墙规则失败。' -ForegroundColor Red
    exit 1
}
