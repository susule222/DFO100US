from contextlib import nullcontext
import codecs
import struct
from pathlib import Path
from tempfile import TemporaryDirectory
import unittest
from unittest.mock import patch

import us100_quest_ui_install as installer
import us100_quest_text_trial as trial
import us100_dstr_ui as dstr
import us100_map_north_belmire as north
from us100_quest_act import compress_exact, inflate_exact
from us100_quest_strings import KEYS, overlay
from us100_ui_atlas import KEY, index, member, rebuild_img, rebuild_npk, split_img


def atlas(*pixels):
    frame = struct.pack("<5I2i2I", 16, 5, 1, 1, 4, 0, 0, 1, 1)
    return b"Neople Img File\0" + struct.pack("<4I",36*len(pixels),0,2,len(pixels)) + frame*len(pixels) + b"".join(pixels)


def fixture(path):
    n = 3
    offset = 20 + 264*n
    items = ((offset,3,"first.img"),(offset,3,"alias.img"),(offset+4,4,"other.img"))
    rows = []
    for pos,size,name in items:
        pad = name.encode().ljust(256,b"\0")
        rows.append(struct.pack("<2I",pos,size) + bytes(a^b for a,b in zip(pad,KEY)))
    path.write_bytes(b"NeoplePack_Bill\0"+struct.pack("<I",n)+b"".join(rows)+b"old!moreTAIL")


