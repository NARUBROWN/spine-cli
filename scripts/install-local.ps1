<#!
.SYNOPSIS
Builds and installs the local development version of Spine CLI.

.DESCRIPTION
The executable is written to bin\spine.exe in this repository. Run this
script again after changing the CLI source.
#>

$ErrorActionPreference = "Stop"

$repositoryRoot = Split-Path -Parent $PSScriptRoot
$outputDirectory = Join-Path $repositoryRoot "bin"
$outputPath = Join-Path $outputDirectory "spine.exe"
$developmentCommandPath = Join-Path $outputDirectory "spine-dev.exe"

New-Item -ItemType Directory -Force -Path $outputDirectory | Out-Null

# Keep build artifacts in the repository so local installation does not depend
# on the user's global Go cache permissions.
$env:GOCACHE = Join-Path $repositoryRoot ".gocache"
$env:GOMODCACHE = Join-Path $repositoryRoot ".gomodcache"

Push-Location $repositoryRoot
try {
    go build -o $outputPath ./cmd/spine
    Copy-Item -Force $outputPath $developmentCommandPath
}
finally {
    Pop-Location
}

$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
$pathEntries = @($userPath -split ';' | Where-Object { $_ })
if ($pathEntries -notcontains $outputDirectory) {
    $updatedPath = (($pathEntries + $outputDirectory) -join ';')
    [Environment]::SetEnvironmentVariable("Path", $updatedPath, "User")
}

Write-Host "Installed local Spine CLI: $outputPath"
Write-Host "Development command installed: spine-dev"
Write-Host "Open a new PowerShell window, then run: spine-dev version"
