[CmdletBinding()]
param(
    [ValidateSet('msedge', 'chrome', 'chromium')]
    [string]$Browser = 'msedge'
)

# Only synthetic tests run here. Live API and Docker startup are checked manually.
$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
$npmCommand = if ($env:OS -eq 'Windows_NT') { 'npm.cmd' } else { 'npm' }
foreach ($command in @('git', 'go', 'node', $npmCommand)) {
    if (-not (Get-Command $command -ErrorAction SilentlyContinue)) {
        throw "Required command not found: $command"
    }
}

function Invoke-Checked {
    param([string]$Command, [string[]]$Arguments, [string]$Log)
    Write-Host ("`n> " + $Command + ' ' + ($Arguments -join ' '))
    $previousPreference = $ErrorActionPreference
    try {
        # Native stderr includes ordinary Go/Node progress, not just failures.
        $ErrorActionPreference = 'Continue'
        & $Command @Arguments 2>&1 | ForEach-Object {
            $line = $_.ToString()
            Write-Host $line
            [System.IO.File]::AppendAllText($Log, $line + [Environment]::NewLine, [System.Text.Encoding]::UTF8)
        }
        $commandExitCode = $LASTEXITCODE
    } finally {
        $ErrorActionPreference = $previousPreference
    }
    if ($commandExitCode -ne 0) {
        throw "$Command failed with exit code $commandExitCode. See $Log. No ZIP was produced."
    }
}

