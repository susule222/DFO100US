param(
    [Parameter(Mandatory)][ValidatePattern('^[a-zA-Z0-9-]+$')][string]$Version,
    [string]$Previous = 'runtime/localize-us100-zh-CN-r6',
    [Parameter(Mandatory)][string]$Overlay,
    [string]$Original = 'D:/DNF/client/100US/DFO/Script.pvf',
    [switch]$IncludeWisdomSimulator
)

$ErrorActionPreference = 'Stop'
Set-Location -LiteralPath (Split-Path -Parent $PSScriptRoot)
$env:PYTHONUTF8 = '1'
$destination = "runtime/localize-us100-zh-CN-$Version"
$xui = "runtime/localize-us100-xui-$Version"
$audit = 'runtime/localize-us100-audit'
$baseline = 'runtime/pvf_source/Script.inner.pvf'
$packer = Join-Path $audit 'pvflocalize-xui.exe'
$inlinePlan = Join-Path $audit 'wisdom-simulator-plan.json'
$inlineCoverage = Join-Path $audit 'wisdom-simulator-coverage-v2.json'
if ($IncludeWisdomSimulator -or $Version -eq 'r12') {
    $packer = Join-Path $audit 'pvflocalize-inline-writer.exe'
    $inlineInspector = Join-Path $audit 'pvfinspect-localization.exe'
    $inlineSources = @('--source', "$audit/wisdom-simulator-inline")
    if ((Test-Path -LiteralPath "$audit/inline-display-plan-v3.json") -and $Version -ne 'r12') {
        $packer = Join-Path $audit 'pvflocalize-inline-cinema.exe'
        $inlinePlan = Join-Path $audit 'inline-display-plan-v3.json'
        $inlineCoverage = Join-Path $audit 'inline-display-coverage-v3.json'
        $inlineSources += @('--source', "$audit/cinematic-inline")
    }
    $inlineArgs = @('-inline-plan', $inlinePlan)
    foreach ($precondition in @($inlinePlan, $inlineCoverage, $inlineInspector)) {
        if (-not (Test-Path -LiteralPath $precondition)) {
            throw "Missing audited inline localization source: $precondition"
        }
    }
} else {
    $inlineArgs = @()
}
$references = 'runtime/localize-us100-zh-CN/script-references.json'
$gate = "runtime/localize-us100-mt/overlay-gate-$Version"

foreach ($source in @($Previous, $Overlay, $Original, $baseline, $packer, $references,
                      "$audit/us-tables", "$audit/us-xui-raw", "$audit/us/00-n_string.lst.txt")) {
    if (-not (Test-Path -LiteralPath $source)) {
        throw "Missing input: $source"
    }
}
if ((Test-Path -LiteralPath $destination) -or (Test-Path -LiteralPath $xui)) {
    throw "Version $Version already exists; choose a new version."
}

$gateArgs = @('--source-file', $Overlay, '--out', "$gate.json", '--report', "$gate-report.json",
              '--rejected', "$gate-rejected.jsonl", '--pending-values', "$Previous/untranslated.jsonl")
if ($Version -eq 'r10' -and (Test-Path -LiteralPath 'runtime/localize-us100-mt/overlay-gate-r9.json')) {
    $gateArgs += @('--fallback', 'runtime/localize-us100-mt/overlay-gate-r9.json')
}
if ($Version -eq 'r15' -and (Test-Path -LiteralPath 'runtime/localize-us100-mt/phrase-reuse-r15.json')) {
    $gateArgs += @('--fallback', 'runtime/localize-us100-mt/phrase-reuse-r15.json')
}
& python tools/us100_filter_model_overlay.py @gateArgs
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
$stringArgs = @('--us', "$audit/us-tables", '--previous', $Previous, '--out', $destination, '--machine', "$gate.json")
if (Test-Path -LiteralPath "$audit/calibrated-reference-numbers.json") {
    $stringArgs += @('--calibrated', "$audit/calibrated-reference-numbers.json")
}
& python tools/extend_us100_localization.py @stringArgs
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
python tools/us100_xui_text.py --us "$audit/us-xui-raw" --reference "$audit/cn100-xui-raw" `
    --reference "$audit/cn115-xui-raw" --glossary tools/us100_zh_glossary.json `
    --glossary tools/us100_zh_supplement.json --glossary tools/us100_xui_glossary.json `
    --overlay "$gate.json" --out $xui
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
Copy-Item -Path "$xui/replacement-tables/*" -Destination "$destination/replacement-tables/" -Recurse
foreach ($name in @('coverage.json', 'untranslated.jsonl', 'translations.jsonl', 'preserved.jsonl')) {
    Copy-Item -LiteralPath "$xui/$name" -Destination "$destination/xui-$name"
}
Copy-Item -LiteralPath "$gate-report.json" -Destination "$destination/model-gate.json"
python tools/verify_us100_localization.py --original-dir "$audit/us-tables" `
    --candidate-dir "$destination/replacement-tables" --reference-file $references `
    --list-file "$audit/us/00-n_string.lst.txt" --output "$destination/verification.json"
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
& $packer -source $baseline -dir "$destination/replacement-tables" -output "$destination/Script.inner.pvf" @inlineArgs
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
if ($inlineArgs.Count -gt 0) {
    $inlineRows = Get-Content -Raw -LiteralPath $inlinePlan | ConvertFrom-Json -AsHashtable
    $scripts = $inlineRows.Keys -join ','
    & $inlineInspector -source "$destination/Script.inner.pvf" -output "$destination/inline-reopened" -files $scripts -tokens -find 'event/universal/'
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    python tools/us100_verify_inline.py @inlineSources --candidate "$destination/inline-reopened" --plan $inlinePlan --report "$destination/inline-verification.json"
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    Copy-Item -LiteralPath $inlineCoverage -Destination "$destination/inline-coverage.json"
}
python tools/rewrap_us100_pvf.py --original $Original --baseline $baseline `
    --candidate "$destination/Script.inner.pvf" --output "$destination/Script.pvf"
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
$receipt = Get-Content -Raw -LiteralPath "$destination/Script.manifest.json" | ConvertFrom-Json
if ((Get-FileHash -LiteralPath $receipt.source).Hash -ne $receipt.source_sha256 -or
    (Get-FileHash -LiteralPath $receipt.candidate).Hash -ne $receipt.candidate_sha256) {
    throw 'Source or candidate digest mismatch'
}
python tools/us100_residual_audit.py --candidate $destination --reference-file $references --list-file "$audit/us/00-n_string.lst.txt" --out "$destination/residual-audit.json" --groups "$destination/residual-by-source.jsonl"
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
python tools/us100_completion_status.py --candidate $destination --audit $audit --out "$destination/completion-status.json"
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
"Candidate verified in $destination; source unchanged."
