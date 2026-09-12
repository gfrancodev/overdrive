$ErrorActionPreference = "Stop"

$Root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
$SkillsDir = if ($env:OVERDRIVE_SKILLS_DIR) { $env:OVERDRIVE_SKILLS_DIR } else { Join-Path $HOME ".agents\skills" }
$OverdriveHome = if ($env:OVERDRIVE_HOME) { $env:OVERDRIVE_HOME } else { Join-Path $HOME ".overdrive" }
$BinDir = if ($env:OVERDRIVE_BIN_DIR) { $env:OVERDRIVE_BIN_DIR } else { Join-Path $OverdriveHome "bin" }
$LibDir = if ($env:OVERDRIVE_LIB_DIR) { $env:OVERDRIVE_LIB_DIR } else { Join-Path $OverdriveHome "lib" }

New-Item -ItemType Directory -Force -Path $SkillsDir, $OverdriveHome, $BinDir, $LibDir | Out-Null

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
    throw "Compatible Overdrive runtime is missing: $Source"
}
Copy-Item -Force $Source $DestRuntime

$LibSource = Join-Path $Root "runtime\lib\windows-$Target\overdrive_turbovec_ffi.dll"
if (Test-Path $LibSource) {
    Copy-Item -Force $LibSource (Join-Path $LibDir "overdrive_turbovec_ffi.dll")
    Copy-Item -Force $LibSource (Join-Path $BinDir "overdrive_turbovec_ffi.dll")
}

try { & $DestRuntime session-start --cwd (Get-Location).Path --quiet | Out-Null } catch { }

Write-Host "Installed Overdrive skills to $SkillsDir"
Write-Host "Installed Overdrive runtime to $DestRuntime"
