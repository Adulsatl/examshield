#requires -version 5.1
<#
ExamShield EDU – Windows Installer Script (PoC)
- Prompts for Telegram bot token and chat ID
- Lets you select installed programs to whitelist (optional)
- Builds the agent and installs it as a Windows service running in background
#>

param(
    [string]$ServerUrl = "http://127.0.0.1:8082"
)

function Assert-Admin {
    $currentIdentity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = New-Object Security.Principal.WindowsPrincipal($currentIdentity)
    if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        Write-Error "Please run this script as Administrator."
        exit 1
    }
}

function Get-GoExe {
    $candidates = @(
        "C:\\Program Files\\Go\\bin\\go.exe",
        "$env:LOCALAPPDATA\\Programs\\Go\\bin\\go.exe"
    )
    foreach ($p in $candidates) { if (Test-Path $p) { return $p } }
    $go = (Get-Command go -ErrorAction SilentlyContinue)?.Source
    if ($go) { return $go }
    throw "go.exe not found. Please install Go (winget install -e --id GoLang.Go)"
}

function Ensure-Dirs {
    param([string]$ProgDir, [string]$StateDir)
    New-Item -ItemType Directory -Force -Path $ProgDir | Out-Null
    New-Item -ItemType Directory -Force -Path $StateDir | Out-Null
}

function Prompt-Telegram {
    $bot = Read-Host "Enter Telegram Bot Token (leave blank to skip alerts)"
    $chat = ""
    if ($bot) { $chat = Read-Host "Enter Telegram Chat ID" }
    return @{ bot=$bot; chat=$chat }
}

function Get-InstalledPrograms {
    $paths = @(
        'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall',
        'HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall',
        'HKCU:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall'
    )
    $apps = @()
    foreach ($path in $paths) {
        if (Test-Path $path) {
            Get-ChildItem $path | ForEach-Object {
                $d = Get-ItemProperty $_.PsPath -ErrorAction SilentlyContinue
                if ($d.DisplayName) {
                    $apps += [pscustomobject]@{
                        Name = $d.DisplayName
                        DisplayIcon = $d.DisplayIcon
                        InstallLocation = $d.InstallLocation
                    }
                }
            }
        }
    }
    $apps | Sort-Object Name -Unique
}

function Select-Whitelist {
    $apps = Get-InstalledPrograms
    Write-Host "Found $($apps.Count) installed programs."
    $whitelist = @()
    if (Get-Command Out-GridView -ErrorAction SilentlyContinue) {
        $sel = $apps | Out-GridView -Title "Select apps to ALLOW during exam (Ctrl/Shift to multi-select)" -PassThru
        foreach ($s in $sel) {
            # Ask for process executable name(s) for each selection
            $exe = Read-Host "Enter process executable(s) for '$($s.Name)' to whitelist (comma-separated, e.g., code.exe,pwsh.exe). Leave blank to skip"
            if ($exe) { $whitelist += ($exe -split ',').Trim() }
        }
    } else {
        Write-Host "Out-GridView not available. You can enter process executable names manually (comma-separated)."
        $exe = Read-Host "Enter process executable(s) to whitelist (e.g., code.exe,pwsh.exe). Leave blank to skip"
        if ($exe) { $whitelist += ($exe -split ',').Trim() }
    }
    # Always include system essentials the agent may need
    $defaults = @('agent.exe','svchost.exe','services.exe','taskmgr.exe','explorer.exe','powershell.exe','pwsh.exe')
    $whitelist = ($whitelist + $defaults) | Where-Object { $_ -and $_.Trim() } | Sort-Object -Unique
    return $whitelist
}

function Write-LocalConfig {
    param([string]$StateDir, [string]$BotToken, [string]$ChatID, [string[]]$AppWhitelist)
    $cfg = [pscustomobject]@{
        telegram_bot_token = $BotToken
        telegram_chat_id   = $ChatID
        app_whitelist      = $AppWhitelist
    }
    $json = $cfg | ConvertTo-Json -Depth 4
    $cfgPath = Join-Path $StateDir 'config.json'
    Set-Content -Path $cfgPath -Value $json -Encoding UTF8 -Force
    return $cfgPath
}

function Build-Agent {
    param([string]$GoExe, [string]$RepoRoot, [string]$ProgDir)
    $agentSrc = Join-Path $RepoRoot 'agent\cmd\agent'
    $outPath = Join-Path $ProgDir 'agent.exe'
    Push-Location $agentSrc
    & $GoExe build -o $outPath .
    if ($LASTEXITCODE -ne 0 -or -not (Test-Path $outPath)) { throw "agent build failed" }
    Pop-Location
    return $outPath
}

function Install-Service {
    param([string]$ExePath)
    $svcName = 'ExamShieldEDU'
    $display = 'ExamShield EDU Agent'
    # Stop and remove if exists
    if (Get-Service -Name $svcName -ErrorAction SilentlyContinue) {
        try { Stop-Service -Name $svcName -Force -ErrorAction SilentlyContinue } catch {}
        sc.exe delete $svcName | Out-Null
        Start-Sleep -Seconds 1
    }
    sc.exe create $svcName binPath= '"' + $ExePath + '"' start= auto DisplayName= '"' + $display + '"' | Out-Null
    sc.exe description $svcName "ExamShield EDU agent service" | Out-Null
    Start-Sleep -Seconds 1
    Start-Service -Name $svcName
}

# Main
Assert-Admin

$RepoRoot = (Resolve-Path "$PSScriptRoot\..\").Path
$ProgDir = "C:\\Program Files\\ExamShieldEDU"
$StateDir = Join-Path $env:ProgramData 'ExamShieldEDU'
Ensure-Dirs -ProgDir $ProgDir -StateDir $StateDir

Write-Host "Server URL (Enter to accept default): $ServerUrl"
$inServer = Read-Host "Server URL"; if ($inServer) { $ServerUrl = $inServer }
$env:EXAMSHIELD_SERVER = $ServerUrl

$tg = Prompt-Telegram
$wl = Select-Whitelist
$cfgPath = Write-LocalConfig -StateDir $StateDir -BotToken $tg.bot -ChatID $tg.chat -AppWhitelist $wl
Write-Host "Wrote local config: $cfgPath"

$go = Get-GoExe
$exe = Build-Agent -GoExe $go -RepoRoot $RepoRoot -ProgDir $ProgDir
Write-Host "Built agent: $exe"

Install-Service -ExePath $exe
Write-Host "Installed and started Windows service 'ExamShieldEDU' (auto-start)."
Write-Host "Done. You can view logs by running: Get-EventLog -LogName Application or using a file-based logger in future iterations."
