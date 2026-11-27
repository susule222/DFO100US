package pvf

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

const (
	headerSize     = 0x30
	fileItemSize   = 0x18
	groupItemSize  = 8
	magicSignature = 0x69706B6E
)

type ArchiveFormat string

const (
	// FormatNKPI 表示旧版 DNF PVF `nkpi` 归档格式。
	FormatNKPI ArchiveFormat = "nkpi"
	// FormatProtectedNKPI 表示 23.4.15.0 之后使用 UTF-16 seed 密钥流的 `nkpi` 归档格式。
	FormatProtectedNKPI ArchiveFormat = "protected_nkpi"
	// FormatDFO20260901 is the inner archive of the supplied 2.38.2.34 client.
	// The RSA/AES outer layer must first be removed into a separate file.
	FormatDFO20260901 ArchiveFormat = "dfo_20260901_inner"
)

var (
	ErrInvalidArchive = errors.New("pvf archive is invalid")
	ErrFileNotFound   = errors.New("pvf file not found")
)

type ArchiveSnapshot struct {
	Format       ArchiveFormat `json:"format"`
	Path         string        `json:"path"`
	Size         int64         `json:"size"`
	Checksum     string        `json:"checksum"`
	LoadedAt     string        `json:"loaded_at"`
	FileCount    int           `json:"file_count"`
	GroupCount   int           `json:"group_count"`
	CachedChunks int           `json:"cached_chunks"`
	CachedTexts  int           `json:"cached_texts"`
}

type PreloadResult struct {
	Groups int `json:"groups"`
	Cached int `json:"cached"`
}

type File struct {
	Index       int    `json:"index"`
	Path        string `json:"path"`
	Name        string `json:"name"`
	ArchivePath string `json:"archive_path"`
	DataType    int    `json:"data_type"`
	Size        int    `json:"size"`
}

type Archive struct {
	snapshot Snapshot
	format   ArchiveFormat
	header   pvfHeader

	// data 是启动期读入的完整 PVF 字节，后续查询不再访问磁盘。
	data   []byte
	items  []fileItem
	groups []groupItem

	// pathIndex 保存归一化路径的 64 位哈希到文件表下标的排序映射。
	// A map[string]int over three million entries costs several hundred MB of
	// string headers alone; the sorted hash table is ~16 bytes per entry and
	// hashes are computed straight from the string-pool bytes, so loading a
	// 390 MB archive no longer constructs millions of transient strings.
	pathIndex []pathEntry
	bodyOff   int
	strA      []byte
	strW      []byte

	// chunks 缓存已解密解压的 body chunk，texts 缓存已解码的脚本文本。
	// Both are byte-budgeted LRUs: unbounded caches made importer runs grow
	// without limit as they streamed the archive's groups.
	chunks *valueCache
	texts  *valueCache
}

// pathEntry pairs a normalized-path hash with its file index.
type pathEntry struct {
	hash uint64
	idx  int32
}

// Cache budgets. Importers read groups sequentially, so a window of a few
// chunks is enough for them; interactive catalogs re-read hot town/dungeon
// scripts which stay resident within the same budget.
const (
	chunkCacheBudget = 64 << 20
	textCacheBudget  = 16 << 20
)

func Open(path string) (*Archive, error) {
	return LoadArchive(Options{Path: path})
}

func OpenBytes(data []byte) (*Archive, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: empty pvf data", ErrInvalidArchive)
	}
	copied := append([]byte(nil), data...)
	sum := sha256.Sum256(copied)
	return OpenArchive(&Bundle{
		snapshot: Snapshot{
			Size:     int64(len(copied)),
			Checksum: hex.EncodeToString(sum[:]),
			LoadedAt: time.Now().UTC(),
		},
		data: copied,
	})
}

func LoadArchive(options Options) (*Archive, error) {
	bundle, err := Load(options)
	if err != nil {
		return nil, err
	}
	archive, err := OpenArchive(bundle)
	if err != nil {
		return nil, err
	}
	return archive, nil
}

func OpenArchive(bundle *Bundle) (*Archive, error) {
	if bundle == nil || len(bundle.data) == 0 {
		return nil, fmt.Errorf("%w: empty pvf data", ErrInvalidArchive)
	}
	archive := &Archive{
		snapshot: bundle.snapshot,
		data:     bundle.data,
		chunks:   newValueCache(chunkCacheBudget),
		texts:    newValueCache(textCacheBudget),
	}
	if err := archive.parse(); err != nil {
		return nil, err
	}
	return archive, nil
}