class QuestUITests(unittest.TestCase):
    def test_north_map_byte_guard_checks_unchanged_gaps_and_trailer(self):
        slots=[(i*2,i*2+1,n) for i,n in enumerate(sorted(north.TARGETS))]
        original=b"\0"*(len(slots)*2+2)
        candidate=bytearray(original)
        for start,_,_ in slots:
            candidate[start]=1
        north.verify_spans(original,candidate,slots)
        candidate[1]=2
        with self.assertRaisesRegex(ValueError,"outside map frame spans"):
            north.verify_spans(original,candidate,slots)
        candidate[1]=0
        candidate[-1]=2
        with self.assertRaisesRegex(ValueError,"trailer"):
            north.verify_spans(original,candidate,slots)

    def test_dstr_container_round_trip_with_partial_final_encryption_block(self):
        keys=[bytes(range(32)),bytes(range(32,64))]
        for n in (244,245,247,248,249,507,514):
            plain=(b"test DSTR row\r\n"*40)[:n]
            encoded=dstr.pack(plain,keys)
            self.assertEqual(dstr.unpack(encoded,keys),plain)
            self.assertEqual(dstr.pack(dstr.unpack(encoded,keys),keys),encoded)
            self.assertEqual(encoded[:4]==b"SC01",len(encoded)<256)

    def test_dstr_install_and_rollback_preserve_other_assets(self):
        with TemporaryDirectory() as directory:
            root=Path(directory).resolve(); client,bundle=root/"client",root/"bundle"
            client.mkdir(); bundle.mkdir()
            atlas=client/dstr.REL; atlas.parent.mkdir(); atlas.write_bytes(b"main")
            live=client/"dstr.dat"; live.write_bytes(b"original")
            candidate=bundle/"dstr.dat"; candidate.write_bytes(b"chinese")
            exe,pvf=client/"DFO.exe",client/"Script.pvf"
            exe.write_bytes(b"exe"); pvf.write_bytes(b"pvf")
            old,new,e,s,a=map(dstr.core.digest,(live,candidate,exe,pvf,atlas))
            dstr.core.atomic(bundle/"manifest.json",dict(before=old,after=new,native_round_trip=True))
            with (patch.multiple(dstr,CLIENT=client,DSTR=old,EXE=e,PVF=s,ACT=a),
                  patch.object(dstr,"GameFonts") as fonts,
                  patch.object(dstr.core,"client_up",return_value=True) as running):
                fonts.return_value.locked.return_value=nullcontext()
                fonts.return_value.hashes.return_value=dstr.OUTLINE_FONTS
                with self.assertRaisesRegex(ValueError,"DFO reopened"):
                    dstr.deploy(bundle,False)
                self.assertEqual(live.read_bytes(),b"original")
                running.return_value=False
                dstr.deploy(bundle,False)
                self.assertEqual(live.read_bytes(),b"chinese")
                dstr.deploy(bundle,True)
                self.assertEqual(live.read_bytes(),b"original")
            self.assertEqual(atlas.read_bytes(),b"main")
            self.assertEqual(pvf.read_bytes(),b"pvf")

    def test_exact_zlib_slot_has_no_data_after_the_end_marker(self):
        raw=b"quest label"*150
        for length in (100,101,102,103,104):
            slot=compress_exact(raw,length,b"\x78\x9c")
            self.assertEqual(len(slot),length)
            self.assertEqual(inflate_exact(slot,len(raw)),raw)
            with self.assertRaises(ValueError):
                inflate_exact(slot+b"\0",len(raw))

    def test_text_diagnostic_preserves_unrelated_exact_text_and_line_endings(self):
        def table(rows):
            return codecs.BOM_UTF16_LE+("\r\n".join(rows)+"\r\n").encode("utf-16-le")
        other=["// text comment", "31052>Abandon", "90000>Other > text"]
        original=table(other+[f"{k}>{v[0]}" for k,v in KEYS.items()])
        prior=table(other+[f"{k}>prior" for k in KEYS])
        out, rows=overlay(original,prior)
        self.assertEqual(out,table(other+[f"{k}>{v[1]}" for k,v in KEYS.items()]))
        self.assertEqual(len(rows),len(KEYS))
        out2, rows2=overlay(original[2:],prior[2:])
        self.assertEqual((out2,rows2),(out[2:],rows))

    def test_text_trial_changes_only_pvf_and_rolls_back_independently(self):
        with TemporaryDirectory() as d:
            root=Path(d).resolve(); client,bundle=root/"client",root/"bundle"
            client.mkdir(); (bundle/"reopened").mkdir(parents=True)
            archive=client/"Script.pvf"; archive.write_bytes(b"R23")
            candidate=bundle/"Script.pvf"; candidate.write_bytes(b"trial")
            table=bundle/"reopened"/"00-dnf.uv.str.bin"; table.write_bytes(b"table")
            atlas=client/trial.REL; atlas.parent.mkdir(); atlas.write_bytes(b"main")
            dstr=client/"dstr.dat"; dstr.write_bytes(b"original dstr")
            old,new,rt,act,ds=map(trial.core.digest,(archive,candidate,table,atlas,dstr))
            trial.core.atomic(bundle/"Script.manifest.json",dict(candidate_sha256=new,verified_rewrap_round_trip=True))
            with (patch.multiple(trial,CLIENT=client,PVF=old,EXPECTED_TRIAL=new,ROUND_TRIP=rt,ACT=act,DSTR=ds),
                  patch.object(trial,"GameFonts") as fonts,
                  patch.object(trial.core,"client_up",return_value=False) as running):
                fonts.return_value.locked.return_value=nullcontext()
                fonts.return_value.hashes.return_value=trial.OUTLINE_FONTS
                running.return_value=True
                with self.assertRaisesRegex(ValueError,"close DFO"):
                    trial.deploy(bundle,False)
                self.assertFalse((bundle/"before-Script.pvf").exists())
                running.return_value=False
                trial.deploy(bundle,False)
                self.assertEqual(archive.read_bytes(),b"trial")
                self.assertEqual(atlas.read_bytes(),b"main")
                self.assertEqual(dstr.read_bytes(),b"original dstr")
                fonts.return_value.hashes.return_value={}
                trial.deploy(bundle,True)
                self.assertEqual(archive.read_bytes(),b"R23")
                self.assertEqual(trial.core.read(bundle/"deployment.json")["status"],"rolled_back")

    def test_image_keeps_untouched_frame_byte_exact(self):
        a=atlas(b"\x04\x03\x02\xff",b"\x11\x12\x13\xff")
        b=atlas(b"\x14\x22\x27\xff",b"\x31\x32\x33\xff")
        built=split_img(rebuild_img(a,b,{0:1}))
        self.assertEqual(built,[split_img(b)[1],split_img(a)[1]])

    def test_npk_can_split_exact_alias_without_touching_other_member(self):
        with TemporaryDirectory() as d:
            old,new=Path(d)/"old.npk",Path(d)/"new.npk"
            fixture(old)
            new.write_bytes(rebuild_npk(old,{"first.img":b"newer"}))
            self.assertEqual(list(index(new)),list(index(old)))
            self.assertEqual(member(new,"first.img"),b"newer")
            self.assertEqual(member(new,"alias.img"),b"old")
            self.assertEqual(member(new,"other.img"),b"more")
            self.assertTrue(new.read_bytes().endswith(b"TAIL"))

    def test_restore_quest_icons_leaves_other_assets_and_font_state_alone(self):
        with TemporaryDirectory() as d:
            root=Path(d).resolve(); client,bundle=root/"client",root/"bundle"
            client.mkdir()
            for name in ("Script.pvf", "dstr.dat"):
                (client/name).write_bytes(name.encode())
            report={"source":str(client),"fonts":{},"npk":{}}
            paths=[]
            for folder, section, names in (("Fonts","fonts",installer.ORIGINAL),
                                           ("ImagePacks2","npk",installer.PLAN)):
                for name in names:
                    rel=Path(folder)/name
                    paths.append(rel)
                    for where, payload in ((bundle/"before",b"old"),(bundle,b"new"),(client,b"new")):
                        path=where/rel
                        path.parent.mkdir(parents=True,exist_ok=True)
                        path.write_bytes(payload)
                    report[section][name]={"before":installer.core.digest(bundle/"before"/rel),
                                           "after":installer.core.digest(bundle/rel)}
            installer.core.atomic(bundle/"manifest.json",report)
            journal=bundle/"deployment.json"
            installer.core.atomic(journal,{"status":"installed","previous_font_state":{"sentinel":True}})
            target=Path("ImagePacks2")/installer.QUEST
            with (patch.multiple(installer,CLIENT=client,PVF=installer.core.digest(client/"Script.pvf"),
                                 DSTR=installer.core.digest(client/"dstr.dat")),
                  patch.object(installer,"GameFonts") as fonts,
                  patch.object(installer.core,"client_up",return_value=False),
                  patch.object(installer.core,"file_copy",wraps=installer.core.file_copy) as copy):
                fonts.return_value.locked.return_value=nullcontext()
                installer.install(bundle,False,restore_quest_icons=True)
                installer.install(bundle,False,restore_quest_icons=True)
                copy.assert_called_once_with(bundle/"before"/target,client/target)
                fonts.return_value.state.assert_not_called()
            for rel in paths:
                self.assertEqual((client/rel).read_bytes(),b"old" if rel==target else b"new")
            installed=installer.core.read(journal)
            self.assertEqual(installed["status"],"quest_icons_restored")
            self.assertEqual(installed["restored_file"],target.as_posix())
            self.assertEqual(installed["previous_font_state"],{"sentinel":True})


if __name__ == "__main__":
    unittest.main()
