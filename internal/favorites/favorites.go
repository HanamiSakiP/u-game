// Package favorites 读写本地游戏收藏夹，并支持多种常见的导入格式。
package favorites

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/HanamiSakiP/u-game/internal/model"
)

// Normalize 清理收藏夹：去掉无效 appid、合并重复项并按 appid 排序。
func Normalize(data *model.FavoritesData) {
	if data == nil {
		return
	}
	byID := make(map[int]model.Favorite, len(data.Games))
	for _, g := range data.Games {
		if g.AppID <= 0 {
			continue
		}
		if old, ok := byID[g.AppID]; ok {
			if old.Name == "" && g.Name != "" {
				old.Name = g.Name
			}
			if old.Note == "" && g.Note != "" {
				old.Note = g.Note
			}
			if old.AddedAt.IsZero() && !g.AddedAt.IsZero() {
				old.AddedAt = g.AddedAt
			}
			byID[g.AppID] = old
			continue
		}
		if g.AddedAt.IsZero() {
			g.AddedAt = time.Now().UTC()
		}
		byID[g.AppID] = g
	}
	games := make([]model.Favorite, 0, len(byID))
	for _, g := range byID {
		games = append(games, g)
	}
	sort.SliceStable(games, func(i, j int) bool { return games[i].AppID < games[j].AppID })
	data.Version = model.Version
	data.Games = games
	data.Count = len(games)
	if data.UpdatedAt.IsZero() {
		data.UpdatedAt = time.Now().UTC()
	}
}

// Add 将 items 合并进收藏夹，返回新增数量。
func Add(data *model.FavoritesData, items []model.Favorite) int {
	if data == nil {
		return 0
	}
	existing := make(map[int]int, len(data.Games))
	for i, g := range data.Games {
		existing[g.AppID] = i
	}
	added := 0
	for _, item := range items {
		if item.AppID <= 0 {
			continue
		}
		if idx, ok := existing[item.AppID]; ok {
			g := data.Games[idx]
			if g.Name == "" && item.Name != "" {
				g.Name = item.Name
			}
			if g.Note == "" && item.Note != "" {
				g.Note = item.Note
			}
			if g.AddedAt.IsZero() && !item.AddedAt.IsZero() {
				g.AddedAt = item.AddedAt
			}
			data.Games[idx] = g
			continue
		}
		if item.AddedAt.IsZero() {
			item.AddedAt = time.Now().UTC()
		}
		data.Games = append(data.Games, item)
		existing[item.AppID] = len(data.Games) - 1
		added++
	}
	Normalize(data)
	return added
}

// Remove 删除指定 appid，返回删除数量。
func Remove(data *model.FavoritesData, ids []int) int {
	if data == nil {
		return 0
	}
	drop := make(map[int]bool, len(ids))
	for _, id := range ids {
		if id > 0 {
			drop[id] = true
		}
	}
	kept := data.Games[:0]
	removed := 0
	for _, g := range data.Games {
		if drop[g.AppID] {
			removed++
			continue
		}
		kept = append(kept, g)
	}
	data.Games = kept
	Normalize(data)
	return removed
}

// Parse 支持 ugal 导出格式、{"games":[...]}、{"patches":[...]}（即
// patches.json）、对象数组、appid 数组，以及每行 "appid" / "appid,名称" /
// "appid<TAB>名称" 的文本格式。
func Parse(raw []byte) (*model.FavoritesData, error) {
	raw = bytes.TrimSpace(bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF}))
	if len(raw) == 0 {
		return nil, fmt.Errorf("导入内容为空")
	}
	var (
		games []model.Favorite
		err   error
	)
	switch raw[0] {
	case '{':
		games, err = parseObject(raw)
	case '[':
		games, err = parseArray(raw)
	default:
		games, err = parseLines(string(raw))
	}
	if err != nil {
		return nil, err
	}
	data := &model.FavoritesData{Games: games}
	Normalize(data)
	if len(data.Games) == 0 {
		return nil, fmt.Errorf("导入内容中没有有效的 appid")
	}
	return data, nil
}

