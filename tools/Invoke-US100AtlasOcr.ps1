param([Parameter(Mandatory)][string]$Directory)

# Run in Windows PowerShell 5.1: WinRT task interop is not the same in PS7.
$ErrorActionPreference='Stop'
Add-Type -AssemblyName System.Runtime.WindowsRuntime
$null=[Windows.Media.Ocr.OcrEngine,Windows.Foundation,ContentType=WindowsRuntime]
$null=[Windows.Globalization.Language,Windows.Globalization,ContentType=WindowsRuntime]
$null=[Windows.Storage.StorageFile,Windows.Storage,ContentType=WindowsRuntime]
$null=[Windows.Storage.Streams.IRandomAccessStream,Windows.Storage.Streams,ContentType=WindowsRuntime]
$null=[Windows.Graphics.Imaging.BitmapDecoder,Windows.Graphics.Imaging,ContentType=WindowsRuntime]
$null=[Windows.Graphics.Imaging.SoftwareBitmap,Windows.Graphics.Imaging,ContentType=WindowsRuntime]
$taskMethod=[System.WindowsRuntimeSystemExtensions].GetMethods() |
    Where-Object { $_.Name -eq 'AsTask' -and $_.IsGenericMethodDefinition -and
       $_.GetParameters().Count -eq 1 -and $_.GetParameters()[0].ParameterType.Name -eq 'IAsyncOperation`1' } |
    Select-Object -First 1
function Wait-Operation($Action,[Type]$Result) {
    $t=$taskMethod.MakeGenericMethod($Result).Invoke($null,@($Action))
    $t.GetAwaiter().GetResult()
}

$root=(Get-Item -LiteralPath $Directory).FullName
$en=[Windows.Globalization.Language]::new('en-US')
$engine=[Windows.Media.Ocr.OcrEngine]::TryCreateFromLanguage($en)
if ($null -eq $engine) { throw 'Windows en-US OCR is unavailable' }
$done=0
$errors=@()
foreach ($f in (Get-ChildItem -LiteralPath $root -Filter 'sheet-*.png' -File)) {
    try {
        $sf=Wait-Operation ([Windows.Storage.StorageFile]::GetFileFromPathAsync($f.FullName)) ([Windows.Storage.StorageFile])
        $stream=Wait-Operation ($sf.OpenAsync([Windows.Storage.FileAccessMode]::Read)) ([Windows.Storage.Streams.IRandomAccessStream])
        try {
            $decoder=Wait-Operation ([Windows.Graphics.Imaging.BitmapDecoder]::CreateAsync($stream)) ([Windows.Graphics.Imaging.BitmapDecoder])
            $bmp=Wait-Operation ($decoder.GetSoftwareBitmapAsync([Windows.Graphics.Imaging.BitmapPixelFormat]::Bgra8,
                    [Windows.Graphics.Imaging.BitmapAlphaMode]::Ignore)) ([Windows.Graphics.Imaging.SoftwareBitmap])
            try {
                $result=Wait-Operation ($engine.RecognizeAsync($bmp)) ([Windows.Media.Ocr.OcrResult])
                $lines=@(foreach($l in $result.Lines) { ,@(foreach($w in $l.Words) {
                    $b=$w.BoundingRect
                    [pscustomobject]@{text=$w.Text; x=$b.X; y=$b.Y; w=$b.Width; h=$b.Height}
                }) })
                $row=[pscustomobject]@{sheet=$f.BaseName; lines=$lines}
                $row | ConvertTo-Json -Depth 7 -Compress | Set-Content -LiteralPath (Join-Path $root ($f.BaseName+'.ocr.json')) -Encoding UTF8
                $done++
            } finally { if ($bmp) {$bmp.Dispose()} }
        } finally { if ($stream) {$stream.Dispose()} }
    } catch {
        $errors += [pscustomobject]@{sheet=$f.BaseName; error=$_.Exception.Message}
    }
}
[pscustomobject]@{engine='Windows.Media.Ocr en-US'; completed_sheets=$done; errors=$errors} |
    ConvertTo-Json -Depth 4 | Set-Content -LiteralPath (Join-Path $root 'ocr-summary.json') -Encoding UTF8
"OCR completed: $done; failed: $($errors.Count)"
if ($errors.Count) { $errors | Select-Object -First 4; exit 1 }
