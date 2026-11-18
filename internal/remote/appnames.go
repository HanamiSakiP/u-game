package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultAppListBase   = "https://api.steampowered.com"
	appListPath          = "/ISteamApps/GetAppList/v2/"
	defaultAppDetailBase = "https://store.steampowered.com"
	appDetailPath        = "/api/appdetails"
	maxAppListBytes      = 128 << 20
	maxAppDetailBytes    = 4 << 20
	appDetailConcurrency = 3
	appDetailLanguage    = "schinese"
	appDetailsTimeout    = 10 * time.Second
)

// AppNamesOptions 控制从 Steam Web API 获取 appid 名称。
type AppNamesOptions struct {
	// AppIDs 是需要的 appid；为空时返回全部应用名称。
	AppIDs    []int
	UserAgent string
	Timeout   time.Duration
	// BaseURL 仅用于测试或镜像；为空时使用 Steam 官方接口。
	BaseURL string
}

type appListResponse struct {
	AppList struct {
		Apps []struct {
			AppID int    `json:"appid"`
			Name  string `json:"name"`
		} `json:"apps"`
	} `json:"applist"`
}

type appDetailResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Name string `json:"name"`
	} `json:"data"`
}

// FetchAppNames 获取 appid 对应的游戏名称。先请求一次 ISteamApps/GetAppList/v2，
// 未能覆盖的 appid 再逐个查询 store appdetails，尽量兼容不同的网络环境。
func FetchAppNames(ctx context.Context, opts AppNamesOptions) (map[int]string, error) {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ua := strings.TrimSpace(opts.UserAgent)
	if ua == "" {
		ua = defaultUA
	}
	base := strings.TrimSpace(opts.BaseURL)
	if base == "" {
		base = defaultAppListBase
	}

	wanted := make(map[int]bool, len(opts.AppIDs))
	for _, id := range opts.AppIDs {
		if id > 0 {
			wanted[id] = true
		}
	}

	names := make(map[int]string, len(wanted))
	var failures []string

	listNames, err := fetchAppList(ctx, base, ua, timeout, wanted)
	if err != nil {
		failures = append(failures, err.Error())
	} else {
		for id, name := range listNames {
			names[id] = name
		}
	}

	missing := missingAppIDs(wanted, names)
	if len(missing) > 0 {
		detailBase := defaultAppDetailBase
		if strings.TrimSpace(opts.BaseURL) != "" {
			detailBase = opts.BaseURL
		}
		detailNames := fetchAppDetails(ctx, detailBase, ua, missing)
		for id, name := range detailNames {
			names[id] = name
		}
		if len(detailNames) == 0 && len(missing) > 0 {
			failures = append(failures, "appdetails 未获取到名称")
		}
	}

	if len(names) == 0 && len(failures) > 0 {
		return nil, errors.New(strings.Join(failures, "; "))
	}
	return names, nil
}

func fetchAppList(ctx context.Context, base, ua string, timeout time.Duration, wanted map[int]bool) (map[int]string, error) {
	endpoint := strings.TrimRight(base, "/") + appListPath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("Steam AppList 请求失败: HTTP %d %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload appListResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxAppListBytes)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("解析 Steam AppList 失败: %w", err)
	}
	names := make(map[int]string)
	for _, app := range payload.AppList.Apps {
		name := strings.TrimSpace(app.Name)
		if app.AppID <= 0 || name == "" {
			continue
		}
		if len(wanted) > 0 && !wanted[app.AppID] {
			continue
		}
		names[app.AppID] = name
	}
	return names, nil
}

func fetchAppDetails(ctx context.Context, base, ua string, ids []int) map[int]string {
	ctx, cancel := context.WithTimeout(ctx, appDetailsTimeout)
	defer cancel()

	var (
		mu    sync.Mutex
		names = make(map[int]string)
		wg    sync.WaitGroup
		sem   = make(chan struct{}, appDetailConcurrency)
	)
	for _, id := range ids {
		id := id
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			if name := fetchAppDetail(ctx, base, ua, id); name != "" {
				mu.Lock()
				names[id] = name
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return names
}

func fetchAppDetail(ctx context.Context, base, ua string, appid int) string {
	q := url.Values{}
	q.Set("appids", strconv.Itoa(appid))
	q.Set("filters", "basic")
	q.Set("l", appDetailLanguage)
	reqURL := strings.TrimRight(base, "/") + appDetailPath + "?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: appDetailsTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var payload map[string]appDetailResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxAppDetailBytes)).Decode(&payload); err != nil {
		return ""
	}
	detail, ok := payload[strconv.Itoa(appid)]
	if !ok || !detail.Success {
		return ""
	}
	return strings.TrimSpace(detail.Data.Name)
}

func missingAppIDs(wanted map[int]bool, names map[int]string) []int {
	if len(wanted) == 0 {
		return nil
	}
	missing := make([]int, 0, len(wanted))
	for id := range wanted {
		if strings.TrimSpace(names[id]) == "" {
			missing = append(missing, id)
		}
	}
	return missing
}
