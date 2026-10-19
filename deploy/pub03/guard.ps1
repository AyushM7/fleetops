param(
    [Parameter(Mandatory)][string]$Name,
    [Parameter(Mandatory)][string]$Executable,
    [string[]]$CommandArguments = @(),
    [Parameter(Mandatory)][string]$EvidenceDirectory,
    [int]$TimeoutSeconds = 600
)
$ErrorActionPreference = 'Stop'
# Stop early enough to preserve the user's five GiB reserve.
$reserve = 5.25GB
$minimum = (Get-PSDrive C).Free
if ($minimum -lt $reserve) { throw 'Insufficient free space for verification.' }
New-Item -ItemType Directory -Force -Path $EvidenceDirectory | Out-Null
$start = [DateTimeOffset]::UtcNow
$info = [Diagnostics.ProcessStartInfo]::new()
$info.FileName = $Executable
$info.UseShellExecute = $false
$info.CreateNoWindow = $true
$info.RedirectStandardOutput = $true
$info.RedirectStandardError = $true
foreach ($argument in $CommandArguments) { [void]$info.ArgumentList.Add($argument) }
$process = [Diagnostics.Process]::new()
$process.StartInfo = $info
[void]$process.Start()
$stdout = $process.StandardOutput.ReadToEndAsync()
$stderr = $process.StandardError.ReadToEndAsync()
$interrupted = $false
while (!$process.WaitForExit(1000)) {
    $minimum = [Math]::Min($minimum, (Get-PSDrive C).Free)
    if ($minimum -lt $reserve -or ([DateTimeOffset]::UtcNow - $start).TotalSeconds -gt $TimeoutSeconds) {
        $process.Kill($true)
        $process.WaitForExit()
        $interrupted = $true
        break
    }
}
$minimum = [Math]::Min($minimum, (Get-PSDrive C).Free)
$out = $stdout.GetAwaiter().GetResult()
$err = $stderr.GetAwaiter().GetResult()
[IO.File]::WriteAllText((Join-Path $EvidenceDirectory "$Name.stdout.log"), $out)
[IO.File]::WriteAllText((Join-Path $EvidenceDirectory "$Name.stderr.log"), $err)
$result = @{name=$Name; executable=$Executable; arguments=$CommandArguments; startedAt=$start.ToString('o'); finishedAt=[DateTimeOffset]::UtcNow.ToString('o'); exitCode=$process.ExitCode; interrupted=$interrupted; minimumFreeBytes=$minimum}
$result | ConvertTo-Json -Depth 5 | Set-Content (Join-Path $EvidenceDirectory "$Name.result.json")
$result | ConvertTo-Json -Compress
$out
$err
if ($interrupted) { exit 99 }
exit $process.ExitCode