func (a *Archive) Snapshot() ArchiveSnapshot {
	if a == nil {
		return ArchiveSnapshot{}
	}
	return ArchiveSnapshot{
		Format:       a.format,
		Path:         a.snapshot.Path,
		Size:         a.snapshot.Size,
		Checksum:     a.snapshot.Checksum,
		LoadedAt:     a.snapshot.LoadedAt.Format(rfc3339Nano),
		FileCount:    len(a.items),
		GroupCount:   len(a.groups),
		CachedChunks: a.chunks.Len(),
		CachedTexts:  a.texts.Len(),
	}
}

// Format 返回当前 PVF 归档格式，供 smoke 报告和兼容排查使用。
func (a *Archive) Format() ArchiveFormat {
	if a == nil {
		return ""
	}
	return a.format
}

func (a *Archive) fileAt(i int) File {
	item := a.items[i]
	return File{
		Index:       i,
		Path:        a.resolveString(item.pathOffset),
		Name:        a.resolveString(item.nameOffset),
		ArchivePath: joinArchivePath(a.resolveString(item.pathOffset), a.resolveString(item.nameOffset)),
		DataType:    item.dataType,
		Size:        item.dataSize,
	}
}

func (a *Archive) Files() []File {
	if a == nil || len(a.items) == 0 {
		return nil
	}
	out := make([]File, len(a.items))
	for i := range a.items {
		out[i] = a.fileAt(i)
	}
	return out
}

// FileAt exposes one entry without copying the table. Archive-wide scans on
// multi-million-entry archives (pvfinspect, audits) must use FileAt with
// FileCount; Files() copies the whole table and costs hundreds of MB.
func (a *Archive) FileAt(i int) File {
	if a == nil || i < 0 || i >= len(a.items) {
		return File{}
	}
	return a.fileAt(i)
}

func (a *Archive) FileCount() int {
	if a == nil {
		return 0
	}
	return len(a.items)
}

func (a *Archive) CanReadFileData() bool {
	if a == nil {
		return false
	}
	return a.format == FormatNKPI || a.format == FormatProtectedNKPI || a.format == FormatDFO20260901
}

func (a *Archive) FindFile(relativePath string) (File, bool) {
	if a == nil {
		return File{}, false
	}
	idx, ok := a.findIndex(relativePath)
	if !ok {
		return File{}, false
	}
	return a.fileAt(idx), true
}

func (a *Archive) FindFileIndex(relativePath string) int {
	if a == nil {
		return -1
	}
	idx, ok := a.findIndex(relativePath)
	if !ok {
		return -1
	}
	return idx
}

func (a *Archive) ReadText(relativePath string) (string, error) {
	if a == nil {
		return "", fmt.Errorf("%w: archive is nil", ErrInvalidArchive)
	}
	idx, ok := a.findIndex(relativePath)
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrFileNotFound, relativePath)
	}
	return a.readTextIndex(idx)
}

func (a *Archive) ReadRaw(relativePath string) ([]byte, error) {
	if a == nil {
		return nil, fmt.Errorf("%w: archive is nil", ErrInvalidArchive)
	}
	idx, ok := a.findIndex(relativePath)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrFileNotFound, relativePath)
	}
	return a.readRawIndex(idx)
}

// findIndex binary-searches the sorted path-hash table. The query is
// normalized exactly like the build side, and a matching bucket's real path
// is compared before accepting, so a hash collision can never return a wrong
// entry.
//
// 韩服 v100 归档用的是**扁平的名称表布局**，其余客户端家族用 `list/`。这里在
// 精确查找失败后做一次别名回退，好让同一套目录表工具吃两种布局 —— 否则
// catalogimport/worldcatalog 会直接报 `pvf file not found: list/character.lst`。
// 每条映射都用韩服 Script.pvf 的真实归档路径核对过（见 kr100/runtime/pvf/matches.json）。
func (a *Archive) findIndex(relativePath string) (int, bool) {
	if idx, ok := a.findIndexExact(relativePath); ok {
		return idx, true
	}
	for _, alias := range krPathAliases[pathKey(relativePath)] {
		if idx, ok := a.findIndexExact(alias); ok {
			return idx, true
		}
	}
	// 韩服归档把脚本按其类型目录分层，而 `.lst` 里存的是**不以类型目录开头**的
	// 相对路径（实测 `swordman/swordman.chr` 实际在 `character/swordman/…`，
	// `silvercrown.twn` 实际在 `town/…`）。逐个前缀试一遍。
	// 这不会误命中：findIndexExact 会比对完整路径键，前缀不对就是找不到。
	if relativePath != "" && relativePath[0] != '/' {
		for _, prefix := range krFallbackPrefixes {
			if idx, ok := a.findIndexExact(prefix + relativePath); ok {
				return idx, true
			}
		}
	}
	return 0, false
}

