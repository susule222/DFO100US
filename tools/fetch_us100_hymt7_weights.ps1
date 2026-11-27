param(
    [ValidateRange(1, 8)][int]$Workers = 4,
    [ValidateSet('Hy7', 'Qwen8')][string]$Model = 'Hy7'
)

$ErrorActionPreference = 'Stop'
$workspace = Split-Path -Parent $PSScriptRoot
$cache = Join-Path $workspace 'runtime/localize-us100-mt'
$size = 4624648896L
$partPrefix = 'hy7'
$name = 'Hy-MT2-7B-Q4_K_M.gguf'
$sha = '9f96256500f3fc1ab4d64336b58f52a949a95ad7516b0c229476eef782f9f77b'
$mirror = 'https://hf-mirror.com/tencent/Hy-MT2-7B-GGUF/resolve/main/Hy-MT2-7B-Q4_K_M.gguf?download=true'
$cdnHost = 'cas-bridge.xethub.hf.co'
if ($Model -eq 'Qwen8') {
    $size = 5027783488L
    $partPrefix = 'qwen8'
    $name = 'Qwen3-8B-Q4_K_M.gguf'
    $sha = 'd98cdcbd03e17ce47681435b5150e34c1417f50b5c0019dd560e4882c5745785'
    $mirror = 'https://modelscope.cn/models/Qwen/Qwen3-8B-GGUF/resolve/master/Qwen3-8B-Q4_K_M.gguf'
    $cdnHost = 'cdn-lfs-cn-1.modelscope.cn'
}
$final = Join-Path $cache $name
$partSize = 64MB
$last = [int][math]::Floor(($size - 1) / $partSize)
if (Test-Path -LiteralPath $final) {
    if ((Get-FileHash -LiteralPath $final).Hash -ne $sha) { throw 'Existing model checksum does not match' }
    Get-Item -LiteralPath $final
    return
}
$headers = if ($Model -eq 'Qwen8') {
    & curl.exe --range 0-1 --max-filesize 4096 --dump-header - --output NUL --silent --show-error --connect-timeout 12 --max-time 20 $mirror
} else {
    & curl.exe --head --fail --silent --show-error --connect-timeout 12 --max-time 20 $mirror
}
$location = ($headers |
    Select-String '^location:\s+' | Select-Object -First 1).Line
if (-not $location) { throw 'Model mirror did not return a signed download URL' }
$url = ($location -replace '^[^:]+:\s+', '').Trim()
$signed = [uri]$url
if ($signed.Scheme -ne 'https' -or $signed.Host -ne $cdnHost) {
    throw 'Unexpected model download host'
}
$parts = 0..$last | ForEach-Object {
    $start = [long]$_ * $partSize
    $end = [math]::Min($start + $partSize, $size) - 1
    [pscustomobject]@{
        Start = $start; End = $end; Count = $end - $start + 1
        Path = Join-Path $cache ('{0}-range-{1:D3}.part' -f $partPrefix, $_)
    }
}
$download = $parts | ForEach-Object -ThrottleLimit $Workers -Parallel {
    $ErrorActionPreference = 'Stop'
    $PSNativeCommandUseErrorActionPreference = $false
    if (-not (Test-Path -LiteralPath $_.Path) -or (Get-Item -LiteralPath $_.Path).Length -ne $_.Count) {
        $span = "$($_.Start)-$($_.End)"
        $code = & curl.exe --fail --location --range $span --max-filesize $_.Count --retry 3 --connect-timeout 15 --max-time 120 --silent --show-error --output $_.Path --write-out '%{http_code}' $using:url
        if ($LASTEXITCODE -ne 0 -or $code -ne '206') {
            throw "Download failed for byte range $span (HTTP $code)"
        }
    }
    if ((Get-Item -LiteralPath $_.Path).Length -ne $_.Count) {
        throw "Range response size mismatch: $($_.Path)"
    }
    $_.Path
}
if ($download.Count -ne $parts.Count) { throw 'Download incomplete' }
$assembly = "$final.assembly-$PID"
$output = [System.IO.File]::Open($assembly, 'CreateNew', 'Write', 'None')
try {
    foreach ($part in $parts) {
        $partStream = [System.IO.File]::OpenRead($part.Path)
        try { $partStream.CopyTo($output, 1MB) } finally { $partStream.Dispose() }
    }
} finally {
    $output.Dispose()
}
if ((Get-Item -LiteralPath $assembly).Length -ne $size -or (Get-FileHash -LiteralPath $assembly).Hash -ne $sha) {
    throw 'Reassembled model does not match the publisher size and SHA-256'
}
if (Test-Path -LiteralPath $final) { throw 'Model destination was created by another process' }
Move-Item -LiteralPath $assembly -Destination $final
Get-Item -LiteralPath $final
