"""Build an offline, data-only preview of the two signed DFO display paths."""
import argparse
import hashlib
from pathlib import Path
import re
import struct
import tempfile

from inspect_pe import PE
from us100_status_dstr import CLIENT, EXE, core

LITERALS=(
    # data RVA, renderer reference RVA, baseline, Chinese, UI context
    (0x685d940,0x1068ef2," Gold"," 金币","reward card amount"),
    (0x72558a8,0x3460581,"Lv%d%s %s","%d级%s %s","monster title with extra descriptor"),
    (0x72558c8,0x34605dc,"Lv%d %s","%d级 %s","monster title"),
)
FORMAT=re.compile(r"%[ds]")


def transform(raw,seed,encode):
    result=bytearray()
    key=seed|0x9a714ca0
    for i in range(0,len(raw)//4*4,4):
        v=int.from_bytes(raw[i:i+4],"little")
        out=v^key
        result.extend(out.to_bytes(4,"little"))
        key=(key*0x1003f+(v if encode else out))&0xffffffff
    for v in raw[len(raw)//4*4:]:
        out=v^(key&255)
        result.append(out)
        key=(key*0x107+(v if encode else out))&0xffffffff
    return bytes(result)


def build(out):
    exe=CLIENT/"DFO.exe"
    out=out.resolve()
    if out.is_relative_to(CLIENT.resolve()) or core.digest(exe)!=EXE:
        raise ValueError("require original signed client and offline output")
    out.mkdir(parents=True,exist_ok=True)
    pe=PE(exe)
    plan=[]
    try:
        _,va,_,file_off,size=next(s for s in pe.sections if s[0]==".rdata")
        for r,ref,english,zh,ui in LITERALS:
            h=pe.read(r,4)
            seed=h[1]&0xfe
            code=(seed<<7)|seed
            n=code^int.from_bytes(h[2:],"little")
            data=pe.read(r+4,n*2)
            plain=transform(data,seed,False)
            lea=pe.read(ref,12)
            if (not va<=r<r+4+len(data)<=va+size or plain!=(english+"\0").encode("utf-16-le")
                or pe.literal(r)!=english or lea[:3]!=b"\x48\x8d\x0d"
                or ref+7+struct.unpack_from("<i",lea,3)[0]!=r
                or lea[7]!=0xe8 or ref+12+struct.unpack_from("<i",lea,8)[0]!=0x4f16830
                or FORMAT.findall(english)!=FORMAT.findall(zh)):
                raise ValueError(f"wrong {ui} literal or renderer reference")
            replacement=(zh+"\0").encode("utf-16-le")
            if len(replacement)>len(data):raise ValueError(f"literal exceeds slot: {ui}")
            ct=transform(replacement,seed,True)
            new=h[:2]+struct.pack("<H",code^(len(replacement)//2))+ct+data[len(ct):]
            if len(new)!=4+len(data) or transform(ct,seed,False)!=replacement:
                raise ValueError("literal encoding did not round trip")
            plan.append({"ui":ui,"data_rva":hex(r),"ref_rva":hex(ref),
                "offset":file_off+r-va,"source":english,"candidate":zh,
                "before_bytes":(h+data).hex(),"after_bytes":new.hex()})
        buf=bytearray(pe.data)
    finally:
        pe.close()
    for row in plan:
        at=row["offset"]
        change=bytes.fromhex(row["after_bytes"])
        buf[at:at+len(change)]=change
    new_hash=hashlib.sha256(buf).hexdigest()
    # Decode a transient PE copy via the existing independent parser. Do not
    # publish a runnable binary or invoke any code from the copy.
    temp=None
    try:
        with tempfile.NamedTemporaryFile(dir=out,suffix=".preview",delete=False) as f:
            temp=Path(f.name)
            f.write(buf)
        check=PE(temp)
        try:
            if any(check.literal(r)!=zh for r,_,_,zh,_ in LITERALS):
                raise ValueError("PE preview did not reopen")
        finally:
            check.close()
    finally:
        if temp is not None:temp.unlink(missing_ok=True)
    for row in plan:
        at=row["offset"]
        old=bytes.fromhex(row["before_bytes"])
        buf[at:at+len(old)]=old
    if hashlib.sha256(buf).hexdigest()!=EXE or core.digest(exe)!=EXE:
        raise ValueError("rollback or original fingerprint differs")
    receipt={"original_sha256":EXE,"hypothetical_sha256":new_hash,
        "status":"offline_only_would_invalidate_vendor_signature",
        "writes_live_client":False,"runtime_executed":False,"reversible":True,"edits":plan}
    core.atomic(out/"literal-preview.json",receipt)
    print("3 fixed display literals verified; original executable untouched.")


if __name__=="__main__":
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out",required=True,type=Path)
    build(parser.parse_args().out)
