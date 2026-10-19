$ErrorActionPreference = 'Stop'
$agentRoot = Split-Path $PSScriptRoot -Parent
$repoRoot = Split-Path $agentRoot -Parent
if ((node --version) -ne 'v24.21.0') { throw 'Node.js 24.21.0 is required.' }
$container = 'fleetops-p400-' + [guid]::NewGuid().ToString('N')
$serverProcess = $null
$started = $false
function Assert-Exit([string]$Step) {
    if ($LASTEXITCODE -ne 0) { throw "$Step failed with exit code $LASTEXITCODE" }
}
function New-Token { return [Convert]::ToHexString([Security.Cryptography.RandomNumberGenerator]::GetBytes(32)).ToLowerInvariant() }
function Token-Hash([string]$Token) { return [Convert]::ToHexString([Security.Cryptography.SHA256]::HashData([Text.Encoding]::UTF8.GetBytes($Token))).ToLowerInvariant() }
try {
    New-Item -ItemType Directory -Force -Path "$agentRoot/dist" | Out-Null
    docker run --detach --rm --name $container --publish '127.0.0.1::5432' --env POSTGRES_HOST_AUTH_METHOD=trust --env POSTGRES_DB=fleetops_p400 postgres:18.6 | Out-Null
    Assert-Exit 'Start isolated PostgreSQL'
    $started = $true
    $ready = $false
    for ($i = 0; $i -lt 40; $i++) {
        docker exec $container pg_isready -U postgres -d fleetops_p400 *> $null
        if ($LASTEXITCODE -eq 0) { $ready = $true; break }
        Start-Sleep -Milliseconds 250
    }
    if (!$ready) { throw 'Isolated PostgreSQL did not become ready.' }
    $dbPort = ((docker port $container 5432/tcp) -split ':')[-1].Trim()
    $env:FLEETOPS_DATABASE_URL = "postgres://postgres@127.0.0.1:$dbPort/fleetops_p400?sslmode=disable"
    $env:FLEETOPS_TEST_DATABASE_URL = $env:FLEETOPS_DATABASE_URL
    $env:FLEETOPS_REQUIRE_DB = '1'
    $env:FLEETOPS_TEST_PROPOSER_TOKEN = New-Token
    $env:FLEETOPS_TEST_VIEWER_TOKEN = New-Token
    $credentials = @(
        @{ subject = 'p400-proposer'; role = 'proposer'; sha256 = (Token-Hash $env:FLEETOPS_TEST_PROPOSER_TOKEN) },
        @{ subject = 'p400-viewer'; role = 'viewer'; sha256 = (Token-Hash $env:FLEETOPS_TEST_VIEWER_TOKEN) }
    )
    $env:FLEETOPS_AUTH_TOKENS = ConvertTo-Json -Compress -InputObject $credentials
    $env:FLEETOPS_CHARGING_DEMO = '1'
    $env:FLEETOPS_AUTO_TICK = '0'
	$env:FLEETOPS_AGENT_FIXTURE_ENABLE = '1'
    $env:FLEETOPS_TICK_INTERVAL = '500ms'
    Push-Location "$repoRoot/backend"
    try {
        go build -tags fleetops_test -o "$agentRoot/dist/p400-server.exe" ./cmd/agentfixture
        Assert-Exit 'Build Go backend'
        # Serialize packages and t.Parallel tests sharing the migration lock; explicit concurrency tests still run their workers.
        # Git's Windows checkout uses CRLF for the byte-compared JSON goldens.
        # Normalize only an ephemeral container copy; repository fixtures stay untouched.
        docker run --rm --network "container:$container" --volume "${repoRoot}/backend:/workspace:ro" --workdir /workspace --env 'FLEETOPS_TEST_DATABASE_URL=postgres://postgres@127.0.0.1:5432/fleetops_p400?sslmode=disable' --env FLEETOPS_REQUIRE_DB=1 golang:1.27.1 sh -c 'cp -a /workspace /tmp/fleetops-test && cd /tmp/fleetops-test && sed -i "s/\r$//" sim/testdata/golden_*.json && go test -p 1 -parallel 1 -race -tags fleetops_test ./... -count=1'
        Assert-Exit 'Go integration tests'
    } finally { Pop-Location }
    $listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, 0)
    $listener.Start()
    $env:PORT = [string]$listener.LocalEndpoint.Port
    $listener.Stop()
    $env:FLEETOPS_TEST_GRAPHQL_URL = "http://127.0.0.1:$($env:PORT)/graphql"
    $serverProcess = Start-Process -FilePath "$agentRoot/dist/p400-server.exe" -WindowStyle Hidden -PassThru -RedirectStandardOutput "$agentRoot/dist/integration-server.log" -RedirectStandardError "$agentRoot/dist/integration-server.err.log"
    $healthy = $false
    for ($i = 0; $i -lt 40; $i++) {
        try { Invoke-WebRequest "http://127.0.0.1:$($env:PORT)/healthz" -TimeoutSec 1 | Out-Null; $healthy = $true; break } catch { Start-Sleep -Milliseconds 250 }
    }
    if (!$healthy) { throw 'Isolated backend did not become ready.' }
    Push-Location $agentRoot
    try {
        npm run build
        Assert-Exit 'Build TypeScript agent'
        node node_modules/typescript/bin/tsc -p tsconfig.fixtures.json
        Assert-Exit 'Build test-only fixture modules'
        npm run test:integration
        Assert-Exit 'Agent real-backend integration tests'
    } finally { Pop-Location }
    $env:FLEETOPS_AGENT_CONTRACT_CHECK = '1'
    Push-Location "$repoRoot/backend"
    try {
        go test ./graphql/... -run '^TestAgentIncidentHTTPClient$' -count=1 -v
        Assert-Exit 'Agent Go incident HTTP contract check'
        go test -tags fleetops_test ./fleet/... -run '^TestIntegrationGraphQLReassignmentRecovery$' -count=1 -v
        Assert-Exit 'Agent Go reassignment approval workflow check'
    } finally { Pop-Location }
} finally {
    if ($serverProcess -and !$serverProcess.HasExited) { Stop-Process -Id $serverProcess.Id }
    if ($started) { docker stop $container | Out-Null }
}
