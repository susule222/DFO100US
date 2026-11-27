param(
    [int]$Port = 18765,
    [int]$Workers = 96,
    [string]$Previous = 'r10',
    [string]$Version = 'r11',
    [int]$BatchSessionPid
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
$stats = Get-Content -Raw -LiteralPath $ready | ConvertFrom-Json
if ($stats.tracked_translations_exhausted) {
    'No outstanding tracked values; no residual archive needed.'
    exit 0
}
$direct = "$mt/hy-residual-whole-value.json"
$merged = "$mt/hy-residual-combined.json"
if (-not (Test-Path -LiteralPath $direct)) {
    $argsForModel = @(
        '--missing', "$path/untranslated.jsonl", '--additional', "$path/xui-untranslated.jsonl",
        '--references', "$path/verification.json", '--cache', "$mt/hy-residual-whole-value.sqlite3",
        '--out', $direct, '--port', "$Port", '--workers', "$Workers", '--translate'
    )
    & python tools/us100_local_llm.py @argsForModel
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}
if (-not (Test-Path -LiteralPath $merged)) {
    python tools/us100_merge_model_overlays.py --first "$mt/overlay-gate-$Previous.json" --second $direct --output $merged
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}
& "$PSScriptRoot/pack_us100_localization.ps1" -Version $Version -Overlay $merged
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