// krFallbackPrefixes: 韩服 v100 归档的类型目录，用于给 `.lst` 条目补前缀。
var krFallbackPrefixes = []string{
	"character/", "town/", "monster/", "npc/", "skill/", "equipment/",
	"stackable/", "map/", "dungeon/", "creature/", "passiveobject/",
	"aicharacter/", "appendage/", "etc/",
}

// krPathAliases: 本工程沿用的路径 -> 韩服 v100 归档中的实际路径。
var krPathAliases = map[string][]string{
	"list/character.lst": {"character/character.lst"},
	"list/creature.lst":  {"creature/creature.lst"},
	"list/dungeon.lst":   {"dungeon/dungeon.lst"},
	"list/equipment.lst": {"equipment/equipment.lst"},
	"list/itemname.lst":  {"itemname.lst"},
	"list/map.lst":       {"map/map.lst"},
	"list/monster.lst":   {"monster/monster.lst"},
	"list/npc.lst":       {"npc/npc.lst"},
	"list/quest.lst":     {"n_quest/quest.lst"},
	"list/setitemname.lst": {"setitemname.lst", "etc/setitemname.lst"},
	"list/skill.lst":     {"skill/skilllist.lst"},
	"list/stackable.lst": {"stackable/stackable.lst"},
	"list/town.lst":      {"town/town.lst"},
	"list/worldmap.lst":  {"worldmap/worldmap.lst"},
}

func (a *Archive) findIndexExact(relativePath string) (int, bool) {
	key := pathKey(relativePath)
	if key == "" {
		return 0, false
	}
	h := pathHashBytes([]byte(key))
	lo, hi := 0, len(a.pathIndex)
	for lo < hi {
		mid := (lo + hi) / 2
		if a.pathIndex[mid].hash < h {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	for i := lo; i < len(a.pathIndex) && a.pathIndex[i].hash == h; i++ {
		idx := int(a.pathIndex[i].idx)
		if idx >= 0 && pathKey(a.fileAt(idx).ArchivePath) == key {
			return idx, true
		}
	}
	return 0, false
}

// ResolveString exposes one script token's string-pool value. PVF type-1
// scripts store labels and quoted values as encoded offsets into the archive's
// shared ANSI/UTF-16 pools; callers must keep the encoded offset unchanged
// unless they rebuild every referring script token.
func (a *Archive) ResolveString(magicOffset int) string {
	if a == nil {
		return ""
	}
	return a.resolveString(magicOffset)
}

// StringPoolBytes reports the decoded ANSI/UTF-16 pool sizes, for memory
// diagnostics only.
func (a *Archive) StringPoolBytes() (ans, wide int) {
	if a == nil {
		return 0, 0
	}
	return len(a.strA), len(a.strW)
}

func (a *Archive) FileText(idx int) (string, error) {
	if a == nil {
		return "", fmt.Errorf("%w: archive is nil", ErrInvalidArchive)
	}
	return a.readTextIndex(idx)
}

func (a *Archive) FileRaw(idx int) ([]byte, error) {
	if a == nil {
		return nil, fmt.Errorf("%w: archive is nil", ErrInvalidArchive)
	}
	return a.readRawIndex(idx)
}

func (a *Archive) PreloadAll() (PreloadResult, error) {
	if a == nil {
		return PreloadResult{}, fmt.Errorf("%w: archive is nil", ErrInvalidArchive)
	}
	result := PreloadResult{Groups: len(a.groups)}
	for idx := range a.groups {
		if _, err := a.chunk(idx); err != nil {
			return result, err
		}
		result.Cached++
	}
	return result, nil
}

func (a *Archive) Bytes() []byte {
	if a == nil {
		return nil
	}
	out := make([]byte, len(a.data))
	copy(out, a.data)
	return out
}

func (a *Archive) Reader() *bytes.Reader {
	if a == nil {
		return bytes.NewReader(nil)
	}
	return bytes.NewReader(a.data)
}
