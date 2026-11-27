package pvf

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func buildMinimalDFO20260901(t *testing.T, dir string) string {
	t.Helper()
	pb := newPoolBuilder([]byte{0}, []byte{0, 0})
	// Build two initial files:
	// 1) list/equipment.lst (DataType 1): 1 `equipment/a.equ`, 2 `equipment/b.equ`
	// 2) string/equipment.uv.str (DataType 3): UTF-16LE "name_1>Base Sword\r\n"
	lstData, err := compileScriptText("[index]\n1 `equipment/a.equ`\n2 `equipment/b.equ`\n", pb)
	if err != nil {
		t.Fatal(err)
	}
	strData := encodeUTF16LE("name_1>Base Sword\r\nname_2>Old Shield\r\n")

	f1 := fileItem{nameOffset: pb.addASCII("equipment.lst"), pathOffset: pb.addASCII("list"), chunkIndex: 0, dataOffset: 0, dataSize: len(lstData), dataType: 1}
	f2 := fileItem{nameOffset: pb.addASCII("equipment.uv.str"), pathOffset: pb.addASCII("string"), chunkIndex: 0, dataOffset: len(lstData), dataSize: len(strData), dataType: 3}
	items := []fileItem{f1, f2}

	var chunkRaw bytes.Buffer
	chunkRaw.Write(lstData)
	chunkRaw.Write(strData)
	compChunk, err := compressZlib(chunkRaw.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	decryptProtected("mAIn", compChunk)

	// Build HSrm
	hashes := make([]byte, 4+2*8+4+2*4)
	binary.LittleEndian.PutUint32(hashes[0:4], 2)
	binary.LittleEndian.PutUint32(hashes[4:8], uint32(f1.nameOffset))
	binary.LittleEndian.PutUint32(hashes[8:12], uint32(f1.pathOffset))
	binary.LittleEndian.PutUint32(hashes[12:16], uint32(f2.nameOffset))
	binary.LittleEndian.PutUint32(hashes[16:20], uint32(f2.pathOffset))
	binary.LittleEndian.PutUint32(hashes[20:24], 2)
	// equipment.lst < equipment.uv.str
	binary.LittleEndian.PutUint32(hashes[24:28], uint32(f1.nameOffset))
	binary.LittleEndian.PutUint32(hashes[28:32], uint32(f2.nameOffset))
	decryptProtected("HSrm", hashes)

	// Build names
	encA, _ := compressZlib(pb.strA)
	decryptPString("stAs", encA)
	encW, _ := compressZlib(pb.strW)
	decryptPString("stWs", encW)
	var names bytes.Buffer
	names.Write(make([]byte, 8))
	_ = binary.Write(&names, binary.LittleEndian, uint32(len(encA))^0xe7adf7ea)
	_ = binary.Write(&names, binary.LittleEndian, uint32(len(pb.strA)^len(encA)))
	names.Write(encA)
	_ = binary.Write(&names, binary.LittleEndian, uint32(len(encW))^0xb8dea7ac)
	_ = binary.Write(&names, binary.LittleEndian, uint32(len(pb.strW)^len(encW)))
	names.Write(encW)

	groups := make([]byte, 8)
	binary.LittleEndian.PutUint32(groups[0:4], uint32(len(compChunk)))
	binary.LittleEndian.PutUint32(groups[4:8], uint32(chunkRaw.Len()))
	decryptProtected("Gidx", groups)

	var ft bytes.Buffer
	for _, it := range items {
		_ = binary.Write(&ft, binary.LittleEndian, uint32(it.nameOffset))
		_ = binary.Write(&ft, binary.LittleEndian, uint32(it.pathOffset))
		_ = binary.Write(&ft, binary.LittleEndian, uint32(it.chunkIndex))
		_ = binary.Write(&ft, binary.LittleEndian, uint32(it.dataOffset))
		_ = binary.Write(&ft, binary.LittleEndian, uint32(it.dataSize))
		_ = binary.Write(&ft, binary.LittleEndian, uint32(it.dataType))
	}

	var hdr [headerSize]byte
	binary.LittleEndian.PutUint32(hdr[0:4], magicSignature)
	binary.LittleEndian.PutUint32(hdr[24:28], uint32(len(items)))
	binary.LittleEndian.PutUint32(hdr[32:36], uint32(len(compChunk)))
	binary.LittleEndian.PutUint32(hdr[36:40], 1)
	binary.LittleEndian.PutUint32(hdr[40:44], uint32(len(hashes)))
	binary.LittleEndian.PutUint32(hdr[44:48], uint32(names.Len()))
	decryptProtected("iNfO", hdr[:])

	out := filepath.Join(dir, "base.inner.pvf")
	var all bytes.Buffer
	all.Write(hdr[:])
	all.Write(ft.Bytes())
	all.Write(hashes)
	all.Write(names.Bytes())
	all.Write(groups)
	all.Write(compChunk)
	if err := os.WriteFile(out, all.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestMergeModPlanReplaceAddLstAndStr(t *testing.T) {
	dir := t.TempDir()
	basePath := buildMinimalDFO20260901(t, dir)
	baseBytes, err := os.ReadFile(basePath)
	if err != nil {
		t.Fatal(err)
	}
	a, err := OpenBytes(baseBytes)
	if err != nil {
		t.Fatalf("load base: %v", err)
	}

	// Mod A (higher priority = 0): overrides ID 2 in .lst, adds ID 3, adds new .equ script, overrides name_2
	modALst := filepath.Join(dir, "modA.lst")
	modBLst := filepath.Join(dir, "modB.lst")
	modAStr := filepath.Join(dir, "modA.str")
	modAEqu := filepath.Join(dir, "modA.equ")
	_ = os.WriteFile(modALst, []byte("[index]\n2 `equipment/b_high.equ`\n3 `equipment/c_new.equ`\n"), 0600)
	_ = os.WriteFile(modBLst, []byte("[index]\n2 `equipment/b_low.equ`\n4 `equipment/d_extra.equ`\n"), 0600)
	_ = os.WriteFile(modAStr, []byte("name_2>神话之盾\r\nname_3>新增武器\r\n"), 0600)
	_ = os.WriteFile(modAEqu, []byte("#PVF_File\n[name]\n`新增测试光剑`\n[rarity]\n4\n[physical attack]\n1234.5\n"), 0600)

	mergedPath := filepath.Join(dir, "merged.inner.pvf")
	report, err := a.MergeModPlan(MergePlan{
		Layers: []MergeLayer{
			{Path: "list/equipment.lst", Source: modALst, ModID: "mod.high", Priority: 0},
			{Path: "list/equipment.lst", Source: modBLst, ModID: "mod.low", Priority: 1},
			{Path: "string/equipment.uv.str", Source: modAStr, ModID: "mod.high", Priority: 0},
			{Path: "equipment/c_new.equ", Source: modAEqu, ModID: "mod.high", Priority: 0},
		},
	}, mergedPath)
	if err != nil {
		t.Fatalf("MergeModPlan failed: %v", err)
	}
	if report.ReplacedFiles != 2 || report.AddedFiles != 1 || report.TotalFiles != 3 {
		t.Fatalf("unexpected report: %+v", report)
	}

	mergedBytes, err := os.ReadFile(mergedPath)
	if err != nil {
		t.Fatal(err)
	}
	m, err := OpenBytes(mergedBytes)
	if err != nil {
		t.Fatalf("reopen merged: %v", err)
	}
	// Verify .lst merge: ID 1 preserved, ID 2 high-priority wins, ID 3 & 4 added
	lstToks, err := m.Tokens("list/equipment.lst")
	if err != nil {
		t.Fatalf("read merged lst: %v", err)
	}
	gotLst := map[int32]string{}
	for i := 0; i+1 < len(lstToks); i++ {
		if lstToks[i].Type == 0 && lstToks[i+1].Type == 6 {
			gotLst[lstToks[i].Value] = lstToks[i+1].Text
		}
	}
	if gotLst[1] != "equipment/a.equ" || gotLst[2] != "equipment/b_high.equ" || gotLst[3] != "equipment/c_new.equ" || gotLst[4] != "equipment/d_extra.equ" {
		t.Fatalf("unexpected merged lst: %+v", gotLst)
	}

	// Verify newly added .equ script with Chinese wide string & float
	equToks, err := m.Tokens("equipment/c_new.equ")
	if err != nil {
		t.Fatalf("read new equ: %v", err)
	}
	foundName := false
	for _, tk := range equToks {
		if tk.Type == 6 && tk.Text == "新增测试光剑" {
			foundName = true
		}
	}
	if !foundName {
		t.Fatalf("chinese inline name not found in compiled script: %+v", equToks)
	}

	// Verify .str merge preserved name_1, overrode name_2, added name_3
	rawStr, err := m.ReadRaw("string/equipment.uv.str")
	if err != nil {
		t.Fatal(err)
	}
	txt := decodeUTF16LE(rawStr)
	for _, want := range []string{"name_1>Base Sword", "name_2>神话之盾", "name_3>新增武器"} {
		if !strings.Contains(txt, want) {
			t.Fatalf("missing %q in merged .str:\n%s", want, txt)
		}
	}
}
