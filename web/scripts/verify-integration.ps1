$ErrorActionPreference = 'Stop'
$webRoot = Split-Path $PSScriptRoot -Parent
$repoRoot = Split-Path $webRoot -Parent
if ((node --version) -ne 'v24.21.0') { throw 'Node.js 24.21.0 is required.' }
$container = 'fleetops-p404-' + [guid]::NewGuid().ToString('N')
$distRoot = [IO.Path]::GetFullPath((Join-Path $webRoot 'dist'))
$taskRoot = [IO.Path]::GetFullPath((Join-Path $distRoot ('integration-' + [guid]::NewGuid().ToString('N'))))
if (!$taskRoot.StartsWith($distRoot + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) { throw 'Integration workspace is outside web/dist.' }
$started = $false
function Assert-Exit([string]$Step) { if ($LASTEXITCODE -ne 0) { throw "$Step failed with exit code $LASTEXITCODE" } }
try {
    Push-Location $webRoot
    try {
        npm exec -- vite build --ssr scripts/contract-entry.ts --outDir dist/contract
        Assert-Exit 'Build frontend HTTP/React contract bundle'
    } finally { Pop-Location }
    New-Item -ItemType Directory -Path $taskRoot | Out-Null
    Copy-Item -LiteralPath (Join-Path $repoRoot 'backend') -Destination $taskRoot -Recurse
    $testBackend = Join-Path $taskRoot 'backend'
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'go-dashboard-integration_test.go') -Destination (Join-Path $testBackend 'fleet/p404_dashboard_integration_test.go')
    docker run --detach --rm --name $container --publish '127.0.0.1::5432' --env POSTGRES_HOST_AUTH_METHOD=trust --env POSTGRES_DB=fleetops_p404 postgres:18.6 | Out-Null
    Assert-Exit 'Start isolated PostgreSQL'; $started = $true
    $ready = $false
    for ($i=0; $i -lt 40; $i++) {
        docker exec $container pg_isready -U postgres -d fleetops_p404 *> $null
        if ($LASTEXITCODE -eq 0) { $ready = $true; break }
        Start-Sleep -Milliseconds 250
    }
    if (!$ready) { throw 'Isolated PostgreSQL did not become ready.' }
    $dbPort = ((docker port $container 5432/tcp) -split ':')[-1].Trim()
    $env:FLEETOPS_TEST_DATABASE_URL = "postgres://postgres@127.0.0.1:$dbPort/fleetops_p404?sslmode=disable"
    $env:FLEETOPS_REQUIRE_DB = '1'
    $env:FLEETOPS_P404_WEB_ROOT = $webRoot
    Push-Location $testBackend
    try {
        go test -tags fleetops_test ./fleet -run '^TestDashboardHTTPIntegration$' -count=1 -v -timeout 2m
        Assert-Exit 'Frontend real Go/PostgreSQL integration'
    } finally { Pop-Location }
} finally {
    if ($started) { docker stop $container | Out-Null }
    if (Test-Path -LiteralPath $taskRoot) {
        $resolvedTaskRoot = (Resolve-Path -LiteralPath $taskRoot).Path
        if (!$resolvedTaskRoot.StartsWith($distRoot + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) { throw 'Refusing cleanup outside web/dist.' }
        Remove-Item -LiteralPath $resolvedTaskRoot -Recurse -Force
    }
}
