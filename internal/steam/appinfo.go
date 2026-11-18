package steam

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

// appInfoMagicV29 是 Steam appcache/appinfo.vdf 当前常见格式的魔数。
// 该格式使用字符串表 + 二进制 KeyValues。
const appInfoMagicV29 = 0x07564429

// appInfoKVOffset 是单条 appinfo 记录中 KeyValues 数据的起始偏移。
// 记录头包含 infoState、lastUpdated、picsToken、哈希等固定字段。
const appInfoKVOffset = 60

// AppInfoPath 返回 Steam 的 appcache/appinfo.vdf 路径；找不到时返回空字符串。
func AppInfoPath(steamDir string) string {
	root, err := locateSteamRoot(steamDir)
	if err != nil {
		return ""
	}
	dir := filepath.Join(root, "appcache")
	if p := filepath.Join(dir, "appinfo.vdf"); isFile(p) {
		return p
	}
	return findFileCI(dir, "appinfo.vdf")
}

// LoadAppInfoNames 从 Steam 本地 appcache/appinfo.vdf 读取 appid 到游戏名称的映射。
// 文件不存在或格式不受支持时返回错误，调用方可以忽略该错误并使用其他名称来源。
func LoadAppInfoNames(steamDir string) (map[int]string, error) {
	path := AppInfoPath(steamDir)
	if path == "" {
		return nil, os.ErrNotExist
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取 %s 失败: %w", path, err)
	}
	names, err := ParseAppInfoNames(data)
	if err != nil {
		return nil, fmt.Errorf("解析 %s 失败: %w", path, err)
	}
	return names, nil
}

// ParseAppInfoNames 解析 appinfo.vdf 内容，返回 appid 与游戏名称。
func ParseAppInfoNames(data []byte) (map[int]string, error) {
	if len(data) < 16 {
		return nil, errors.New("appinfo.vdf 内容过短")
	}
	magic := binary.LittleEndian.Uint32(data[0:4])
	if magic != appInfoMagicV29 {
		return nil, fmt.Errorf("不支持的 appinfo.vdf 格式: %#x", magic)
	}
	tableOffset := binary.LittleEndian.Uint64(data[8:16])
	if tableOffset < 16 || tableOffset > uint64(len(data)) {
		return nil, errors.New("appinfo.vdf 字符串表偏移无效")
	}
	table, err := parseAppInfoStringTable(data[tableOffset:])
	if err != nil {
		return nil, err
	}

	names := make(map[int]string)
	pos := 16
	for pos+4 <= int(tableOffset) {
		appid := int(binary.LittleEndian.Uint32(data[pos:]))
		pos += 4
		if appid == 0 {
			break
		}
		if pos+4 > int(tableOffset) {
			break
		}
		size := int(binary.LittleEndian.Uint32(data[pos:]))
		pos += 4
		if size < 0 || pos+size > len(data) {
			return names, errors.New("appinfo.vdf 记录长度无效")
		}
		record := data[pos : pos+size]
		pos += size
		if name := appInfoRecordName(record, table); name != "" {
			names[appid] = name
		}
	}
	return names, nil
}

// parseAppInfoStringTable 解析字符串表：uint32 数量 + 若干以 NUL 结尾的字符串，
// 后面可能还有索引表，这里只需要按顺序读取的字符串本身。
func parseAppInfoStringTable(data []byte) ([]string, error) {
	if len(data) < 4 {
		return nil, errors.New("appinfo.vdf 字符串表过短")
	}
	count := int(binary.LittleEndian.Uint32(data[:4]))
	if count < 0 || count > len(data) {
		return nil, errors.New("appinfo.vdf 字符串表数量无效")
	}
	table := make([]string, 0, count)
	pos := 4
	for i := 0; i < count; i++ {
		if pos > len(data) {
			return nil, errors.New("appinfo.vdf 字符串表内容无效")
		}
		end := pos
		for end < len(data) && data[end] != 0 {
			end++
		}
		if end >= len(data) {
			return nil, errors.New("appinfo.vdf 字符串表缺少结尾")
		}
		table = append(table, string(data[pos:end]))
		pos = end + 1
	}
	return table, nil
}

