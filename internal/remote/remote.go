// Package remote 从 SteamDB / Steam 社区 / Steam Web API 获取账号已拥有的游戏。
package remote

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/HanamiSakiP/u-game/internal/model"
)

const (
	steamID64Base = 76561197960265728
	defaultUA     = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
)

// ErrNoGames 表示页面可以访问但没有解析到游戏。
var ErrNoGames = errors.New("未解析到任何游戏")

// Options 控制远程抓取。
type Options struct {
	SteamID   string
	Source    string // auto / steamdb / community / api
	Cookie    string // SteamDB Cookie，支持原始请求头或 Netscape cookies.txt 内容
	UserAgent string
	Timeout   time.Duration
	APIKey    string
	Logf      func(format string, args ...interface{})
}

// Fetch 按 source 获取已拥有游戏，auto 模式会依次尝试 SteamDB、Steam 社区、官方 API。
func Fetch(ctx context.Context, opts Options) (*model.OwnedData, error) {
	source := strings.ToLower(strings.TrimSpace(opts.Source))
	if source == "" || source == "auto" {
		source = "auto"
	}
	// 社区来源直接使用原始标识（自定义 URL、数字昵称等），不做 SteamID64 转换。
	id := CanonicalSteamID(opts.SteamID)
	if source == "community" {
		id = NormalizeSteamID(opts.SteamID)
	}
	if id == "" {
		return nil, errors.New("SteamID 不能为空")
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 30 * time.Second
	}
	ua := strings.TrimSpace(opts.UserAgent)
	if ua == "" {
		ua = defaultUA
	}
	client := &http.Client{Timeout: opts.Timeout}
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...interface{}) {}
	}

	order := []string{source}
	if source == "auto" {
		order = []string{"steamdb", "community", "api"}
	}

	var failures []string
	for _, src := range order {
		var (
			games []model.OwnedGame
			err   error
		)
		switch src {
		case "steamdb":
			games, err = fetchSteamDB(ctx, client, id, opts.Cookie, ua)
		case "community":
			games, err = fetchCommunity(ctx, client, id, ua)
		case "api":
			if strings.TrimSpace(opts.APIKey) == "" {
				err = errors.New("未设置 STEAM_API_KEY 环境变量")
			} else {
				games, err = fetchWebAPI(ctx, client, id, opts.APIKey)
			}
		default:
			return nil, fmt.Errorf("未知来源 %q，可选 auto/steamdb/community/api", opts.Source)
		}
		if err == nil && len(games) > 0 {
			games = dedupeGames(games)
			if len(games) == 0 {
				err = ErrNoGames
			} else {
				if src != source && source == "auto" {
					logf("提示: 已回退到 %s 来源", src)
				}
				return &model.OwnedData{
					Version:   model.Version,
					SteamID:   id,
					Source:    src,
					FetchedAt: time.Now().UTC(),
					Count:     len(games),
					Games:     games,
				}, nil
			}
		}
		if err == nil {
			err = ErrNoGames
		}
		if source == "auto" {
			failures = append(failures, fmt.Sprintf("%s: %v", src, err))
			logf("警告: %s 获取失败: %v", src, err)
		} else {
			failures = append(failures, err.Error())
		}
	}
	return nil, fmt.Errorf("获取已拥有游戏失败:\n- %s", strings.Join(failures, "\n- "))
}

