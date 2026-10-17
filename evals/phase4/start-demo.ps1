param([switch]$Submit, [switch]$CheckOnly)
$ErrorActionPreference = 'Stop'
if (!$Submit) { throw 'Submission defaults off. Use -Submit to explicitly create one pending demo proposal. No live LLM calls are made.' }
if ((node --version) -ne 'v24.21.0') { throw 'Node.js 24.21.0 is required.' }
$repoRoot = Split-Path (Split-Path $PSScriptRoot -Parent) -Parent
$runDir = Join-Path $repoRoot 'evals/dist/p405'
if (Test-Path -LiteralPath (Join-Path $runDir 'session.private.json')) { throw 'A demo session file already exists. Stop that session before starting another.' }
New-Item -ItemType Directory -Force -Path $runDir | Out-Null
foreach ($name in @('stop','outage')) { $path = Join-Path $runDir $name; if (Test-Path -LiteralPath $path) { Remove-Item -LiteralPath $path } }
$workDir = [IO.Path]::GetFullPath((Join-Path $runDir ('work-' + [guid]::NewGuid().ToString('N'))))
$allowedRoot = [IO.Path]::GetFullPath($runDir) + [IO.Path]::DirectorySeparatorChar
if (!$workDir.StartsWith($allowedRoot, [StringComparison]::OrdinalIgnoreCase)) { throw 'Work directory is outside the acceptance run directory.' }
$container = 'fleetops-p405-' + [guid]::NewGuid().ToString('N')
$started = $false
function Assert-Exit([string]$Step) { if ($LASTEXITCODE -ne 0) { throw "$Step failed with exit code $LASTEXITCODE" } }
try {
    Push-Location $repoRoot
    try {
        npm --prefix agent run build
        Assert-Exit 'Build agent'
        npm --prefix web run build
        Assert-Exit 'Build React dashboard'
    } finally { Pop-Location }
    New-Item -ItemType Directory -Path $workDir | Out-Null
    Copy-Item -LiteralPath (Join-Path $repoRoot 'backend') -Destination $workDir -Recurse
    $testBackend = Join-Path $workDir 'backend'
    Copy-Item -LiteralPath (Join-Path $repoRoot 'web/scripts/go-dashboard-integration_test.go') -Destination (Join-Path $testBackend 'fleet/p404_dashboard_integration_test.go')
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'demo_test.go') -Destination (Join-Path $testBackend 'fleet/p405_demo_test.go')
    docker run --detach --rm --name $container --publish '127.0.0.1::5432' --env POSTGRES_HOST_AUTH_METHOD=trust --env POSTGRES_DB=fleetops_p405 postgres:18.6 | Out-Null
    Assert-Exit 'Start isolated PostgreSQL'; $started = $true
    $ready = $false
    for ($i=0; $i -lt 40; $i++) {
        docker exec $container pg_isready -U postgres -d fleetops_p405 *> $null
        if ($LASTEXITCODE -eq 0) { $ready = $true; break }
        Start-Sleep -Milliseconds 250
    }
    if (!$ready) { throw 'Isolated PostgreSQL did not become ready.' }
    $dbPort = ((docker port $container 5432/tcp) -split ':')[-1].Trim()
    @{ container = $container; database = 'fleetops_p405' } | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $runDir 'infrastructure.json')
    $env:FLEETOPS_TEST_DATABASE_URL = "postgres://postgres@127.0.0.1:$dbPort/fleetops_p405?sslmode=disable"
    $env:FLEETOPS_REQUIRE_DB = '1'
    $env:FLEETOPS_P405_ROOT = $repoRoot
    $env:FLEETOPS_P405_RUN_DIR = $runDir
    $env:FLEETOPS_P405_SUBMIT = '1'
    $env:FLEETOPS_P405_CHECK_ONLY = if ($CheckOnly) { '1' } else { '0' }
    Write-Output 'MODEL MODE: deterministic local Mastra adapter. NOT a live LLM demonstration. Paid requests: disabled.'
    Write-Output 'Private session credentials are written under evals/dist/p405; do not print or commit them.'
    Write-Output 'For the interactive run, follow docs/phase4-demo.md. Only a human operator should click Approve.'
    Push-Location $testBackend
    try {
        go test -tags fleetops_test ./fleet -run '^TestPhase4AcceptanceDemo$' -count=1 -v -timeout 25m
        Assert-Exit 'Phase 4 local acceptance demo'
    } finally { Pop-Location }
} finally {
    if ($started) { docker stop $container | Out-Null }
    $privateFile = Join-Path $runDir 'session.private.json'
    if (Test-Path -LiteralPath $privateFile) { Remove-Item -LiteralPath $privateFile }
    if (Test-Path -LiteralPath $workDir) {
        $resolvedWorkDir = (Resolve-Path -LiteralPath $workDir).Path
        if (!$resolvedWorkDir.StartsWith($allowedRoot, [StringComparison]::OrdinalIgnoreCase)) { throw 'Refusing cleanup outside acceptance work directory.' }
        Remove-Item -LiteralPath $resolvedWorkDir -Recurse -Force
    }
}