Push-Location -LiteralPath $projectRoot
$previousBrowser = $env:BUDDY_BROWSER_CHANNEL
try {
    $revision = (& git rev-parse HEAD).Trim()
    if ($LASTEXITCODE -ne 0) { throw 'Cannot read repository revision.' }
    $trackedChanges = @(& git status --porcelain --untracked-files=no)
    $stamp = Get-Date -Format 'yyyyMMdd-HHmmss-fff'
    $package = Join-Path $projectRoot "artifacts/verification-$stamp"
    if (Test-Path -LiteralPath $package) { throw "Output already exists: $package" }
    $backend = Join-Path $package 'backend'
    $frontend = Join-Path $package 'frontend'
    $training = Join-Path $package 'training'
    New-Item -ItemType Directory -Path $backend, $frontend, $training -Force | Out-Null
    $backendLog = Join-Path $backend 'checks.log'
    $frontendLog = Join-Path $frontend 'checks.log'

    Push-Location -LiteralPath (Join-Path $projectRoot 'backend')
    try {
        $profile = Join-Path $backend 'coverage.out'
        Invoke-Checked 'go' @('test', './...', '-timeout=120s', '-coverpkg=./...', "-coverprofile=$profile", '-count=1') $backendLog
        Invoke-Checked 'go' @('vet', './...') $backendLog
        Invoke-Checked 'go' @('build', './...') $backendLog
        Invoke-Checked 'go' @('tool', 'cover', "-func=$profile") (Join-Path $backend 'statements.txt')
        Invoke-Checked 'go' @('tool', 'cover', "-html=$profile", "-o=$(Join-Path $backend 'coverage.html')") $backendLog

        # Pinned converter is the same one documented in README; go.mod is unchanged.
        $xmlPath = Join-Path $backend 'coverage.xml'
        $previousPreference = $ErrorActionPreference
        try {
            $ErrorActionPreference = 'Continue'
            Get-Content -LiteralPath $profile |
                & go run github.com/boumenot/gocover-cobertura@v1.5.0 -strict -ignore-non-code-lines -by-files 2> (Join-Path $backend 'converter.log') |
                Set-Content -LiteralPath $xmlPath -Encoding UTF8
            $converterExitCode = $LASTEXITCODE
        } finally {
            $ErrorActionPreference = $previousPreference
        }
        if ($converterExitCode -ne 0) { throw "Coverage conversion failed. See $backend/converter.log. No ZIP was produced." }
        [xml]$coverage = Get-Content -Raw -LiteralPath $xmlPath
        $backendCovered = [long]$coverage.coverage.'lines-covered'
        $backendTotal = [long]$coverage.coverage.'lines-valid'
    } finally { Pop-Location }

    Push-Location -LiteralPath (Join-Path $projectRoot 'frontend')
    try {
        if ($Browser -eq 'chromium') { Remove-Item Env:BUDDY_BROWSER_CHANNEL -ErrorAction SilentlyContinue }
        else { $env:BUDDY_BROWSER_CHANNEL = $Browser }
        Invoke-Checked $npmCommand @('ci') $frontendLog
        Invoke-Checked $npmCommand @('run', 'build') $frontendLog
        Invoke-Checked $npmCommand @('run', 'test:coverage') $frontendLog
        $frontendSummary = Get-Content -Raw -LiteralPath 'coverage/coverage-summary.json' | ConvertFrom-Json
        $frontendCovered = [long]$frontendSummary.total.lines.covered
        $frontendTotal = [long]$frontendSummary.total.lines.total
        Copy-Item -LiteralPath 'coverage' -Destination $frontend -Recurse
        Copy-Item -LiteralPath 'playwright-report' -Destination $frontend -Recurse
    } finally { Pop-Location }

    Push-Location -LiteralPath (Join-Path $projectRoot 'training/orders')
    try {
        Invoke-Checked 'go' @('test', '-tags', 'solution', './...', '-count=1') (Join-Path $training 'checks.log')
    } finally { Pop-Location }

    foreach ($metric in @(@($backendCovered, $backendTotal), @($frontendCovered, $frontendTotal))) {
        if ($metric[1] -le 0 -or $metric[0] * 100 -le $metric[1] * 80) {
            throw 'Application line coverage must exceed 80% for both backend and frontend. No ZIP was produced.'
        }
    }
    $summary = [ordered]@{
        revision = $revision
        created_at = [DateTime]::UtcNow.ToString('o')
        tracked_changes = $trackedChanges
        # Truncate percentages to two decimals, matching Istanbul's display.
        backend = @{ covered = $backendCovered; total = $backendTotal; percent = [Math]::Floor(10000.0 * $backendCovered / $backendTotal) / 100 }
        frontend = @{ covered = $frontendCovered; total = $frontendTotal; percent = [Math]::Floor(10000.0 * $frontendCovered / $frontendTotal) / 100 }
        combined = @{ percent = [Math]::Floor(10000.0 * ($backendCovered + $frontendCovered) / ($backendTotal + $frontendTotal)) / 100 }
        checks = @('go test/vet/build', 'npm ci/build', 'frontend unit + browser tests', 'training solution tests')
        not_checked = @('Live provider availability', 'Docker runtime startup')
    }
    # Windows PowerShell's Set-Content UTF8 adds a BOM, rejected by strict JSON readers.
    [System.IO.File]::WriteAllText((Join-Path $package 'summary.json'), ($summary | ConvertTo-Json -Depth 5), (New-Object System.Text.UTF8Encoding($false)))
    @"
# AI-Buddy verification reports

Revision: $revision
UTC: $($summary.created_at)
Backend lines: $backendCovered / $backendTotal ($($summary.backend.percent)%)
Frontend lines: $frontendCovered / $frontendTotal ($($summary.frontend.percent)%)
Combined application lines: $($summary.combined.percent)%
Tracked working tree changes: $($trackedChanges.Count)

Open backend/coverage.html and frontend/coverage/index.html in a browser.
Open frontend/playwright-report/index.html for the browser test report.
Machine-readable evidence: backend/coverage.xml, backend/coverage.out,
frontend/coverage/coverage-summary.json, frontend/coverage/lcov.info, summary.json.

Coverage includes all backend cmd/internal packages and frontend application src,
including files without tests; dependencies, generated files and tests are excluded.
Go source lines use gocover-cobertura v1.5.0; TypeScript lines use Istanbul.
training/orders is a separate teaching exercise: its solution tests are checked,
and its deliberately broken initial version is outside the AI-Buddy application metric.

The automated suite uses synthetic services and a test LLM API. It does not call
the configured Live provider or verify Docker startup. The functional scenario
and expected results are documented in docs/demo.md.

Only generated reports and this summary are packaged. The script does not read .env
or copy project configuration, model keys, application state, or the source repository.
"@ | Set-Content -LiteralPath (Join-Path $package 'README.md') -Encoding UTF8
    $archive = "$package.zip"
    Compress-Archive -LiteralPath $package -DestinationPath $archive -CompressionLevel Optimal
    Write-Host "`nReady: $archive"
    Write-Host "Backend: $($summary.backend.percent)%; frontend: $($summary.frontend.percent)% (lines)."
} finally {
    if ($null -eq $previousBrowser) { Remove-Item Env:BUDDY_BROWSER_CHANNEL -ErrorAction SilentlyContinue }
    else { $env:BUDDY_BROWSER_CHANNEL = $previousBrowser }
    Pop-Location
}
