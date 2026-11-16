# 开发辅助：统一脚本编码
# .ps1 → UTF-8 with BOM + CRLF；.bat → ASCII + CRLF
#
# 为什么需要：PowerShell 5.1 读取无 BOM 的 .ps1 时会按 ANSI 解码，
# 中文界面文案会变成乱码甚至语法错误；.bat 必须是 CRLF，否则标签解析会出问题。
# 修改任何脚本后执行一次：
#   powershell -ExecutionPolicy Bypass -File deploy\windows\normalize-scripts.ps1

param([string]$Dir = $PSScriptRoot)

$utf8Bom = New-Object System.Text.UTF8Encoding($true)

function Convert-ScriptFile {
    param([string]$Path, [bool]$WithBom)

    $text = [System.IO.File]::ReadAllText($Path, [System.Text.Encoding]::UTF8)
    $text = $text -replace "`r`n", "`n"
    $text = $text -replace "`r", "`n"
    $text = $text -replace "`n", "`r`n"

    if ($WithBom) {
        [System.IO.File]::WriteAllText($Path, $text, $utf8Bom)
    } else {
        [System.IO.File]::WriteAllText($Path, $text, (New-Object System.Text.ASCIIEncoding))
    }
}

$ps1 = @(Get-ChildItem -LiteralPath $Dir -Recurse -Filter '*.ps1' -File)
foreach ($f in $ps1) { Convert-ScriptFile -Path $f.FullName -WithBom $true }

$bat = @(Get-ChildItem -LiteralPath $Dir -Recurse -Filter '*.bat' -File)
foreach ($f in $bat) { Convert-ScriptFile -Path $f.FullName -WithBom $false }

# HTA 是 HTML（可以含中文），必须 UTF-8 带 BOM，否则 mshta 会按 GBK 渲染出乱码
$hta = @(Get-ChildItem -LiteralPath $Dir -Recurse -Filter '*.hta' -File)
foreach ($f in $hta) { Convert-ScriptFile -Path $f.FullName -WithBom $true }

Write-Host ('已规范化 ps1: {0} 个，bat: {1} 个，hta: {2} 个' -f $ps1.Count, $bat.Count, $hta.Count) -ForegroundColor Green

$bad = 0
foreach ($f in $ps1) {
    $b = [System.IO.File]::ReadAllBytes($f.FullName)
    $ok = ($b.Length -ge 3 -and $b[0] -eq 0xEF -and $b[1] -eq 0xBB -and $b[2] -eq 0xBF)
    if (-not $ok) { Write-Host ('  BOM 缺失: {0}' -f $f.Name) -ForegroundColor Red; $bad++ }
}
if ($bad -gt 0) { exit 1 }
Write-Host 'BOM 检查通过' -ForegroundColor Green