// appInfoRecordName 从一条记录中读取 appinfo.common.name。
func appInfoRecordName(record []byte, table []string) string {
	if len(record) <= appInfoKVOffset {
		return ""
	}
	_, value, _, err := parseAppInfoKV(record, appInfoKVOffset, table)
	if err != nil {
		return ""
	}
	root, ok := value.(map[string]interface{})
	if !ok {
		return ""
	}
	common, ok := root["common"].(map[string]interface{})
	if !ok {
		return ""
	}
	name, _ := common["name"].(string)
	return strings.TrimSpace(name)
}

// parseAppInfoKV 解析二进制 KeyValues。键使用字符串表索引表示。
// 返回值中的 next 是解析完当前节点后的位置。
func parseAppInfoKV(buf []byte, pos int, table []string) (key string, value interface{}, next int, err error) {
	if pos >= len(buf) {
		return "", nil, pos, errors.New("KeyValues 内容越界")
	}
	kind := buf[pos]
	pos++
	if pos+4 > len(buf) {
		return "", nil, pos, errors.New("KeyValues 键索引越界")
	}
	keyIndex := int(binary.LittleEndian.Uint32(buf[pos:]))
	pos += 4
	if keyIndex < 0 || keyIndex >= len(table) {
		return "", nil, pos, fmt.Errorf("KeyValues 键索引 %d 超出字符串表", keyIndex)
	}
	key = table[keyIndex]

	switch kind {
	case 0x00: // object
		obj := make(map[string]interface{})
		for {
			if pos >= len(buf) {
				return "", nil, pos, errors.New("KeyValues 对象缺少结尾")
			}
			if buf[pos] == 0x08 { // end
				return key, obj, pos + 1, nil
			}
			childKey, childValue, childNext, err := parseAppInfoKV(buf, pos, table)
			if err != nil {
				return "", nil, pos, err
			}
			obj[childKey] = childValue
			pos = childNext
		}
	case 0x01: // string
		end := pos
		for end < len(buf) && buf[end] != 0 {
			end++
		}
		if end >= len(buf) {
			return "", nil, pos, errors.New("KeyValues 字符串缺少结尾")
		}
		return key, string(buf[pos:end]), end + 1, nil
	case 0x02, 0x04: // int32 / pointer
		if pos+4 > len(buf) {
			return "", nil, pos, errors.New("KeyValues 整数越界")
		}
		return key, int32(binary.LittleEndian.Uint32(buf[pos:])), pos + 4, nil
	case 0x03: // float32
		if pos+4 > len(buf) {
			return "", nil, pos, errors.New("KeyValues 浮点数越界")
		}
		return key, math.Float32frombits(binary.LittleEndian.Uint32(buf[pos:])), pos + 4, nil
	case 0x05: // wide string (UTF-16LE)
		end := pos
		for end+1 < len(buf) {
			if buf[end] == 0 && buf[end+1] == 0 {
				break
			}
			end += 2
		}
		if end+1 >= len(buf) {
			return "", nil, pos, errors.New("KeyValues 宽字符串缺少结尾")
		}
		units := make([]uint16, 0, (end-pos)/2)
		for i := pos; i+1 < end; i += 2 {
			units = append(units, binary.LittleEndian.Uint16(buf[i:i+2]))
		}
		return key, string(utf16.Decode(units)), end + 2, nil
	case 0x06: // color
		if pos+4 > len(buf) {
			return "", nil, pos, errors.New("KeyValues 颜色越界")
		}
		return key, binary.LittleEndian.Uint32(buf[pos:]), pos + 4, nil
	case 0x07: // uint64
		if pos+8 > len(buf) {
			return "", nil, pos, errors.New("KeyValues uint64 越界")
		}
		return key, binary.LittleEndian.Uint64(buf[pos:]), pos + 8, nil
	case 0x0a: // int64
		if pos+8 > len(buf) {
			return "", nil, pos, errors.New("KeyValues int64 越界")
		}
		return key, int64(binary.LittleEndian.Uint64(buf[pos:])), pos + 8, nil
	default:
		return "", nil, pos, fmt.Errorf("未知 KeyValues 类型 %#x", kind)
	}
}
