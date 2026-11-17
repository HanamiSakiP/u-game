// Package patches 管理本地游戏补丁记录（补丁名称、哈希和是否已打补丁）。
package patches

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/HanamiSakiP/u-game/internal/model"
)

// Normalize 清理补丁记录：去掉无效 appid、合并重复项并按 appid 排序。
func Normalize(data *model.PatchesData) {
	if data == nil {
		return
	}
	byID := make(map[int]model.Patch, len(data.Patches))
	for _, p := range data.Patches {
		if p.AppID <= 0 {
			continue
		}
		p.Name = strings.TrimSpace(p.Name)
		p.Hash = NormalizeHash(p.Hash)
		if p.UpdatedAt.IsZero() {
			p.UpdatedAt = time.Now().UTC()
		}
		if old, ok := byID[p.AppID]; ok {
			if p.Name == "" {
				p.Name = old.Name
			}
			if p.Hash == "" {
				p.Hash = old.Hash
			}
			if p.UpdatedAt.Before(old.UpdatedAt) {
				p.UpdatedAt = old.UpdatedAt
			}
		}
		byID[p.AppID] = p
	}
	out := make([]model.Patch, 0, len(byID))
	for _, p := range byID {
		out = append(out, p)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].AppID < out[j].AppID })
	data.Version = model.Version
	data.Patches = out
	data.Count = len(out)
	if data.UpdatedAt.IsZero() {
		data.UpdatedAt = time.Now().UTC()
	}
}

// Set 插入或更新一条补丁记录。
func Set(data *model.PatchesData, patch model.Patch) {
	if data == nil || patch.AppID <= 0 {
		return
	}
	patch.Name = strings.TrimSpace(patch.Name)
	patch.Hash = NormalizeHash(patch.Hash)
	patch.UpdatedAt = time.Now().UTC()
	for i := range data.Patches {
		if data.Patches[i].AppID == patch.AppID {
			data.Patches[i] = patch
			Normalize(data)
			return
		}
	}
	data.Patches = append(data.Patches, patch)
	Normalize(data)
}

// Remove 删除指定 appid 的补丁记录，返回删除数量。
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

// Find 返回某个 appid 的补丁记录；没有记录时返回 nil，表示「无补丁」。
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

// StatusLabel 返回补丁状态文案。
func StatusLabel(p *model.Patch) string {
	if p == nil {
		return "无补丁"
	}
	if p.Applied {
		return "已打补丁"
	}
	return "未打补丁"
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
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("打开补丁文件失败: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("读取补丁文件失败: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