func fetchSteamDB(ctx context.Context, client *http.Client, id, cookie, ua string) ([]model.OwnedGame, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, SteamDBCalculatorURL(id), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("Referer", "https://steamdb.info/calculator/")
	if c := cookieHeaderFor(cookie, "steamdb.info"); c != "" {
		req.Header.Set("Cookie", c)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 SteamDB 失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, fmt.Errorf("读取 SteamDB 页面失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		if isChallenge(body) {
			return nil, fmt.Errorf("SteamDB 返回 HTTP %d（Cloudflare 验证页），请加 `--browser` 用浏览器验证，或提供浏览器 Cookie", resp.StatusCode)
		}
		return nil, fmt.Errorf("SteamDB 返回 HTTP %d", resp.StatusCode)
	}
	if isChallenge(body) {
		return nil, errors.New("SteamDB 返回 Cloudflare 验证页，请加 `--browser` 用浏览器验证，或提供浏览器 Cookie")
	}
	text := string(body)
	for _, marker := range []string{"This profile is private", "profile is private", "Private Profile"} {
		if strings.Contains(text, marker) {
			return nil, errors.New("Steam 资料是私密的，SteamDB 无法读取游戏列表")
		}
	}
	games, err := ParseSteamDB(body)
	if err != nil {
		return nil, fmt.Errorf("解析 SteamDB 页面失败: %w", err)
	}
	return games, nil
}

// ParseSteamDB 从 SteamDB calculator 页面中解析游戏列表。
func ParseSteamDB(body []byte) ([]model.OwnedGame, error) {
	text := string(body)
	seen := make(map[int]string)

	add := func(id int, rawName string) {
		if id <= 0 {
			return
		}
		name := cleanName(rawName)
		if name != "" && isBadName(name) {
			name = ""
		}
		old, ok := seen[id]
		if !ok {
			seen[id] = name
			return
		}
		if name != "" && (old == "" || len(name) > len(old) || (isGenericName(old) && !isGenericName(name))) {
			seen[id] = name
		}
	}

	core := steamDBRowRe.FindAllStringSubmatch(text, -1)
	for _, m := range core {
		id, _ := strconv.Atoi(m[1])
		name := ""
		if dm := dataNameRe.FindStringSubmatch(m[0]); dm != nil {
			name = dm[1]
		}
		if name == "" {
			name = nameFromAnchors(m[2], id)
		}
		add(id, name)
	}
	if len(seen) == 0 {
		for _, anchor := range parseAnchors(text) {
			add(anchor.appID, anchor.name)
		}
	} else {
		// 行里可能只有图标链接，用页面中的 app 链接补全缺失的名称。
		for _, anchor := range parseAnchors(text) {
			if name, ok := seen[anchor.appID]; ok && name == "" {
				add(anchor.appID, anchor.name)
			}
		}
	}
	if len(seen) == 0 {
		for _, m := range steamDBJSONRe.FindAllStringSubmatch(text, -1) {
			id, _ := strconv.Atoi(m[1])
			name := ""
			if gm := jsonNameRe.FindStringSubmatch(m[0]); gm != nil {
				name = unquoteJSON(gm[1])
			}
			add(id, name)
		}
	}
	if len(seen) == 0 {
		return nil, ErrNoGames
	}

	games := make([]model.OwnedGame, 0, len(seen))
	for id, name := range seen {
		if name == "" {
			name = fmt.Sprintf("App %d", id)
		}
		games = append(games, model.OwnedGame{AppID: id, Name: name})
	}
	sortGames(games)
	return games, nil
}

