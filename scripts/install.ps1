# install.ps1 — Download and install dtingest on Windows.
#
# Usage:
#   .\install.ps1 [-InstallDir <dir>]
#
# By default the binary is installed to $env:LOCALAPPDATA\Programs\dtingest.
# Pass -InstallDir to override.  The install directory is added permanently
# to the current user's PATH.
#
# The script requires an internet connection (uses Invoke-WebRequest).
#
# Run once to allow execution:
#   Set-ExecutionPolicy -Scope CurrentUser -ExecutionPolicy RemoteSigned

[CmdletBinding()]
param(
    [string]$InstallDir = ""
)

$ErrorActionPreference = "Stop"
$Repo = "dietermayrhofer/dtingest"

# ── Detect architecture ────────────────────────────────────────────────────────
$RawArch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture
switch ($RawArch) {
    "X64"   { $Arch = "amd64" }
    "Arm64" { $Arch = "arm64" }
    default {
        Write-Error "Unsupported architecture: $RawArch"
        exit 1
    }
}

Write-Host "Detected platform: windows/$Arch"

# ── Resolve latest release version ────────────────────────────────────────────
# Follow the /releases/latest redirect to extract the tag from the final URL.
$Response = Invoke-WebRequest `
    -Uri "https://github.com/$Repo/releases/latest" `
    -MaximumRedirection 0 `
    -ErrorAction SilentlyContinue `
    -UseBasicParsing
$RedirectUrl = $Response.Headers.Location
if (-not $RedirectUrl) {
    # Some PS versions follow the redirect automatically
    $RedirectUrl = $Response.BaseResponse.ResponseUri.AbsoluteUri
    if (-not $RedirectUrl) {
        $RedirectUrl = $Response.BaseResponse.RequestMessage.RequestUri.AbsoluteUri
    }
}
$Version = ($RedirectUrl -split '/')[-1]

if (-not $Version) {
    Write-Error "Could not determine the latest dtingest version."
    exit 1
}

Write-Host "Downloading dtingest $Version..."

# ── Download and extract ───────────────────────────────────────────────────────
$VersionNum = $Version.TrimStart("v")
$Archive    = "dtingest_${VersionNum}_windows_${Arch}.zip"
$TmpDir     = Join-Path ([System.IO.Path]::GetTempPath()) ([System.IO.Path]::GetRandomFileName())
New-Item -ItemType Directory -Path $TmpDir | Out-Null

try {
    $ArchivePath = Join-Path $TmpDir $Archive

    $DownloadUrl = "https://github.com/$Repo/releases/download/$Version/$Archive"
    Invoke-WebRequest -Uri $DownloadUrl -OutFile $ArchivePath -UseBasicParsing

    Expand-Archive -Path $ArchivePath -DestinationPath $TmpDir -Force

    $ExtractedBinary = Join-Path $TmpDir "dtingest.exe"
    if (-not (Test-Path $ExtractedBinary)) {
        Write-Error "dtingest.exe not found after extraction."
        exit 1
    }

    # ── Determine install directory ────────────────────────────────────────────
    if (-not $InstallDir) {
        $InstallDir = Join-Path $env:LOCALAPPDATA "Programs\dtingest"
    }
    if (-not (Test-Path $InstallDir)) {
        New-Item -ItemType Directory -Path $InstallDir | Out-Null
    }

    # ── Install binary ─────────────────────────────────────────────────────────
    $Dest = Join-Path $InstallDir "dtingest.exe"
    Move-Item -Force $ExtractedBinary $Dest

    Write-Host ""
    Write-Host "dtingest $Version installed to $Dest"

    # ── Add to user PATH if needed ─────────────────────────────────────────────
    $UserPath = [Environment]::GetEnvironmentVariable("PATH", "User")
    $PathDirs = $UserPath -split ";"
    if ($PathDirs -notcontains $InstallDir) {
        $NewPath = ($PathDirs + $InstallDir) -join ";"
        [Environment]::SetEnvironmentVariable("PATH", $NewPath, "User")
        # Also update the current session
        $env:PATH = "$env:PATH;$InstallDir"
        Write-Host ""
        Write-Host "  Added $InstallDir to your user PATH."
        Write-Host "  Open a new terminal for the change to take effect."
    }
} finally {
    Remove-Item -Recurse -Force $TmpDir -ErrorAction SilentlyContinue
}