func parseObject(raw []byte) ([]model.Favorite, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("解析收藏夹 JSON 失败: %w", err)
	}
	for _, key := range []string{"games", "favorites", "items", "list"} {
		if v, ok := obj[key]; ok {
			return parseAnyItems(v)
		}
	}
	// patches.json 也可以直接导入收藏：取其中出现过的 appid。
	// 补丁名称（name）不是游戏名称，这里忽略，由调用方按 appid 解析。
	if v, ok := obj["patches"]; ok {
		games, err := parseAnyItems(v)
		if err != nil {
			return nil, err
		}
		for i := range games {
			games[i].Name = ""
		}
		return games, nil
	}
	if _, ok := obj["appid"]; ok {
		g, ok, err := decodeItem(raw)
		if err != nil {
			return nil, err
		}
		if ok {
			return []model.Favorite{g}, nil
		}
	}
	var games []model.Favorite
	for key, value := range obj {
		id, err := strconv.Atoi(strings.TrimSpace(key))
		if err != nil || id <= 0 {
			continue
		}
		g := model.Favorite{AppID: id}
		var name string
		if json.Unmarshal(value, &name) == nil {
			g.Name = strings.TrimSpace(name)
		} else {
			if item, ok, err := decodeItem(value); err == nil && ok {
				g = item
				if g.AppID <= 0 {
					g.AppID = id
				}
			}
		}
		games = append(games, g)
	}
	if len(games) == 0 {
		return nil, fmt.Errorf("JSON 对象中缺少 games 数组")
	}
	return games, nil
}

func parseArray(raw []byte) ([]model.Favorite, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("解析收藏夹数组失败: %w", err)
	}
	var games []model.Favorite
	for _, item := range items {
		g, ok, err := decodeItem(item)
		if err != nil {
			return nil, err
		}
		if ok {
			games = append(games, g)
		}
	}
	return games, nil
}

func parseAnyItems(raw json.RawMessage) ([]model.Favorite, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, fmt.Errorf("games 字段为空")
	}
	switch raw[0] {
	case '[':
		return parseArray(raw)
	case '{':
		g, ok, err := decodeItem(raw)
		if err != nil {
			return nil, err
		}
		if ok {
			return []model.Favorite{g}, nil
		}
		return nil, fmt.Errorf("无法识别 games 字段")
	default:
		g, ok, err := decodeItem(raw)
		if err != nil {
			return nil, err
		}
		if ok {
			return []model.Favorite{g}, nil
		}
	}
	return nil, fmt.Errorf("无法识别 games 字段")
}

func decodeItem(raw json.RawMessage) (model.Favorite, bool, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return model.Favorite{}, false, nil
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return model.Favorite{}, false, err
		}
		if id, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && id > 0 {
			return model.Favorite{AppID: id}, true, nil
		}
		return model.Favorite{}, false, nil
	}
	if raw[0] == '{' {
		var obj struct {
			AppID    int    `json:"appid"`
			AppIDAlt int    `json:"app_id"`
			ID       int    `json:"id"`
			Name     string `json:"name"`
			Note     string `json:"note"`
			AddedAt  string `json:"added_at"`
		}
		if err := json.Unmarshal(raw, &obj); err != nil {
			return model.Favorite{}, false, err
		}
		id := obj.AppID
		if id <= 0 {
			id = obj.AppIDAlt
		}
		if id <= 0 {
			id = obj.ID
		}
		if id <= 0 {
			return model.Favorite{}, false, nil
		}
		addedAt, _ := time.Parse(time.RFC3339Nano, strings.TrimSpace(obj.AddedAt))
		return model.Favorite{
			AppID:   id,
			Name:    strings.TrimSpace(obj.Name),
			Note:    strings.TrimSpace(obj.Note),
			AddedAt: addedAt,
		}, true, nil
	}
	var id int
	if err := json.Unmarshal(raw, &id); err == nil && id > 0 {
		return model.Favorite{AppID: id}, true, nil
	}
	return model.Favorite{}, false, nil
}

func parseLines(text string) ([]model.Favorite, error) {
	var games []model.Favorite
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idText, name := line, ""
		for _, sep := range []string{"\t", ",", ";", " "} {
			if i := strings.Index(line, sep); i > 0 {
				idText = strings.TrimSpace(line[:i])
				name = strings.TrimSpace(line[i+len(sep):])
				break
			}
		}
		id, err := strconv.Atoi(idText)
		if err != nil || id <= 0 {
			continue
		}
		games = append(games, model.Favorite{AppID: id, Name: name})
	}
	if len(games) == 0 {
		return nil, fmt.Errorf("无法识别的收藏夹格式，支持 JSON 或每行一个 appid")
	}
	return games, nil
}

// Marshal 输出可移植的收藏夹 JSON。
func Marshal(data *model.FavoritesData) ([]byte, error) {
	if data == nil {
		data = &model.FavoritesData{}
	}
	Normalize(data)
	out, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("编码收藏夹失败: %w", err)
	}
	return append(out, '\n'), nil
}
