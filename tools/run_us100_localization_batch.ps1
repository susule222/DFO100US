param(
    [int]$Port = 18765,
    [int]$Workers = 96,
    [string]$FirstVersion = 'r9',
    [string]$RetryVersion = 'r10'
)
$ErrorActionPreference = 'Stop'
Set-Location -LiteralPath (Split-Path -Parent $PSScriptRoot)
$env:PYTHONUTF8 = '1'
$mt = 'runtime/localize-us100-mt'
$previous = 'runtime/localize-us100-zh-CN-r6'
$stop = "$mt/pause-translation"
$common = @(
    '--missing', "$previous/untranslated.jsonl",
    '--references', "$previous/verification.json",
    '--additional', 'runtime/localize-us100-xui-r1/untranslated.jsonl',
    '--preview', "$mt/hy-mt2-pilot.json",
    '--seed', "$previous/translations.jsonl",
    '--cache', "$mt/hy-grouped.sqlite3",
    '--port', "$Port", '--workers', "$Workers", '--translate',
    '--stop-file', $stop
)
$first = "$mt/hy-grouped-complete-pass.json"
$retry = "$mt/hy-grouped-retry-pass.json"

if (Test-Path -LiteralPath $stop) {
    throw "The translation queue is paused: $stop"
}
if (-not (Test-Path -LiteralPath $first)) {
    & python tools/us100_grouped_llm.py @common --out $first
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}
if (Test-Path -LiteralPath $stop) {
    throw 'Paused after checkpoint. Cache remains intact.'
}
if (-not (Test-Path -LiteralPath "runtime/localize-us100-zh-CN-$FirstVersion/Script.manifest.json")) {
    & "$PSScriptRoot/pack_us100_localization.ps1" -Version $FirstVersion -Overlay $first
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}
if (-not (Test-Path -LiteralPath $retry)) {
    & python tools/us100_grouped_llm.py @common --out $retry --retry
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}
if (Test-Path -LiteralPath $stop) {
    throw 'Paused after retry checkpoint. Cache remains intact.'
}
if (-not (Test-Path -LiteralPath "runtime/localize-us100-zh-CN-$RetryVersion/Script.manifest.json")) {
    & "$PSScriptRoot/pack_us100_localization.ps1" -Version $RetryVersion -Overlay $retry
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}
$stats = Get-Content -Raw -LiteralPath "runtime/localize-us100-zh-CN-$RetryVersion/coverage.json" | ConvertFrom-Json
$xuiStats = Get-Content -Raw -LiteralPath "runtime/localize-us100-zh-CN-$RetryVersion/xui-coverage.json" | ConvertFrom-Json
"Packaged $RetryVersion : str_remaining=$($stats.totals.untranslated), xui_remaining=$($xuiStats.untranslated)"
if ($stats.totals.untranslated -or $xuiStats.untranslated) {
    'Unresolved rows still require localization; this is not a full translation.'
}
