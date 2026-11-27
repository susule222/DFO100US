[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [ValidateSet('Plan','Apply')]
    [string]$Action
)

$ErrorActionPreference = 'Stop'
$Root = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..\runtime')).ProviderPath
$Client = (Resolve-Path -LiteralPath 'D:\DNF\client\100US\DFO').ProviderPath
$Work = Join-Path $Root 'us100-localization-cleanup-20261125'
$Recovery = Join-Path $Root 'us100-last-localization-rollback'
$PlanFile = Join-Path $Work 'plan.json'
$ReceiptFile = Join-Path $Work 'receipts.json'
$Distro = Join-Path $Root 'localize-us100-zh-CN-r23'
$TableBuild = Join-Path $Root 'localize-us100-candidate'
$GameTables = @('quest.uv.str','epicquest.uv.str','live_quest.uv.str','storybookquest.uv.str')
$BuildTables = @('equipment.uv.str','stackable.uv.str')

function InRoot([string]$Path) {
    $r = [IO.Path]::GetFullPath($Root).TrimEnd('\') + '\'
    return [IO.Path]::GetFullPath($Path).StartsWith($r,[StringComparison]::OrdinalIgnoreCase)
}

function ExactTarget([string]$Path) {
    if (-not (InRoot $Path)) { throw "Target outside explicit runtime: $Path" }
    $item = Get-Item -Force -LiteralPath $Path
    if (-not (InRoot $item.FullName)) { throw "Resolved out of scope: $Path" }
    $current = $item.FullName
    while ($current) {
        $a = Get-Item -Force -LiteralPath $current
        if (($a.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { throw "Reparse point: $current" }
        $current = [IO.Path]::GetDirectoryName($current)
    }
    return $item
}

function WriteJson([string]$Path,$Data) {
    $part = "$Path.part"
    $Data | ConvertTo-Json -Depth 15 | Set-Content -LiteralPath $part -Encoding utf8
    [IO.File]::Move($part,$Path,$true)
}

function FileDigest([string]$Path) {
    return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash
}

function Fingerprint($BaseItem) {
    $count=0; $dirs=0; $size=[long]0
    $lines=[System.Collections.Generic.List[string]]::new()
    if ($BaseItem.PSIsContainer) {
        $stack=[System.Collections.Generic.Stack[string]]::new()
        $stack.Push($BaseItem.FullName)
        while ($stack.Count -gt 0) {
            $at=$stack.Pop()
            foreach($item in (Get-ChildItem -LiteralPath $at -Force)) {
                if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
                    throw "Rejecting nested reparse point: $($item.FullName)"
                }
                $rel=[IO.Path]::GetRelativePath($BaseItem.FullName,$item.FullName)
                if ($item.PSIsContainer) {
                    $dirs++
                    $lines.Add("D|${rel}")
                    $stack.Push($item.FullName)
                } else {
                    $count++; $size+=$item.Length
                    $lines.Add("F|${rel}|$($item.Length)|$($item.LastWriteTimeUtc.Ticks)")
                }
            }
        }
    } else {
        $count=1; $size=$BaseItem.Length
        $lines.Add("F|$($BaseItem.Length)|$($BaseItem.LastWriteTimeUtc.Ticks)|$(FileDigest $BaseItem.FullName)")
    }
    $lines.Sort([StringComparer]::Ordinal)
    $bytes=[Text.Encoding]::UTF8.GetBytes([string]::Join("`n",$lines))
    return [ordered]@{count=$count;dirs=$dirs;size=$size;
        fingerprint=[Convert]::ToHexString([Security.Cryptography.SHA256]::HashData($bytes))}
}

function ProtectedSnapshot {
    $files = [Collections.Generic.List[string]]::new()
    foreach($n in 'DFO.exe','Script.pvf','Script.pvf-1','dstr.dat',
       'ImagePacks2\sprite_interface_quest.NPK','ImagePacks2\sprite_interface2_hud.NPK',
       'ImagePacks2\sprite_common_etc.NPK','ImagePacks2\NpkIndex.etc') { $files.Add((Join-Path $Client $n)) }
    foreach($f in (Get-ChildItem -LiteralPath (Join-Path $Client 'Fonts') -File)) { $files.Add($f.FullName) }
    foreach($dir in $Distro,$TableBuild) {
        $names=if($dir -eq $Distro){$GameTables}else{$BuildTables}
        foreach($n in $names) {
            $x=Join-Path $dir ('replacement-tables\string\'+$n)
            if (Test-Path -LiteralPath $x -PathType Leaf) { $files.Add($x) }
        }
    }
    $files.Add((Join-Path $Distro 'replacement-tables\string\quest.uv.str'))
    $files.Add((Join-Path $Root 'atelier-fonts\originals\calibri.ttf'))
    $files.Add((Join-Path $Root 'atelier-fonts\originals\tahoma.ttf'))
    foreach($f in (Get-ChildItem -LiteralPath $Recovery -File -Recurse)) { $files.Add($f.FullName) }
    $out=[ordered]@{}
    foreach($f in ($files | Sort-Object -Unique)) { $out[$f]=FileDigest $f }
    return $out
}

function VerifyRollback {
    & python -X utf8 (Join-Path $PSScriptRoot 'us100_restore_last_quest.py') --bundle $Recovery --verify-only
    if ($LASTEXITCODE -ne 0) { throw 'Latest rollback did not verify' }
}

function RecycleContext {
    $vol=Get-Volume -DriveLetter D
    $id=[regex]::Match([string]$vol.UniqueId,'\{[0-9a-f-]+\}', 'IgnoreCase').Value
    if (-not $id -or $vol.FileSystem -ne 'NTFS' -or [IO.Path]::GetPathRoot($Root) -ne 'D:\') {
        throw 'Expected the verified local D: volume'
    }
    $pref=Get-ItemProperty -LiteralPath ("HKCU:\Software\Microsoft\Windows\CurrentVersion\Explorer\BitBucket\Volume\"+$id)
    if ($pref.NukeOnDelete -ne 0 -or $pref.MaxCapacity -le 0) { throw 'Recycle Bin disabled or size unavailable' }
    $idn=[System.Security.Principal.WindowsIdentity]::GetCurrent().User.Value
    $dir=Join-Path 'D:\$Recycle.Bin' $idn
    if (-not (Test-Path -LiteralPath $dir -PathType Container)) { throw 'No physical Recycle Bin for D:' }
    $usage=[long]0
    foreach($q in (Get-ChildItem -LiteralPath $dir -Force -Filter '$R*')) {
        if ($q.PSIsContainer) { $usage+=[long](Get-ChildItem -LiteralPath $q.FullName -File -Recurse -Force |
                            Measure-Object -Property Length -Sum).Sum }
        else { $usage+=$q.Length }
    }
    return @{dir=$dir; volume=$id; total=[long]$pref.MaxCapacity*1MB; current=$usage}
}

function EnsureSpace($Context,[long]$Bytes) {
    if ($Context.current+$Bytes+1GB -gt $Context.total) {
        throw "Insufficient Recycle Bin capacity; no fallback to permanent removal"
    }
}

function SelectedPaths {
    $names=@(
      'us100-avatar-l10n-atlas-r1','us100-avatar-l10n-atlas-r2','us100-avatar-l10n-atlas-r3',
      'us100-avatar-l10n-atlas-r4','us100-avatar-l10n-atlas-r5','us100-avatar-l10n-atlas-r6',
      'us100-avatar-l10n-dstr-r1','us100-avatar-l10n-pvf-r1','us100-avatar-pvf-plan-r1',
      'us100-character-create-dstr-r1','us100-character-name-r1','us100-clear-font-r1',
      'us100-clear-ui-atlas-r1','us100-clear-ui-dstr-r1','us100-contract-toast-r1',
      'us100-create-load-fix-r1','us100-create-load-fix-r2','us100-dstr-ui-r1',
      'us100-equipfilter-dstr-r1','us100-equipfilter-pvf-r1','us100-equipment-tooltip-dstr-r1',
      'us100-font-r23-local-trial','us100-hud-priority-atlas-audit-r1',
      'us100-hud-priority-atlas-r1','us100-hud-priority-atlas-r2','us100-hud-priority-atlas-r3',
      'us100-hud-priority-atlas-r4','us100-hud-priority-atlas-r5','us100-hud-priority-atlas-r6',
      'us100-hud-priority-dstr-r1','us100-inventory-system-notices-r2','us100-inventory-timed-items-r1',
      'us100-item-expiry','us100-monster-badges-r1','us100-north-map-r1','us100-north-map-r2',
      'us100-premium-panel-r1','us100-quest-act-fixed-r3','us100-quest-recommend-atlas-r1',
      'us100-quest-recommend-atlas-r2','us100-quest-recommend-dstr-r1','us100-quest-recommend-dstr-r2',
      'us100-quest-recommend-dstr-r3','us100-quest-strings-diagnostic-r1','us100-quest-ui-r1','us100-quest-ui-r2',
      'us100-shop-checkout-r1','us100-shop-checkout-r2','us100-shop-dstr-r1','us100-shop-dstr-r2',
      'us100-shop-load-atlas-r1','us100-shop-load-atlas-r2','us100-shop-load-atlas-r3',
      'us100-shop-load-r1','us100-shop-new-badge-r1','us100-skill-reward-ui-r1',
      'us100-status-dstr-r1','us100-status-dstr-r2','us100-system-reward-notices-r1',
      'us100-tooltip-cleanup-r1','us100-town-shop-r1','us100-town-shop-r2','us100-town-shop-r3','us100-weapon-tooltip-r1',
      'us100-fx-regression-20261125\hud-isolation',
      'us100-shop-payment-audit\Script.inner.pvf',
      'us100-shop-payment-audit\sprite_interface2_univ_coupon.NPK',
      'us100-premium-panel-audit\candidate-1','us100-premium-panel-audit\candidate-2',
      'us100-premium-panel-audit\candidate-3','us100-premium-panel-audit\candidate-4',
      'us100-premium-panel-audit\candidate-5'
    )
    foreach($name in $names) {
        $p=Join-Path $Root $name
        if(Test-Path -LiteralPath $p){$p}
    }
    foreach($d in (Get-ChildItem -LiteralPath $Root -Directory -Force)) {
        if ($d.FullName -eq $Distro) { continue }
        if ($d.Name -match '^localize-us100-(?:zh-CN(?:-r\d+[a-z]?(?:-preview)?)?|xui-r\d+[a-z]?(?:-preview)?)$') {
            $d.FullName
        }
    }
    foreach($dir in $Distro,$TableBuild) {
        $retained=if($dir -eq $Distro){$GameTables}else{$BuildTables}
        $r=Join-Path $dir 'replacement-tables'
        $s=Join-Path $r 'string'
        (Get-ChildItem -LiteralPath $dir -Force | Where-Object FullName -ne $r).FullName
        (Get-ChildItem -LiteralPath $r -Force | Where-Object FullName -ne $s).FullName
        (Get-ChildItem -LiteralPath $s -Force |
            Where-Object {$_.Name -notin $retained}).FullName
    }
}

function ReadReceipt([string]$Path) {
    $b=[IO.File]::ReadAllBytes($Path)
    if ($b.Length -lt 26) { return $null }
    $ver=[BitConverter]::ToInt64($b,0)
    $pos=if($ver -eq 1){24}elseif($ver -eq 2 -and $b.Length -ge 29){28}else{return $null}
    $length=$b.Length-$pos
    if (($length % 2) -ne 0) { return $null }
    $original=[Text.Encoding]::Unicode.GetString($b,$pos,$length).TrimEnd([char]0)
    if (-not $original) { return $null }
    return @{original=$original;bytes=[BitConverter]::ToInt64($b,8)}
}

if ($Action -eq 'Plan') {
    if (Test-Path -LiteralPath $Work) { throw 'Plan already exists; cannot silently replace' }
    VerifyRollback
    $protected=ProtectedSnapshot
    $paths=@(SelectedPaths | Where-Object { $_ } | Sort-Object -Unique)
    if ($paths.Count -eq 0) { throw 'No reviewed old packages found' }
    foreach($p in $paths) {
        foreach($f in $protected.Keys) {
            if ($p -eq $f -or $f.StartsWith($p+'\',[StringComparison]::OrdinalIgnoreCase)) {
                throw "Protected file is in target: $f"
            }
        }
        if ($paths.Where({$_ -ne $p -and $_.StartsWith($p+'\',[StringComparison]::OrdinalIgnoreCase)}).Count) {
            throw "Overlapping move targets: $p"
        }
    }
    $rows=foreach($p in $paths) {
        $a=ExactTarget $p
        $b=Fingerprint $a
        [ordered]@{path=$a.FullName; is_dir=$a.PSIsContainer; last_write_ticks=$a.LastWriteTimeUtc.Ticks;
           fingerprint=$b.fingerprint; count=$b.count; dirs=$b.dirs;size=$b.size}
    }
    $ctx=RecycleContext
    $total=[long]0; $fileCount=[int]0
    foreach($row in $rows) { $total+=[long]$row['size']; $fileCount+=[int]$row['count'] }
    EnsureSpace $ctx $total
    New-Item -ItemType Directory -Path $Work | Out-Null
    $summary=[ordered]@{root=$Root;client=$Client;volume=$ctx.volume;total=$total;
       targets=$rows;protected=$protected}
    WriteJson $PlanFile $summary
    "Planned $($rows.Count) targets / $fileCount files / $total bytes."
    "D: bin $($ctx.current) used of $($ctx.total) bytes."
    exit 0
}

if (Test-Path -LiteralPath $ReceiptFile) { throw 'A move was already started; verify the receipt before any retry' }
$plan=Get-Content -LiteralPath $PlanFile -Raw -Encoding utf8 | ConvertFrom-Json -AsHashtable
if ($plan.root -ne $Root -or $plan.client -ne $Client) { throw 'Plan paths changed' }
VerifyRollback
foreach($k in $plan.protected.Keys) {
    if ((FileDigest $k) -ne $plan.protected[$k]) { throw "Kept file changed since plan: $k" }
}
$ctx=RecycleContext
if ($ctx.volume -ne $plan.volume) { throw 'Volume identity differs' }
$plannedSize=[long]0
foreach($row in $plan.targets) { $plannedSize+=[long]$row['size'] }
if ($plannedSize -ne $plan.total) { throw 'Aggregate size differs from the target snapshots' }
EnsureSpace $ctx $plan.total
$actual=@(SelectedPaths | Where-Object {$_} | Sort-Object -Unique)
if ((Compare-Object $actual @($plan.targets.path)).Count) { throw 'Current allowlist differs from plan' }

function CheckTarget($row) {
    $a=ExactTarget $row.path
    if ($a.FullName -ne $row.path -or $a.PSIsContainer -ne $row.is_dir -or
        $a.LastWriteTimeUtc.Ticks -ne $row.last_write_ticks) { throw "Target changed: $($row.path)" }
    $b=Fingerprint $a
    if ($b.fingerprint -ne $row.fingerprint -or $b.count -ne $row.count -or
        $b.dirs -ne $row.dirs -or $b.size -ne $row.size) { throw "Target snapshot differs: $($row.path)" }
}
foreach($row in $plan.targets){CheckTarget $row}
Add-Type -AssemblyName Microsoft.VisualBasic
$seen=[Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
foreach($f in (Get-ChildItem -LiteralPath $ctx.dir -File -Force -Filter '$I*')){[void]$seen.Add($f.Name)}
$receipts=[Collections.Generic.List[object]]::new()
foreach($row in $plan.targets) {
    CheckTarget $row
    WriteJson $ReceiptFile @{status='moving'; pending=$row.path; verified=$receipts}
    $ui=[Microsoft.VisualBasic.FileIO.UIOption]::OnlyErrorDialogs
    $recycle=[Microsoft.VisualBasic.FileIO.RecycleOption]::SendToRecycleBin
    $cancel=[Microsoft.VisualBasic.FileIO.UICancelOption]::ThrowException
    if ($row.is_dir) {
        [Microsoft.VisualBasic.FileIO.FileSystem]::DeleteDirectory($row.path,$ui,$recycle,$cancel)
    } else {
        [Microsoft.VisualBasic.FileIO.FileSystem]::DeleteFile($row.path,$ui,$recycle,$cancel)
    }
    if (Test-Path -LiteralPath $row.path) { throw "Source did not move: $($row.path)" }
    $result=@(Get-ChildItem -LiteralPath $ctx.dir -File -Force -Filter '$I*' | Where-Object {
        -not $seen.Contains($_.Name) -and (ReadReceipt $_.FullName).original -eq $row.path
    })
    if ($result.Count -ne 1) { throw "Cannot identify one new physical receipt: $($row.path)" }
    $r=$result[0]
    $payloadPath=Join-Path $ctx.dir ('$R'+$r.Name.Substring(2))
    $p=Get-Item -LiteralPath $payloadPath -Force
    if ($p.PSIsContainer -ne $row.is_dir) { throw "Recycle payload type differs: $payloadPath" }
    $m=Fingerprint $p
    if ($m.fingerprint -ne $row.fingerprint -or $m.count -ne $row.count -or
        $m.dirs -ne $row.dirs -or $m.size -ne $row.size) { throw "Recycle payload differs: $payloadPath" }
    [void]$seen.Add($r.Name)
    $receipts.Add([ordered]@{original=$row.path;receipt=$r.FullName;payload=$p.FullName;
        count=$m.count;dirs=$m.dirs;bytes=$m.size;fingerprint=$m.fingerprint})
    WriteJson $ReceiptFile @{status='in_progress';verified=$receipts}
}

foreach($k in $plan.protected.Keys) {
    if ((FileDigest $k) -ne $plan.protected[$k]) { throw "Kept file changed during cleanup: $k" }
}
VerifyRollback
if (@($plan.targets | Where-Object {Test-Path -LiteralPath $_.path}).Count -or
    @($receipts | Where-Object {-not (Test-Path -LiteralPath $_.receipt) -or
                              -not (Test-Path -LiteralPath $_.payload)}).Count) { throw 'Final move check failed' }
WriteJson $ReceiptFile @{status='verified'; mode='recycle_bin_only'; verified=$receipts;
     live_untouched=$true; rollback=$Recovery; total=$plan.total;
     protected_count=$plan.protected.Count; utc=[DateTimeOffset]::UtcNow.ToString('o')}
"Recycled and verified $($receipts.Count) targets / $($plan.total) bytes."
