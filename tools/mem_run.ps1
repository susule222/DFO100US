param(
    [string]$Exe,
    [string[]]$Args,
    [string]$WorkDir = "."
)
$p = Start-Process -FilePath $Exe -ArgumentList $Args -WorkingDirectory $WorkDir -WindowStyle Hidden -PassThru -RedirectStandardOutput "$env:TEMP\mem_run.out" -RedirectStandardError "$env:TEMP\mem_run.err"
$peak = 0
while (-not $p.HasExited) {
    Start-Sleep -Milliseconds 300
    try { $ws = $p.WorkingSet64 } catch { continue }
    if ($ws -gt $peak) { $peak = $ws }
}
$p.WaitForExit()
Write-Output ("exit={0} peak_ws_mb={1} final_ws_mb={2}" -f $p.ExitCode, [math]::Round($peak/1MB), [math]::Round($p.WorkingSet64/1MB))
Get-Content "$env:TEMP\mem_run.out" -Tail 3 -ErrorAction SilentlyContinue
Get-Content "$env:TEMP\mem_run.err" -Tail 5 -ErrorAction SilentlyContinue
