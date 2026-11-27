[CmdletBinding()]
param([Parameter(Mandatory)][string]$Out)

$ErrorActionPreference = 'Stop'
$root = (Get-Item -LiteralPath (Join-Path $PSScriptRoot '../runtime') -Force).FullName
$outPath = [IO.Path]::GetFullPath($Out)
if ([IO.Path]::GetDirectoryName($outPath) -ne $root -or [IO.Path]::GetFileName($outPath) -notmatch '^cleanup-smoke-[0-9a-z-]+$' -or (Test-Path -LiteralPath $outPath)) {
    throw 'Require a new cleanup-smoke-* evidence directory directly below runtime.'
}

function Assert-Regular([string]$path, [string]$parent) {
    $f=Get-Item -LiteralPath $path -Force
    if ($f.PSIsContainer -or $f.FullName -ne $path -or $f.DirectoryName -ne $parent) { throw "Unexpected target: $path" }
    $cur=$f
    while ($null -ne $cur) {
        if ($cur.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw "Reparse point: $($cur.FullName)" }
        $cur=if($cur.PSIsContainer){$cur.Parent}else{$cur.Directory}
    }
    return $f
}

Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
public static class US100Recycle {
  [ComImport, InterfaceType(ComInterfaceType.InterfaceIsIUnknown), Guid("947AAB5F-0A5C-4C13-B4D6-4BF7836FC9F8")]
  interface IFileOperation {
    void Slot01(); void Slot02();
    [PreserveSig] int SetOperationFlags(uint flags);
    void Slot04(); void Slot05(); void Slot06(); void Slot07(); void Slot08(); void Slot09();
    void Slot10(); void Slot11(); void Slot12(); void Slot13(); void Slot14(); void Slot15();
    [PreserveSig] int DeleteItem(IntPtr item, IntPtr sink);
    void Slot17(); void Slot18();
    [PreserveSig] int PerformOperations();
    [PreserveSig] int GetAnyOperationsAborted([MarshalAs(UnmanagedType.Bool)] out bool result);
  }
  [DllImport("shell32.dll", CharSet=CharSet.Unicode, ExactSpelling=true)]
  static extern int SHCreateItemFromParsingName(string path, IntPtr bind, ref Guid iid, out IntPtr item);
  [StructLayout(LayoutKind.Sequential)]
  struct BinInfo { public uint size; public long bytes; public long count; }
  [DllImport("shell32.dll", CharSet=CharSet.Unicode, ExactSpelling=true)]
  static extern int SHQueryRecycleBinW(string volume, ref BinInfo data);
  public static long[] Size(string volume) {
    BinInfo v = new BinInfo(); v.size=(uint)Marshal.SizeOf(typeof(BinInfo));
    Marshal.ThrowExceptionForHR(SHQueryRecycleBinW(volume,ref v));
    return new long[]{v.bytes,v.count};
  }
  public static void Recycle(string path) {
    var op=(IFileOperation)Activator.CreateInstance(Type.GetTypeFromCLSID(new Guid("3AD05575-8857-4850-9277-11B85BDB8E09")));
    IntPtr item=IntPtr.Zero;
    try {
      // RECYCLEONDELETE | EARLYFAILURE | WANTNUKEWARNING | NOERRORUI | ALLOWUNDO | SILENT
      Marshal.ThrowExceptionForHR(op.SetOperationFlags(0x00184444));
      var iid=new Guid("43826D1E-E718-42EE-BC55-A1E261C37BFE");
      Marshal.ThrowExceptionForHR(SHCreateItemFromParsingName(path,IntPtr.Zero,ref iid,out item));
      Marshal.ThrowExceptionForHR(op.DeleteItem(item,IntPtr.Zero));
      Marshal.ThrowExceptionForHR(op.PerformOperations());
      bool aborted; Marshal.ThrowExceptionForHR(op.GetAnyOperationsAborted(out aborted));
      if(aborted)throw new InvalidOperationException("Recycle operation aborted");
    } finally { if (item!=IntPtr.Zero)Marshal.Release(item); Marshal.FinalReleaseComObject(op); }
  }
}
'@

