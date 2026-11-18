package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/HanamiSakiP/u-game/internal/model"
	"github.com/HanamiSakiP/u-game/internal/remote"
	"github.com/HanamiSakiP/u-game/internal/steam"
)

// gameNames 合并本地数据中的 appid 名称：已安装 > 已拥有 > 收藏夹 >
// Steam appinfo.vdf > appnames.json 缓存。
func (a *app) gameNames() (map[int]string, error) {
	owned, installed, err := a.loadData()
	if err != nil {
		return nil, err
	}
	names := make(map[int]string)
	if installed != nil {
		for _, ap := range installed.Games {
			if ap.Name != "" {
				names[ap.AppID] = ap.Name
			}
		}
	}
	if owned != nil {
		for _, g := range owned.Games {
			if names[g.AppID] == "" && g.Name != "" {
				names[g.AppID] = g.Name
			}
		}
	}
	if fav, err := a.store.LoadFavorites(); err == nil {
		for _, g := range fav.Games {
			if names[g.AppID] == "" && strings.TrimSpace(g.Name) != "" {
				names[g.AppID] = strings.TrimSpace(g.Name)
			}
		}
	}
	if appInfo, err := steam.LoadAppInfoNames(a.cfg.SteamDir); err == nil {
		for id, name := range appInfo {
			if names[id] == "" && strings.TrimSpace(name) != "" {
				names[id] = strings.TrimSpace(name)
			}
		}
	}
	if cached, err := a.loadAppNames(); err == nil {
		for key, name := range cached.Names {
			id, err := strconv.Atoi(strings.TrimSpace(key))
			if err != nil || id <= 0 || names[id] != "" {
				continue
			}
			if name = strings.TrimSpace(name); name != "" {
				names[id] = name
			}
		}
	}
	return names, nil
}

func (a *app) loadAppNames() (*model.AppNamesData, error) {
	data, err := a.store.LoadAppNames()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &model.AppNamesData{Version: model.Version, Names: map[string]string{}}, nil
		}
		return nil, err
	}
	return data, nil
}

// resolveGameNames 在 gameNames 的基础上，为缺失的 appid 按需从 Steam 获取名称，
// 成功后写入 appnames.json 缓存。offline 为 true 或设置了 UGAL_OFFLINE 时不联网。
func (a *app) resolveGameNames(ids []int, offline bool) (map[int]string, error) {
	names, err := a.gameNames()
	if err != nil {
		return nil, err
	}
	if offlineRequested(offline) {
		return names, nil
	}

	missing := missingNames(ids, names)
	if len(missing) == 0 {
		return names, nil
	}

	fmt.Fprintf(a.err, "正在从 Steam 获取 %d 个游戏名称...\n", len(missing))
	fetched, fetchErr := remote.FetchAppNames(context.Background(), remote.AppNamesOptions{
		AppIDs:    missing,
		UserAgent: a.cfg.UserAgent,
		Timeout:   12 * time.Second,
	})
	if fetchErr != nil {
		fmt.Fprintf(a.err, "提示: 获取游戏名称失败: %v\n", fetchErr)
		return names, nil
	}
	if len(fetched) == 0 {
		fmt.Fprintf(a.err, "提示: 未从 Steam 获取到游戏名称\n")
		return names, nil
	}
	for id, name := range fetched {
		if name = strings.TrimSpace(name); name != "" {
			names[id] = name
		}
	}

	if cache, err := a.loadAppNames(); err == nil {
		for id, name := range fetched {
			cache.Names[strconv.Itoa(id)] = strings.TrimSpace(name)
		}
		if err := a.store.SaveAppNames(cache); err != nil {
			fmt.Fprintf(a.err, "提示: 保存游戏名称缓存失败: %v\n", err)
		}
	}
	fmt.Fprintf(a.err, "已获取 %d 个游戏名称并缓存到 %s\n", len(fetched), a.store.AppNamesPath())
	return names, nil
}

func missingNames(ids []int, names map[int]string) []int {
	seen := make(map[int]bool, len(ids))
	var missing []int
	for _, id := range ids {
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		if strings.TrimSpace(names[id]) == "" {
			missing = append(missing, id)
		}
	}
	return missing
}

func (a *app) loadPublishers() (*model.PublishersData, error) {
	data, err := a.store.LoadPublishers()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &model.PublishersData{Version: model.Version, Publishers: map[string]string{}}, nil
		}
		return nil, err
	}
	return data, nil
}

// resolvePublishers 读取 publishers.json 缓存，并为缺失的 appid 按需从 Steam 获取
// 发行商，成功后写回缓存。offline 为 true 或设置了 UGAL_OFFLINE 时不联网。
func (a *app) resolvePublishers(ids []int, offline bool) (map[int]string, error) {
	cache, err := a.loadPublishers()
	if err != nil {
		return nil, err
	}
	publishers := make(map[int]string, len(cache.Publishers))
	for key, publisher := range cache.Publishers {
		id, err := strconv.Atoi(strings.TrimSpace(key))
		if err != nil || id <= 0 {
			continue
		}
		if publisher = strings.TrimSpace(publisher); publisher != "" {
			publishers[id] = publisher
		}
	}
	if offlineRequested(offline) {
		return publishers, nil
	}

	missing := missingNames(ids, publishers)
	if len(missing) == 0 {
		return publishers, nil
	}

	fmt.Fprintf(a.err, "正在从 Steam 获取 %d 个发行商...\n", len(missing))
	fetched, fetchErr := remote.FetchPublishers(context.Background(), remote.PublisherOptions{
		AppIDs:    missing,
		UserAgent: a.cfg.UserAgent,
		Timeout:   12 * time.Second,
	})
	if fetchErr != nil {
		fmt.Fprintf(a.err, "提示: 获取发行商失败: %v\n", fetchErr)
		return publishers, nil
	}
	if len(fetched) == 0 {
		fmt.Fprintf(a.err, "提示: 未从 Steam 获取到发行商\n")
		return publishers, nil
	}
	for id, publisher := range fetched {
		if publisher = strings.TrimSpace(publisher); publisher != "" {
			publishers[id] = publisher
			cache.Publishers[strconv.Itoa(id)] = publisher
		}
	}
	if err := a.store.SavePublishers(cache); err != nil {
		fmt.Fprintf(a.err, "提示: 保存发行商缓存失败: %v\n", err)
	} else {
		fmt.Fprintf(a.err, "已获取 %d 个发行商并缓存到 %s\n", len(fetched), a.store.PublishersPath())
	}
	return publishers, nil
}

func offlineRequested(flag bool) bool {
	if flag {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("UGAL_OFFLINE"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// applyGameNames 用解析到的名称替换 entries 中的占位名（App <appid>）。
func applyGameNames(entries []entry, names map[int]string) {
	for i := range entries {
		name := strings.TrimSpace(names[entries[i].AppID])
		if name == "" {
			continue
		}
		if entries[i].Name == "" || entries[i].Name == fmt.Sprintf("App %d", entries[i].AppID) {
			entries[i].Name = name
		}
	}
}

// appIDsFromEntries 返回去重后的 entry appid。
func appIDsFromEntries(entries []entry) []int {
	seen := make(map[int]bool, len(entries))
	ids := make([]int, 0, len(entries))
	for _, e := range entries {
		if e.AppID <= 0 || seen[e.AppID] {
			continue
		}
		seen[e.AppID] = true
		ids = append(ids, e.AppID)
	}
	return ids
}
