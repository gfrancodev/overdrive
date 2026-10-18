$ErrorActionPreference = "Stop"

$Root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
$SkillsDir = if ($env:OVERDRIVE_SKILLS_DIR) { $env:OVERDRIVE_SKILLS_DIR } else { Join-Path $HOME ".agents\skills" }
$OverdriveHome = if ($env:OVERDRIVE_HOME) { $env:OVERDRIVE_HOME } else { Join-Path $HOME ".overdrive" }
$BinDir = if ($env:OVERDRIVE_BIN_DIR) { $env:OVERDRIVE_BIN_DIR } else { Join-Path $OverdriveHome "bin" }
$LibDir = if ($env:OVERDRIVE_LIB_DIR) { $env:OVERDRIVE_LIB_DIR } else { Join-Path $OverdriveHome "lib" }

New-Item -ItemType Directory -Force -Path $SkillsDir, $OverdriveHome, $BinDir, $LibDir | Out-Null

# Windows refuses to overwrite a running executable or a loaded DLL but lets it be renamed. Copy the new file
# beside it, move the old one aside, then put the new one in place; a stale .old goes on the next install.
function Install-OverdriveFile([string]$Source, [string]$Dest) {
    $staged = "$Dest.new"
    Copy-Item -Force $Source $staged
    $old = "$Dest.old"
    Remove-Item -Force $old -ErrorAction SilentlyContinue
    if (Test-Path $Dest) {
        Move-Item -Force $Dest $old
    }
    Move-Item -Force $staged $Dest
    Remove-Item -Force $old -ErrorAction SilentlyContinue
}

Get-ChildItem (Join-Path $Root "skills") -Directory | ForEach-Object {
    $Dest = Join-Path $SkillsDir $_.Name
    if (Test-Path $Dest) { Remove-Item -Recurse -Force $Dest }
    Copy-Item -Recurse -Force $_.FullName $Dest
}

$Arch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString().ToLowerInvariant()
$Target = switch ($Arch) {
    "x64" { "amd64" }
    "arm64" { "arm64" }
    default { throw "Unsupported Windows architecture: $Arch" }
}

$Source = Join-Path $Root "runtime\bin\overdrive-runtime-windows-$Target.exe"
$DestRuntime = Join-Path $BinDir "overdrive-runtime.exe"
if (-not (Test-Path $Source)) {
    throw "Compatible Overdrive runtime is missing: $Source. Install ships only the Windows-$Target prebuilt binary."
}
Install-OverdriveFile $Source $DestRuntime

$LibSource = Join-Path $Root "runtime\lib\windows-$Target\overdrive_turbovec_ffi.dll"
if (Test-Path $LibSource) {
    Install-OverdriveFile $LibSource (Join-Path $LibDir "overdrive_turbovec_ffi.dll")
    Install-OverdriveFile $LibSource (Join-Path $BinDir "overdrive_turbovec_ffi.dll")
}

# Remove foreign runtime artifacts from prior installs.
Get-ChildItem $BinDir -File -ErrorAction SilentlyContinue | ForEach-Object {
    $name = $_.Name
    if ($name -match '^overdrive-runtime-' -and $name -ne 'overdrive-runtime.exe') {
        Remove-Item -Force $_.FullName
    }
    if ($name -match '^(liboverdrive_turbovec_ffi\.(so|dylib)|overdrive_turbovec_ffi\.dll)$' -and $name -ne 'overdrive_turbovec_ffi.dll') {
        Remove-Item -Force $_.FullName
    }
}
Get-ChildItem $LibDir -File -ErrorAction SilentlyContinue | ForEach-Object {
    if ($_.Name -ne 'overdrive_turbovec_ffi.dll') {
        Remove-Item -Force $_.FullName
    }
}

$forbidden = Get-ChildItem $SkillsDir -Recurse -File -ErrorAction SilentlyContinue |
    Where-Object { $_.Extension -in '.go' -or $_.Name -in 'go.mod','go.sum','Cargo.toml' }
if ($forbidden) {
    throw "Install destination contains forbidden dev payload"
}

try {
    & $DestRuntime session-start --cwd (Get-Location).Path --quiet | Out-Null
} catch {
    Write-Warning "session-start failed and was skipped: $_"
}

$TrayDest = Join-Path $BinDir "overdrive-tray.exe"
$TrayPrebuilt = Join-Path $Root "runtime\bin\overdrive-tray-windows-$Target.exe"
$TraySrc = Join-Path $Root "runtime\tray"
$TrayOK = $false
if (Test-Path $TrayPrebuilt) {
    Install-OverdriveFile $TrayPrebuilt $TrayDest
    $TrayOK = $true
} elseif ((Test-Path (Join-Path $TraySrc "go.mod")) -and (Get-Command go -ErrorAction SilentlyContinue)) {
    $savedCgo = $env:CGO_ENABLED
    $savedGoos = $env:GOOS
    $savedGoarch = $env:GOARCH
    $pushed = $false
    try {
        $env:CGO_ENABLED = "0"
        $env:GOOS = "windows"
        $env:GOARCH = $Target
        Push-Location $TraySrc
        $pushed = $true
        $TrayBuilt = Join-Path $BinDir "overdrive-tray.build.exe"
        & go build -buildvcs=false -trimpath "-ldflags=-s -w" -o $TrayBuilt .\cmd\overdrive-tray
        if ($LASTEXITCODE -eq 0 -and (Test-Path $TrayBuilt)) {
            Install-OverdriveFile $TrayBuilt $TrayDest
            Remove-Item -Force $TrayBuilt -ErrorAction SilentlyContinue
            $TrayOK = $true
        }
    } catch {
        Write-Warning "Overdrive tray build failed: $_"
    } finally {
        if ($pushed) { Pop-Location }
        $env:CGO_ENABLED = $savedCgo
        $env:GOOS = $savedGoos
        $env:GOARCH = $savedGoarch
    }
}
if (-not $TrayOK) {
    Write-Warning "Overdrive tray was not installed. The Core still runs without a tray."
} elseif ($env:OVERDRIVE_TRAY_ENABLE -ne "0") {
    & $TrayDest register
    if ($LASTEXITCODE -ne 0) {
        Write-Warning "Overdrive tray autostart was not registered. The Core still runs."
    } else {
        Write-Host "Installed Overdrive tray to $TrayDest"
    }
} else {
    Write-Host "Installed Overdrive tray to $TrayDest"
}

Write-Host "Installed Overdrive skills to $SkillsDir"
Write-Host "Installed Overdrive runtime to $DestRuntime (windows-$Target only)"