$spec = @(
    'us100-shop-checkout-r2|zip|before-UI.zip|UI.diff.json',
    'us100-tooltip-cleanup-r1|zip|before-UI.zip|UI.diff.json',
    'us100-equipfilter-pvf-r1|pvf|before-Script.pvf|PVF.diff.json',
    'us100-shop-load-r1|zip|before-UI.zip|UI.diff.json',
    'us100-clear-font-r1|zip|before-Fonts.zip|FONT.diff.json',
    'us100-create-load-fix-r1|zip|before-UI.zip|UI.diff.json',
    'us100-create-load-fix-r2|zip|before-UI.zip|UI.diff.json',
    'us100-shop-new-badge-r1|NPK|before-sprite_interface_cashshop.NPK|BADGE.diff.json',
    'us100-town-shop-r3|zip|before-UI.zip|UI.diff.json',
    'us100-contract-toast-r1|NPK|before-sprite_interface2_premiumservice.NPK|NPK.diff.json'
)
$spec+=@('us100-equipfilter-dstr-r1','us100-character-create-dstr-r1','us100-status-dstr-r1','us100-status-dstr-r2',
    'us100-shop-dstr-r1','us100-shop-dstr-r2','us100-equipment-tooltip-dstr-r1','us100-inventory-system-notices-r2',
    'us100-inventory-timed-items-r1') | ForEach-Object { $_+'|dat|before-dstr.dat|DSTR.diff.json' }

$plan=@(foreach($line in $spec){
    $run,$extension,$kept,$delta=$line.Split('|');$dir=Join-Path $root $run
    $target=Join-Path $dir ("rollback-test.$extension")
    if (-not (Test-Path -LiteralPath $target)) { continue }
    $file=Assert-Regular $target $dir
    $keep=Assert-Regular (Join-Path $dir $kept) $dir
    $side=Assert-Regular (Join-Path $dir $delta) $dir
    $null=Assert-Regular (Join-Path $dir 'ROLLBACK.sh') $dir
    $null=Assert-Regular (Join-Path $dir 'VERIFICATION.txt') $dir
    $sha=(Get-FileHash -Algorithm SHA256 -LiteralPath $file.FullName).Hash
    $manifest=Get-Content -LiteralPath $side.FullName -Raw | ConvertFrom-Json
    if ($file.Length -ne $keep.Length -or $sha -ne $manifest.before -or $sha -ne (Get-FileHash -Algorithm SHA256 -LiteralPath $keep.FullName).Hash) {
        throw "Not a verified byte-identical rollback test copy: $target"
    }
    [ordered]@{run=$run;path=$file.FullName;retained=$keep.FullName;bytes=$file.Length;sha256=$sha.ToLowerInvariant();ticks=$file.LastWriteTimeUtc.Ticks}
})
if ($plan.Count -eq 0) { throw 'No eligible rollback-test copies.' }

