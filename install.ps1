# install.ps1 — Download and run dtingest on Windows.
#
# Usage:
#   .\install.ps1 -Environment <env-url> `
#                 -AccessToken <token> `
#                 -PlatformToken <token> `
#                 [ExtraArgs <dtingest args...>]
#
# All three credential parameters are optional; omit any that are not required
# for your chosen installation method.  Any extra arguments are forwarded
# verbatim to dtingest (default: "setup").
#
# The script requires either:
#   • the GitHub CLI (gh) — recommended for private repos, or
#   • a GITHUB_TOKEN environment variable with repo read access.
#
# Run once to allow execution:
#   Set-ExecutionPolicy -Scope CurrentUser -ExecutionPolicy RemoteSigned

[CmdletBinding()]
param(
    [Alias("e")]
    [string]$Environment    = "",

    [Alias("a")]
    [string]$AccessToken    = "",

    [Alias("p")]
    [string]$PlatformToken  = "",

    # Additional arguments forwarded to dtingest
    [Parameter(ValueFromRemainingArguments)]
    [string[]]$ExtraArgs    = @()
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
$ghAvailable = $null -ne (Get-Command gh -ErrorAction SilentlyContinue)
$githubToken = $env:GITHUB_TOKEN

if ($ghAvailable) {
    $Version = (gh release view --repo $Repo --json tagName -q ".tagName" 2>$null).Trim()
} elseif ($githubToken) {
    $Headers = @{
        "Authorization" = "Bearer $githubToken"
        "Accept"        = "application/vnd.github+json"
    }
    $Release = Invoke-RestMethod `
        -Uri "https://api.github.com/repos/$Repo/releases/latest" `
        -Headers $Headers
    $Version = $Release.tag_name
} else {
    Write-Error ("Neither 'gh' CLI nor GITHUB_TOKEN is available.`n" +
                 "  Install the GitHub CLI (https://cli.github.com/) or set GITHUB_TOKEN.")
    exit 1
}

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

    if ($ghAvailable) {
        gh release download $Version `
            --repo $Repo `
            --pattern $Archive `
            --dir $TmpDir
    } else {
        $DownloadUrl = "https://github.com/$Repo/releases/download/$Version/$Archive"
        $Headers = @{ "Authorization" = "Bearer $githubToken" }
        Invoke-WebRequest -Uri $DownloadUrl -OutFile $ArchivePath -Headers $Headers
    }

    Expand-Archive -Path $ArchivePath -DestinationPath $TmpDir -Force

    $Binary = Join-Path $TmpDir "dtingest.exe"
    if (-not (Test-Path $Binary)) {
        Write-Error "dtingest.exe not found after extraction."
        exit 1
    }

    # ── Set credentials as environment variables ───────────────────────────────
    if ($Environment)   { $env:DT_ENVIRONMENT    = $Environment }
    if ($AccessToken)   { $env:DT_ACCESS_TOKEN   = $AccessToken }
    if ($PlatformToken) { $env:DT_PLATFORM_TOKEN = $PlatformToken }

    # ── Run dtingest ───────────────────────────────────────────────────────────
    if ($ExtraArgs.Count -gt 0) {
        & $Binary @ExtraArgs
    } else {
        & $Binary setup
    }
} finally {
    Remove-Item -Recurse -Force $TmpDir -ErrorAction SilentlyContinue
}
