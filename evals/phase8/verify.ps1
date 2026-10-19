param([switch]$Serve, [switch]$Race, [switch]$AffectedOnly)
$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path (Split-Path $PSScriptRoot -Parent) -Parent
$runRoot = Join-Path $repoRoot 'evals/dist/pub01'
$runDir = Join-Path $runRoot ([guid]::NewGuid().ToString('N'))
$workDir = Join-Path $runDir 'work'
$container = 'fleetops-pub01-' + [guid]::NewGuid().ToString('N')
$started = $false
$passed = $false
function Assert-Exit([string]$step) { if ($LASTEXITCODE -ne 0) { throw "$step failed with exit code $LASTEXITCODE" } }
if ((node --version) -ne 'v24.21.0') { throw 'Node.js 24.21.0 is required.' }
New-Item -ItemType Directory -Path $workDir -Force | Out-Null
try {
    npm --prefix (Join-Path $repoRoot 'agent') run build
    Assert-Exit 'Build deterministic agent'
    npm --prefix (Join-Path $repoRoot 'web') run build
    Assert-Exit 'Build dashboard'
    Copy-Item -LiteralPath (Join-Path $repoRoot 'backend') -Destination $workDir -Recurse
    $backend = Join-Path $workDir 'backend'
    # Go emits LF JSON; a Windows checkout can hold CRLF golden files. Normalize
    # only the disposable copies, without regenerating any simulator values.
    foreach ($golden in @('golden_seed42_tick200.json', 'golden_warehouse_seed42_tick300.json')) {
        $goldenPath = Join-Path $backend ('sim/testdata/' + $golden)
        $goldenText = [IO.File]::ReadAllText($goldenPath).Replace("`r`n", "`n")
        [IO.File]::WriteAllText($goldenPath, $goldenText, (New-Object Text.UTF8Encoding($false)))
    }
    Copy-Item -LiteralPath (Join-Path $repoRoot 'web/scripts/go-dashboard-integration_test.go') -Destination (Join-Path $backend 'fleet/p404_dashboard_integration_test.go')
    foreach ($file in Get-ChildItem -LiteralPath (Join-Path $repoRoot 'evals/phase4') -Filter '*.go') {
        Copy-Item -LiteralPath $file.FullName -Destination (Join-Path $backend ('fleet/p405_' + $file.Name))
    }
    foreach ($file in Get-ChildItem -LiteralPath $PSScriptRoot -Filter '*.go') {
        Copy-Item -LiteralPath $file.FullName -Destination (Join-Path $backend ('fleet/pub01_' + $file.Name))
    }
    docker run --detach --rm --name $container --publish '127.0.0.1::5432' --env POSTGRES_HOST_AUTH_METHOD=trust --env POSTGRES_DB=fleetops_pub01 postgres:18.6 | Out-Null
    Assert-Exit 'Start isolated PostgreSQL'
    $started = $true
    $ready = $false
    for ($i = 0; $i -lt 60; $i++) {
        docker exec $container pg_isready -U postgres -d fleetops_pub01 *> $null
        if ($LASTEXITCODE -eq 0) { $ready = $true; break }
        Start-Sleep -Milliseconds 250
    }
    if (!$ready) { throw 'PostgreSQL did not become ready.' }
    $dbPort = ((docker port $container 5432/tcp) -split ':')[-1].Trim()
    $env:FLEETOPS_TEST_DATABASE_URL = "postgres://postgres@127.0.0.1:$dbPort/fleetops_pub01?sslmode=disable"
    $env:FLEETOPS_REQUIRE_DB = '1'
    $env:FLEETOPS_P405_ROOT = $repoRoot
    $env:FLEETOPS_P404_WEB_ROOT = Join-Path $repoRoot 'web'
    $env:FLEETOPS_PUB01_RUN_DIR = $runDir
    $env:FLEETOPS_PUB01_SERVE = if ($Serve) { '1' } else { '0' }
    Push-Location $backend
    try {
        $goTestArgs = @('-tags', 'fleetops_test', './...', '-skip', '^Test(Phase4AcceptanceDemo|DashboardHTTPIntegration)$', '-count=1', '-timeout', '10m')
        if ($AffectedOnly) { $goTestArgs = @('-tags', 'fleetops_test', './fleet', '-run', '^Test(PUB01|Demo)', '-count=1', '-timeout', '5m') }
        if ($AffectedOnly -and $Race) { $goTestArgs = @('-tags', 'fleetops_test', './fleet', '-run', '^TestPUB01', '-v', '-count=1', '-timeout', '5m') }
        if ($Serve) {
            go test -tags fleetops_test ./fleet -run '^TestPUB01Serve$' -v -count=1 -timeout 25m
        } elseif ($Race) {
            $moduleCache = go env GOMODCACHE
            Assert-Exit 'Locate cached Go modules'
            $nodeContainer = 'fleetops-pub01-node-' + [guid]::NewGuid().ToString('N')
            docker create --name $nodeContainer node:24.21.0 | Out-Null
            Assert-Exit 'Select cached Linux Node runtime'
            try {
                docker cp ($nodeContainer + ':/usr/local/bin/node') (Join-Path $runDir 'node')
                Assert-Exit 'Copy cached Linux Node runtime'
            } finally { docker rm $nodeContainer | Out-Null }
            docker run --rm --network ('container:' + $container) --mount "type=bind,source=$backend,target=/work" --mount "type=bind,source=$repoRoot,target=/repo,readonly" --mount "type=bind,source=$moduleCache,target=/gomodcache,readonly" --mount "type=bind,source=$(Join-Path $runDir 'node'),target=/usr/local/bin/node,readonly" --workdir /work --env CGO_ENABLED=1 --env GOTOOLCHAIN=local --env GOPROXY=off --env GOSUMDB=off --env GOMODCACHE=/gomodcache --env FLEETOPS_TEST_DATABASE_URL=postgres://postgres@127.0.0.1:5432/fleetops_pub01?sslmode=disable --env FLEETOPS_REQUIRE_DB=1 --env FLEETOPS_P405_ROOT=/repo --env FLEETOPS_P404_WEB_ROOT=/repo/web golang:1.27.1 go test -race @goTestArgs
        } else {
            go test @goTestArgs
        }
        Assert-Exit 'PUB-01 Go/PostgreSQL verification'
        $passed = $true
    } finally { Pop-Location }
} finally {
    foreach ($name in @('FLEETOPS_TEST_DATABASE_URL','FLEETOPS_REQUIRE_DB','FLEETOPS_P405_ROOT','FLEETOPS_P404_WEB_ROOT','FLEETOPS_PUB01_RUN_DIR','FLEETOPS_PUB01_SERVE')) {
        Remove-Item -LiteralPath "Env:$name" -ErrorAction SilentlyContinue
    }
    if ($started -and $passed) { docker stop $container | Out-Null }
    if ($started -and !$passed) { Write-Warning "Failed verification retained container $container and evidence at $runDir for reconciliation." }
    $linuxNode = Join-Path $runDir 'node'
    if (Test-Path -LiteralPath $linuxNode) { Remove-Item -LiteralPath $linuxNode -Force }
    # Retain logs/evidence. Only the verified temporary source copy is disposable.
    if ($passed) {
        $resolvedWorkDir = (Resolve-Path -LiteralPath $workDir).Path
        $allowedRoot = [IO.Path]::GetFullPath($runRoot) + [IO.Path]::DirectorySeparatorChar
        if (!$resolvedWorkDir.StartsWith($allowedRoot, [StringComparison]::OrdinalIgnoreCase)) { throw 'Refusing cleanup outside PUB-01 work root.' }
        Remove-Item -LiteralPath $resolvedWorkDir -Recurse -Force
    }
}