$client='D:\DNF\client\100US\DFO'
$protected=@(
    (Join-Path $client 'Script.pvf'), (Join-Path $client 'Script.pvf-1'), (Join-Path $client 'dstr.dat'),
    (Join-Path $client 'ImagePacks2/sprite_interface2_premiumservice.NPK'),
    (Join-Path $root 'us100-contract-toast-r1/sprite_interface2_premiumservice.NPK'),
    (Join-Path $root 'us100-inventory-system-notices-r2/dstr.dat')
)
$sentinels=@(Get-FileHash -Algorithm SHA256 -LiteralPath $protected)
$drive=[IO.Path]::GetPathRoot($root); $volume=Get-Volume -DriveLetter $drive.Substring(0,1)
$guid=[regex]::Match($volume.UniqueId,'\{[0-9a-fA-F-]+\}').Value
$setting=Get-ItemProperty -LiteralPath ('HKCU:\Software\Microsoft\Windows\CurrentVersion\Explorer\BitBucket\Volume\'+$guid)
$usage=[US100Recycle]::Size($drive)
$bytes=0L
foreach($item in $plan){$bytes+=[long]$item['bytes']}
$capacity=[long]$setting.MaxCapacity*1MB
if ($volume.FileSystem -ne 'NTFS' -or $setting.NukeOnDelete -ne 0 -or $capacity -lt ($usage[0]+$bytes+16MB)) { throw 'Recycle Bin capacity or policy check failed.' }
$sid=[System.Security.Principal.WindowsIdentity]::GetCurrent().User.Value
$bin=Join-Path (Join-Path $drive '$Recycle.Bin') $sid
$null=Get-Item -LiteralPath $bin -Force

[IO.Directory]::CreateDirectory($outPath)|Out-Null
$moved=@(); $log=New-Object 'System.Collections.Generic.List[string]'
function Add-Reg([string]$s){$log.Add($s);Write-Output $s}
$planPath=Join-Path $outPath 'CLEANUP.diff.json';$movedPath=Join-Path $outPath 'MOVED.json'
$verify=Join-Path $outPath 'VERIFICATION.txt';$rollback=Join-Path $outPath 'ROLLBACK.sh'
[ordered]@{root=$root;copies=$plan;protected=$sentinels;bytes=$bytes;recycleUsageBefore=$usage[0];recycleCapacity=$capacity} | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath $planPath -Encoding utf8NoBOM
Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'us100_cleanup_restore.sh') -Destination $rollback
function Save-Moved {
    [ordered]@{root=$root;moved=$moved;utc=[DateTime]::UtcNow.ToString('o')} | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath $movedPath -Encoding utf8NoBOM
}
function Read-Entry([string]$path){
    $d=[IO.File]::ReadAllBytes($path);if($d.Length -lt 28){return}
    $v=[BitConverter]::ToInt64($d,0);$size=[BitConverter]::ToInt64($d,8)
    if ($v -eq 2) {$n=[BitConverter]::ToInt32($d,24)*2;$start=28}
    elseif ($v -eq 1) {$start=24;$n=$d.Length-24}
    else {return}
    if ($n -le 0 -or $start+$n -gt $d.Length) {return}
    [pscustomobject]@{path=[Text.Encoding]::Unicode.GetString($d,$start,$n).TrimEnd([char]0);bytes=$size}
}
try {
    Add-Reg "BASELINE: $($plan.Count) source paths present and byte-identical to retained backups; $bytes bytes."
    foreach($x in $plan){
        $p=Assert-Regular $x.path ([IO.Path]::GetDirectoryName($x.path))
        if ($p.Length -ne $x.bytes -or $p.LastWriteTimeUtc.Ticks -ne $x.ticks -or
            (Get-FileHash -Algorithm SHA256 -LiteralPath $p.FullName).Hash -ne $x.sha256){throw "Source changed: $($x.path)"}
        $existing=@{}; Get-ChildItem -LiteralPath $bin -Force -File -Filter '$I*' | ForEach-Object {$existing[$_.Name]=$true}
        [US100Recycle]::Recycle($p.FullName)
        $hit=$null
        for($attempt=0;$attempt -lt 15 -and -not $hit;$attempt++){
            $hit=Get-ChildItem -LiteralPath $bin -Force -File -Filter '$I*' | Where-Object {
                if($existing.ContainsKey($_.Name)){return $false}
                $info=Read-Entry $_.FullName
                return $info.path -eq $x.path -and $info.bytes -eq $x.bytes
            } | Select-Object -First 1
            if(-not $hit){Start-Sleep -Milliseconds 125}
        }
        if(-not $hit -or (Test-Path -LiteralPath $x.path)){throw "Recycle receipt not verified: $($x.path)"}
        $payload=Join-Path $bin ('$R'+$hit.Name.Substring(2))
        if ((Get-Item -LiteralPath $payload -Force).Length -ne $x.bytes -or (Get-FileHash -Algorithm SHA256 -LiteralPath $payload).Hash -ne $x.sha256) {throw "Recycle payload mismatch: $payload"}
        $moved+=,$([ordered]@{run=$x.run;path=$x.path;retained=$x.retained;bytes=$x.bytes;sha256=$x.sha256;receipt=$hit.FullName;payload=$payload})
        Save-Moved
        Add-Reg "RECYCLED verified: $($x.path)"
    }
    foreach($x in $plan){ if ((Get-FileHash -Algorithm SHA256 -LiteralPath $x.retained).Hash -ne $x.sha256) {throw "Retained backup changed: $($x.retained)"} }
    foreach($s in $sentinels){ if ((Get-FileHash -Algorithm SHA256 -LiteralPath $s.Path).Hash -ne $s.Hash) {throw "Protected file changed: $($s.Path)"} }
    Add-Reg "MODIFIED: $($moved.Count) test paths are absent; original backups and live protected files unchanged."
    $small=$moved|Sort-Object {[long]$_['bytes']}|Select-Object -First 1
    $output=& python $rollback --only $small.run --probe 2>&1
    Add-Reg "ROLLBACK: python `"$rollback`" --only $($small.run) --probe; exit=$LASTEXITCODE"
    foreach($o in $output){Add-Reg ([string]$o)}
    if($LASTEXITCODE -ne 0){throw 'Rollback probe failed.'}
    Add-Reg "Complete: $($moved.Count) recycled files; $bytes bytes. Recycle Bin not emptied."
} catch {Add-Reg "ERROR: $_";throw} finally {$log | Set-Content -LiteralPath $verify -Encoding utf8NoBOM}
Get-Item -LiteralPath @($movedPath,$planPath,$verify,$rollback) | Select-Object -ExpandProperty FullName