var (
	steamDBRowRe   = regexp.MustCompile(`(?is)<t[rl]\b[^>]*\bdata-appid\s*=\s*["']?(\d+)["']?[^>]*>(.*?)</t[rl]>`)
	dataNameRe     = regexp.MustCompile(`(?is)\bdata-name\s*=\s*["']([^"']*)["']`)
	anchorRe       = regexp.MustCompile(`(?is)<a\b([^>]*)>(.*?)</a>`)
	hrefRe         = regexp.MustCompile(`(?is)\bhref\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	titleRe        = regexp.MustCompile(`(?is)\btitle\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	appIDInHrefRe  = regexp.MustCompile(`(?i)/app/(\d+)`)
	steamDBJSONRe  = regexp.MustCompile(`(?is)\{[^{}]{0,400}?"appid"\s*:\s*(\d+)[^{}]{0,400}?\}`)
	jsonNameRe     = regexp.MustCompile(`(?is)"name"\s*:\s*"((?:\\.|[^"\\])*)"`)
	stripTagsRe    = regexp.MustCompile(`(?s)<[^>]*>`)
	profileGamesRe = regexp.MustCompile(`(?is)data-profile-gameslist\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	rgGamesRe      = regexp.MustCompile(`(?is)"rgGames"\s*:\s*(\[[^\[\]]*\])`)
)

type anchor struct {
	appID int
	name  string
}

func parseAnchors(text string) []anchor {
	var out []anchor
	for _, m := range anchorRe.FindAllStringSubmatch(text, -1) {
		attrs, inner := m[1], m[2]
		href := attrValue(hrefRe, attrs)
		if href == "" {
			continue
		}
		idm := appIDInHrefRe.FindStringSubmatch(href)
		if idm == nil {
			continue
		}
		id, err := strconv.Atoi(idm[1])
		if err != nil || id <= 0 {
			continue
		}
		name := cleanName(inner)
		if name == "" || isBadName(name) {
			name = cleanName(attrValue(titleRe, attrs))
		}
		out = append(out, anchor{appID: id, name: name})
	}
	return out
}

func nameFromAnchors(text string, appid int) string {
	for _, a := range parseAnchors(text) {
		if a.appID == appid && a.name != "" {
			return a.name
		}
	}
	return ""
}

func attrValue(re *regexp.Regexp, attrs string) string {
	m := re.FindStringSubmatch(attrs)
	if m == nil {
		return ""
	}
	if m[1] != "" {
		return m[1]
	}
	return m[2]
}

func cleanName(s string) string {
	s = stripTagsRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}

func isGenericName(name string) bool {
	return regexp.MustCompile(`^App \d+$`).MatchString(name)
}

func isBadName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "steamdb", "steam", "store", "community", "login", "sign in", "register",
		"about", "faq", "charts", "calculator", "search", "apps", "packages", "bundles",
		"depots", "history", "info", "configuration", "manifests", "wishlist", "sales",
		"free packages", "top owners", "patches", "steam api", "profile", "games":
		return true
	}
	if _, err := strconv.Atoi(name); err == nil {
		return true
	}
	return false
}

func unquoteJSON(s string) string {
	v, err := strconv.Unquote(`"` + s + `"`)
	if err != nil {
		return s
	}
	return v
}

func isChallenge(body []byte) bool {
	text := string(body)
	for _, marker := range []string{"_cf_chl_opt", "Checking your browser", "Just a moment", "cf-challenge"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

// communityGamesURL 按原始标识拼出 Steam 社区游戏列表地址。
// 只有 17 位 SteamID64 使用 /profiles/ 路径，其余（自定义 URL、数字昵称等）
// 原样使用 /id/ 路径，避免把 user 提供的标识转换成其他账号。
func communityGamesURL(id string) string {
	const base = "https://steamcommunity.com/"
	if isDigits(id) && len(id) == 17 {
		return base + "profiles/" + url.PathEscape(id) + "/games/?tab=all&xml=1"
	}
	return base + "id/" + url.PathEscape(id) + "/games/?tab=all&xml=1"
}

func fetchCommunity(ctx context.Context, client *http.Client, id, ua string) ([]model.OwnedGame, error) {
	rawURL := communityGamesURL(id)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 Steam 社区失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, fmt.Errorf("读取 Steam 社区页面失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Steam 社区返回 HTTP %d", resp.StatusCode)
	}
	text := string(body)
	if strings.Contains(text, "This profile is private") || strings.Contains(text, "profile is private") {
		return nil, errors.New("Steam 资料是私密的，请在 Steam 隐私设置中公开游戏详情")
	}
	if strings.Contains(text, "The specified profile could not be found") {
		return nil, errors.New("找不到该 Steam 资料，请检查 SteamID")
	}

	games := parseCommunityXML(body)
	if len(games) == 0 {
		games = parseCommunityHTML(text)
	}
	if len(games) == 0 {
		return nil, ErrNoGames
	}
	return games, nil
}

func parseCommunityXML(body []byte) []model.OwnedGame {
	var payload struct {
		SteamID64 string `xml:"steamID64"`
		Games     struct {
			Game []struct {
				AppID int    `xml:"appID"`
				Name  string `xml:"name"`
			} `xml:"game"`
		} `xml:"games"`
	}
	if err := xml.Unmarshal(body, &payload); err != nil {
		return nil
	}
	var out []model.OwnedGame
	for _, g := range payload.Games.Game {
		if g.AppID > 0 {
			out = append(out, model.OwnedGame{AppID: g.AppID, Name: cleanName(g.Name)})
		}
	}
	return out
}

func parseCommunityHTML(text string) []model.OwnedGame {
	raw := ""
	if m := profileGamesRe.FindStringSubmatch(text); m != nil {
		if m[1] != "" {
			raw = m[1]
		} else {
			raw = m[2]
		}
	}
	raw = html.UnescapeString(strings.TrimSpace(raw))
	if raw != "" {
		if games := parseRGames([]byte(raw)); len(games) > 0 {
			return games
		}
	}
	if m := rgGamesRe.FindStringSubmatch(text); m != nil {
		return parseRGames([]byte(m[1]))
	}
	return nil
}

func parseRGames(data []byte) []model.OwnedGame {
	var payload struct {
		Games []struct {
			AppID int    `json:"appid"`
			Name  string `json:"name"`
		} `json:"rgGames"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil
	}
	var out []model.OwnedGame
	for _, g := range payload.Games {
		if g.AppID > 0 {
			out = append(out, model.OwnedGame{AppID: g.AppID, Name: cleanName(g.Name)})
		}
	}
	return out
}

func fetchWebAPI(ctx context.Context, client *http.Client, id, apiKey string) ([]model.OwnedGame, error) {
	id64 := SteamID64(id)
	if id64 == "" {
		return nil, errors.New("官方 API 需要 17 位 SteamID64")
	}
	q := url.Values{}
	q.Set("key", apiKey)
	q.Set("steamid", id64)
	q.Set("include_appinfo", "1")
	q.Set("include_played_free_games", "1")
	q.Set("format", "json")
	rawURL := "https://api.steampowered.com/IPlayerService/GetOwnedGames/v1/?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 Steam Web API 失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, fmt.Errorf("读取 Steam Web API 响应失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Steam Web API 返回 HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		Response struct {
			GameCount int `json:"game_count"`
			Games     []struct {
				AppID int    `json:"appid"`
				Name  string `json:"name"`
			} `json:"games"`
		} `json:"response"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("解析 Steam Web API 响应失败: %w", err)
	}
	var out []model.OwnedGame
	for _, g := range payload.Response.Games {
		if g.AppID > 0 {
			out = append(out, model.OwnedGame{AppID: g.AppID, Name: cleanName(g.Name)})
		}
	}
	if len(out) == 0 {
		return nil, errors.New("Steam Web API 未返回游戏（资料可能私密或未设置 API Key）")
	}
	return out, nil
}

func dedupeGames(games []model.OwnedGame) []model.OwnedGame {
	m := make(map[int]string, len(games))
	for _, g := range games {
		if g.AppID <= 0 {
			continue
		}
		name := cleanName(g.Name)
		old, ok := m[g.AppID]
		if !ok || name != "" && (old == "" || len(name) > len(old) || isGenericName(old) && !isGenericName(name)) {
			m[g.AppID] = name
		}
	}
	out := make([]model.OwnedGame, 0, len(m))
	for id, name := range m {
		if name == "" {
			name = fmt.Sprintf("App %d", id)
		}
		out = append(out, model.OwnedGame{AppID: id, Name: name})
	}
	sortGames(out)
	return out
}

func sortGames(games []model.OwnedGame) {
	sort.SliceStable(games, func(i, j int) bool {
		a, b := strings.ToLower(games[i].Name), strings.ToLower(games[j].Name)
		if a != b {
			return a < b
		}
		return games[i].AppID < games[j].AppID
	})
}

// CanonicalSteamID 把输入的 SteamID、AccountID（32 位账号 ID）、SteamID2/3
// 或资料 URL 统一转换为 17 位 SteamID64；自定义 URL 等无法转换的标识原样返回。
func CanonicalSteamID(raw string) string {
	id := NormalizeSteamID(raw)
	if id == "" {
		return ""
	}
	if id64 := SteamID64(id); id64 != "" {
		return id64
	}
	return id
}

// SteamDBCalculatorURL 返回 SteamDB 的账号计算器页面地址。
func SteamDBCalculatorURL(id string) string {
	return "https://steamdb.info/calculator/" + url.PathEscape(CanonicalSteamID(id)) + "/"
}

// NormalizeSteamID 从 SteamID、资料 URL 或自定义 URL 中提取可用标识。
func NormalizeSteamID(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.Trim(s, `"'`)
	if s == "" {
		return ""
	}
	if strings.Contains(s, "://") {
		if u, err := url.Parse(s); err == nil {
			parts := strings.Split(strings.Trim(u.Path, "/"), "/")
			for i := 0; i+1 < len(parts); i++ {
				if parts[i] == "profiles" || parts[i] == "id" || parts[i] == "user" {
					return parts[i+1]
				}
			}
			if len(parts) >= 2 && parts[0] == "p" {
				return parts[1]
			}
		}
	}
	if strings.Contains(s, "steamcommunity.com/") {
		parts := strings.Split(strings.Trim(s, "/"), "/")
		for i := 0; i+1 < len(parts); i++ {
			if parts[i] == "profiles" || parts[i] == "id" || parts[i] == "user" {
				return parts[i+1]
			}
		}
	}
	return s
}

// SteamID64 将常见的 SteamID 形式转换为 17 位 SteamID64；无法转换时返回空串。
func SteamID64(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if isDigits(s) {
		if len(s) == 17 {
			return s
		}
		n, err := strconv.ParseUint(s, 10, 64)
		if err == nil && n < 1<<32 {
			return strconv.FormatUint(steamID64Base+n, 10)
		}
		return ""
	}
	if strings.HasPrefix(strings.ToUpper(s), "STEAM_") {
		parts := strings.Split(s, ":")
		if len(parts) == 3 {
			y, err1 := strconv.ParseUint(parts[1], 10, 64)
			z, err2 := strconv.ParseUint(parts[2], 10, 64)
			if err1 == nil && err2 == nil {
				return strconv.FormatUint(steamID64Base+z*2+y, 10)
			}
		}
	}
	if strings.HasPrefix(strings.ToUpper(s), "[U:1:") && strings.HasSuffix(s, "]") {
		n, err := strconv.ParseUint(s[5:len(s)-1], 10, 64)
		if err == nil {
			return strconv.FormatUint(steamID64Base+n, 10)
		}
	}
	return ""
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// cookieHeaderFor 从原始 Cookie 或 Netscape cookies.txt 内容中提取指定域名的 Cookie。
func cookieHeaderFor(raw, host string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(raw), "cookie:") {
		raw = strings.TrimSpace(raw[len("cookie:"):])
	}
	if !strings.Contains(raw, "\t") {
		return raw
	}
	var pairs []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "#HttpOnly_") {
			line = strings.TrimPrefix(line, "#HttpOnly_")
		} else if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 7 {
			continue
		}
		domain := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(fields[0])), ".")
		name := strings.TrimSpace(fields[5])
		value := strings.TrimSpace(fields[6])
		if name == "" {
			continue
		}
		if domain != "" && !(strings.EqualFold(host, domain) || strings.HasSuffix(strings.ToLower(host), "."+domain)) {
			continue
		}
		pairs = append(pairs, name+"="+value)
	}
	if len(pairs) == 0 {
		return ""
	}
	return strings.Join(pairs, "; ")
}
