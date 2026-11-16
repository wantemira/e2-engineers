$ErrorActionPreference = 'Stop'

function Get-EngineType {
    $previousPreference = $ErrorActionPreference
    try {
        $ErrorActionPreference = 'Continue'
        $engineType = & docker info --format '{{.OSType}}' 2>$null
        if ($LASTEXITCODE -eq 0) { return ([string]$engineType).Trim() }
        return ''
    } finally { $ErrorActionPreference = $previousPreference }
}

if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
    Write-Host 'Docker is not installed or not on PATH. Install Docker Desktop and run make up again.'
    exit 1
}

$engineType = Get-EngineType
if (-not $engineType) {
    Write-Host 'Docker Engine is unavailable. Starting Docker Desktop (up to 90 seconds)...'
    $previousPreference = $ErrorActionPreference
    try {
        $ErrorActionPreference = 'Continue'
        $startOutput = & docker desktop start --timeout 90 2>&1
        $startExitCode = $LASTEXITCODE
    } finally { $ErrorActionPreference = $previousPreference }
    if ($startExitCode -ne 0) {
        Write-Host 'Could not start Docker Desktop automatically. Open Docker Desktop, wait for Engine running, then run make up again.'
        exit 1
    }
    $engineType = Get-EngineType
}

if ($engineType -ne 'linux') {
    Write-Host 'AI-Buddy requires a running Linux Docker Engine. In Docker Desktop enable Linux containers, then run make up again.'
    exit 1
}

$previousPreference = $ErrorActionPreference
try {
    $ErrorActionPreference = 'Continue'
    $composeVersion = & docker compose version --short 2>$null
    $composeExitCode = $LASTEXITCODE
} finally { $ErrorActionPreference = $previousPreference }
if ($composeExitCode -ne 0) {
    Write-Host 'Docker Compose v2 or newer is required. Update Docker Desktop, then run make up again.'
    exit 1
}
Write-Host 'Docker Engine and Compose are ready (Linux containers).'
