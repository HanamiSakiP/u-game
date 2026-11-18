// Package patches 管理本地游戏补丁记录（补丁名称、哈希和是否已打补丁）。
package patches

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/HanamiSakiP/u-game/internal/model"
)

// Normalize 清理补丁记录：去掉无效 appid、合并同一 appid 下的同名记录，
// 并按 appid、补丁名称排序。同一 appid 下名称不同的记录会全部保留。
func Normalize(data *model.PatchesData) {
	if data == nil {
		return
	}
	byKey := make(map[string]model.Patch, len(data.Patches))
	for _, p := range data.Patches {
		if p.AppID <= 0 {
			continue
		}
		p.Name = strings.TrimSpace(p.Name)
		p.Hash = NormalizeHash(p.Hash)
		if p.UpdatedAt.IsZero() {
			p.UpdatedAt = time.Now().UTC()
		}
		key := patchKey(p.AppID, p.Name)
		if old, ok := byKey[key]; ok {
			if p.Hash == "" {
				p.Hash = old.Hash
			}
			if p.UpdatedAt.Before(old.UpdatedAt) {
				p.UpdatedAt = old.UpdatedAt
			}
		}
		byKey[key] = p
	}
	out := make([]model.Patch, 0, len(byKey))
	for _, p := range byKey {
		out = append(out, p)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].AppID != out[j].AppID {
			return out[i].AppID < out[j].AppID
		}
		return out[i].Name < out[j].Name
	})
	data.Version = model.Version
	data.Patches = out
	data.Count = len(out)
	if data.UpdatedAt.IsZero() {
		data.UpdatedAt = time.Now().UTC()
	}
}

func patchKey(appid int, name string) string {
	return fmt.Sprintf("%d\x00%s", appid, strings.TrimSpace(name))
}

// Set 插入或更新一条补丁记录。appid 与补丁名称都相同的记录视为同一条。
func Set(data *model.PatchesData, patch model.Patch) {
	if data == nil || patch.AppID <= 0 {
		return
	}
	patch.Name = strings.TrimSpace(patch.Name)
	patch.Hash = NormalizeHash(patch.Hash)
	patch.UpdatedAt = time.Now().UTC()
	for i := range data.Patches {
		if data.Patches[i].AppID == patch.AppID && strings.TrimSpace(data.Patches[i].Name) == patch.Name {
			data.Patches[i] = patch
			Normalize(data)
			return
		}
	}
	data.Patches = append(data.Patches, patch)
	Normalize(data)
}

// Remove 删除指定 appid 的全部补丁记录，返回删除数量。
func Remove(data *model.PatchesData, ids []int) int {
	if data == nil {
		return 0
	}
	drop := make(map[int]bool, len(ids))
	for _, id := range ids {
		if id > 0 {
			drop[id] = true
		}
	}
	kept := data.Patches[:0]
	removed := 0
	for _, p := range data.Patches {
		if drop[p.AppID] {
			removed++
			continue
		}
		kept = append(kept, p)
	}
	data.Patches = kept
	Normalize(data)
	return removed
}

// RemoveByName 删除某个 appid 下指定名称的补丁记录，返回删除数量。
func RemoveByName(data *model.PatchesData, appid int, name string) int {
	if data == nil || appid <= 0 {
		return 0
	}
	name = strings.TrimSpace(name)
	kept := data.Patches[:0]
	removed := 0
	for _, p := range data.Patches {
		if p.AppID == appid && strings.TrimSpace(p.Name) == name {
			removed++
			continue
		}
		kept = append(kept, p)
	}
	data.Patches = kept
	Normalize(data)
	return removed
}

// FindAll 返回某个 appid 的全部补丁记录。
func FindAll(data *model.PatchesData, appid int) []model.Patch {
	if data == nil {
		return nil
	}
	var out []model.Patch
	for i := range data.Patches {
		if data.Patches[i].AppID == appid {
			out = append(out, data.Patches[i])
		}
	}
	return out
}

// FindByName 返回某个 appid 下指定名称的补丁记录；没有记录时返回 nil。
func FindByName(data *model.PatchesData, appid int, name string) *model.Patch {
	if data == nil {
		return nil
	}
	name = strings.TrimSpace(name)
	for i := range data.Patches {
		if data.Patches[i].AppID == appid && strings.TrimSpace(data.Patches[i].Name) == name {
			p := data.Patches[i]
			return &p
		}
	}
	return nil
}

// Find 返回某个 appid 的第一条补丁记录；没有记录时返回 nil，表示「无补丁」。
// 同一 appid 有多条记录时请使用 FindAll。
func Find(data *model.PatchesData, appid int) *model.Patch {
	if data == nil {
		return nil
	}
	for i := range data.Patches {
		if data.Patches[i].AppID == appid {
			p := data.Patches[i]
			return &p
		}
	}
	return nil
}

// StatusLabel 返回单条补丁记录的状态文案。
func StatusLabel(p *model.Patch) string {
	if p == nil {
		return "无补丁"
	}
	if p.Applied {
		return "已打补丁"
	}
	return "未打补丁"
}

// StatusLabelAll 汇总某个 appid 的多条补丁记录，返回整体状态文案。
func StatusLabelAll(ps []model.Patch) string {
	if len(ps) == 0 {
		return "无补丁"
	}
	applied := 0
	for _, p := range ps {
		if p.Applied {
			applied++
		}
	}
	switch {
	case applied == len(ps):
		return "已打补丁"
	case applied == 0:
		return "未打补丁"
	default:
		return "部分已打"
	}
}

// NormalizeHash 去掉常见算法前缀并转为小写。
func NormalizeHash(hash string) string {
	hash = strings.TrimSpace(hash)
	if i := strings.Index(hash, ":"); i > 0 {
		switch strings.ToLower(strings.TrimSpace(hash[:i])) {
		case "sha256", "sha-256", "md5", "sha1", "sha-1":
			hash = strings.TrimSpace(hash[i+1:])
		}
	}
	return strings.ToLower(hash)
}

// HashFile 计算文件的 SHA-256 十六进制摘要。
func HashFile(path string) (string, error) {
	return HashFileWith(path, "sha256")
}

// HashFileMD5 计算文件的 MD5 十六进制摘要。
func HashFileMD5(path string) (string, error) {
	return HashFileWith(path, "md5")
}

// HashFileWith 按指定算法计算文件摘要，支持 sha256（默认）和 md5。
func HashFileWith(path, algorithm string) (string, error) {
	var h hash.Hash
	switch strings.ToLower(strings.TrimSpace(algorithm)) {
	case "", "sha256", "sha-256":
		h = sha256.New()
	case "md5":
		h = md5.New()
	default:
		return "", fmt.Errorf("不支持的哈希算法 %q（支持 sha256、md5）", algorithm)
	}
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("打开补丁文件失败: %w", err)
	}
	defer f.Close()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("读取补丁文件失败: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
