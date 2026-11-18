package remote

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const publisherDetailsTimeout = 30 * time.Second

// PublisherOptions 控制从 Steam Store 获取 appid 的发行商。
type PublisherOptions struct {
	AppIDs    []int
	UserAgent string
	Timeout   time.Duration
	// BaseURL 仅用于测试或镜像；为空时使用 Steam 官方接口。
	BaseURL string
}

type appPublisherResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Publishers []string `json:"publishers"`
	} `json:"data"`
}

// FetchPublishers 逐个查询 Steam Store appdetails，返回 appid 对应的发行商。
// 同一款游戏有多个发行商时用 " / " 连接。没有发行商信息的 appid 不会出现在结果中。
func FetchPublishers(ctx context.Context, opts PublisherOptions) (map[int]string, error) {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 12 * time.Second
	}
	ua := strings.TrimSpace(opts.UserAgent)
	if ua == "" {
		ua = defaultUA
	}
	base := strings.TrimSpace(opts.BaseURL)
	if base == "" {
		base = defaultAppDetailBase
	}

	seen := make(map[int]bool, len(opts.AppIDs))
	ids := make([]int, 0, len(opts.AppIDs))
	for _, id := range opts.AppIDs {
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return map[int]string{}, nil
	}

	ctx, cancel := context.WithTimeout(ctx, publisherDetailsTimeout)
	defer cancel()

	var (
		mu         sync.Mutex
		publishers = make(map[int]string)
		wg         sync.WaitGroup
		sem        = make(chan struct{}, appDetailConcurrency)
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
			if publisher := fetchAppPublisher(ctx, base, ua, timeout, id); publisher != "" {
				mu.Lock()
				publishers[id] = publisher
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if len(publishers) == 0 {
		return nil, errors.New("appdetails 未获取到发行商")
	}
	return publishers, nil
}

func fetchAppPublisher(ctx context.Context, base, ua string, timeout time.Duration, appid int) string {
	q := url.Values{}
	q.Set("appids", strconv.Itoa(appid))
	q.Set("filters", "publishers")
	q.Set("l", appDetailLanguage)
	reqURL := strings.TrimRight(base, "/") + appDetailPath + "?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var payload map[string]appPublisherResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxAppDetailBytes)).Decode(&payload); err != nil {
		return ""
	}
	detail, ok := payload[strconv.Itoa(appid)]
	if !ok || !detail.Success {
		return ""
	}
	var names []string
	for _, publisher := range detail.Data.Publishers {
		if publisher = strings.TrimSpace(publisher); publisher != "" {
			names = append(names, publisher)
		}
	}
	return strings.Join(names, " / ")
}
