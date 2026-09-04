param([Parameter(ValueFromRemainingArguments=$true)][string[]]$ConnectorArgs)
$ErrorActionPreference = 'Stop'
$version = 'v0.2.2'
# Native architecture, including an x64 process running on Windows ARM.
$native = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()
$arch = switch ($native) { 'Arm64' { 'arm64' } 'X64' { 'amd64' } default { throw 'Unsupported Windows CPU' } }
$asset = "agentx-connect_windows_$arch.exe"
$base = "https://github.com/Lingbo-Huang/agentx-connect/releases/download/$version"
$stage = Join-Path ([IO.Path]::GetTempPath()) ([Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $stage | Out-Null
try {
    # curl.exe is provided by supported Windows versions and enforces HTTPS redirects.
    foreach ($name in @('SHA256SUMS', $asset)) {
        & curl.exe --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --max-redirs 3 --connect-timeout 15 --max-time 180 "$base/$name" -o (Join-Path $stage $name)
        if ($LASTEXITCODE -ne 0) { throw 'Release download failed' }
    }
    $lines = @(Get-Content (Join-Path $stage 'SHA256SUMS') | Where-Object { ($_ -split '\s+')[1] -ceq $asset })
    if ($lines.Count -ne 1) { throw 'Missing or ambiguous checksum' }
    $expected = ($lines[0] -split '\s+')[0]
    $binary = Join-Path $stage $asset
    if ((Get-FileHash $binary -Algorithm SHA256).Hash.ToLowerInvariant() -cne $expected) { throw 'Checksum mismatch' }
    & $binary @ConnectorArgs
    $result = $LASTEXITCODE
} finally { Remove-Item -LiteralPath $stage -Recurse -Force }
exit $result
