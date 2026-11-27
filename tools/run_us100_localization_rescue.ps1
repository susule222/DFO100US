param(
    [int]$Port = 18765,
    [int]$Workers = 96,
    [string]$Previous = 'r11a',
    [string]$Version = 'r12',
    [int]$BatchSessionPid,
    [switch]$PreferPlain,
    [switch]$RetryFailed,
    [switch]$IsolateFormat,
    [switch]$ClauseTails,
    [switch]$ExplicitLayout,
    [switch]$WholeValue,
    [switch]$ReviseInvalid,
    [string]$PhraseMemory
)
$ErrorActionPreference = 'Stop'
Set-Location -LiteralPath (Split-Path -Parent $PSScriptRoot)
$env:PYTHONUTF8 = '1'
$mt = 'runtime/localize-us100-mt'
$path = "runtime/localize-us100-zh-CN-$Previous"
$ready = "$path/completion-status.json"
if (-not (Test-Path -LiteralPath $ready) -and $BatchSessionPid -gt 0) {
    Wait-Process -Id $BatchSessionPid -Timeout 7200 -ErrorAction Stop
}
if (-not (Test-Path -LiteralPath $ready)) {
    throw "Prior batch has not produced a verified result: $path"
}
if ((Get-Content -Raw -LiteralPath $ready | ConvertFrom-Json).tracked_translations_exhausted) {
    'No outstanding tracked values.'
    exit 0
}
$direct = "$mt/rescue-$Version.json"
$merged = "$mt/rescue-combined-$Version.json"
$cache = if ($WholeValue) { "$mt/rescue-values-layout.sqlite3" } elseif ($ExplicitLayout) { "$mt/rescue-sentences-layout.sqlite3" } elseif ($IsolateFormat) { "$mt/rescue-sentences-isolated.sqlite3" } elseif ($PreferPlain) { "$mt/rescue-sentences-plain.sqlite3" } else { "$mt/rescue-sentences.sqlite3" }
if (-not (Test-Path -LiteralPath $direct)) {
    $argsForModel = @(
        '--missing', "$path/untranslated.jsonl", '--references', "$path/verification.json",
        '--history', "$mt/hy-grouped.sqlite3", '--cache', $cache,
        '--out', $direct, '--receipt', "$mt/rescue-$Version-receipt.json", '--port', "$Port",
        '--workers', "$Workers", '--translate'
    )
    if (Test-Path -LiteralPath "$mt/verified-game-terms.json") {
        $argsForModel += @('--terms', "$mt/verified-game-terms.json")
    }
    if ($PreferPlain) { $argsForModel += '--prefer-plain' }
    if ($RetryFailed) { $argsForModel += '--retry-failed' }
    if ($IsolateFormat) { $argsForModel += '--isolate-format' }
    if ($ClauseTails) { $argsForModel += '--clause-tails' }
    if ($ExplicitLayout) { $argsForModel += '--explicit-layout' }
    if ($WholeValue) { $argsForModel += '--whole-value' }
    if ($ReviseInvalid) { $argsForModel += '--revise-invalid' }
    if ($PhraseMemory) { $argsForModel += @('--phrase-memory', $PhraseMemory) }
    & python tools/us100_rescue_llm.py @argsForModel
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}
if (-not (Test-Path -LiteralPath $merged)) {
    & python tools/us100_merge_model_overlays.py --first "$mt/overlay-gate-$Previous.json" --second $direct --output $merged
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}
& "$PSScriptRoot/pack_us100_localization.ps1" -Version $Version -Overlay $merged -IncludeWisdomSimulator
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
