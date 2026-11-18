package browser

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMarionetteMessageRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	msg := []interface{}{0, 1, "WebDriver:NewSession", map[string]interface{}{"capabilities": map[string]interface{}{}}}
	if err := writeMarionetteMessage(&buf, msg); err != nil {
		t.Fatal(err)
	}
	colon := bytes.IndexByte(buf.Bytes(), ':')
	if colon <= 0 {
		t.Fatalf("missing length prefix: %q", buf.String())
	}
	if _, err := fmt.Sscanf(buf.String()[:colon], "%d", new(int)); err != nil {
		t.Fatalf("invalid length prefix: %q", buf.String()[:colon])
	}
	if got := buf.Len() - colon - 1; fmt.Sprint(got) != buf.String()[:colon] {
		t.Fatalf("declared length %q, payload %d bytes", buf.String()[:colon], got)
	}
	raw, err := readMarionetteMessage(bufio.NewReader(&buf))
	if err != nil {
		t.Fatal(err)
	}
	var got []json.RawMessage
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("message = %s", raw)
	}
}

func TestCookieHeaderFiltersHost(t *testing.T) {
	cookies := []webdriverCookie{
		{Name: "cf_clearance", Value: "abc", Domain: ".steamdb.info"},
		{Name: "other", Value: "1", Domain: "example.com"},
		{Name: "__cf_bm", Value: "xyz", Domain: "steamdb.info"},
	}
	got := cookieHeader(cookies, "steamdb.info")
	if got != "cf_clearance=abc; __cf_bm=xyz" {
		t.Fatalf("cookie header = %q", got)
	}
}

func TestChallengeDetection(t *testing.T) {
	if !isChallengePage("Just a moment...", "Checking your browser…") {
		t.Fatal("cloudflare page should be detected")
	}
	if isChallengePage("SteamDB Calculator", "Team Fortress 2 Half-Life 2") {
		t.Fatal("calculator page should not be detected as challenge")
	}
	if !isPrivateProfile("SteamDB", "This profile is private") {
		t.Fatal("private profile should be detected")
	}
}

// TestVerifyWithFirefox 需要本机安装 Firefox，且通过 UGAL_TEST_FIREFOX=1 显式启用。
func TestVerifyWithFirefox(t *testing.T) {
	if os.Getenv("UGAL_TEST_FIREFOX") == "" {
		t.Skip("设置 UGAL_TEST_FIREFOX=1 运行真实 Firefox 测试")
	}
	if _, err := findFirefox(""); err != nil {
		t.Skip(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><html><head><title>ugal browser test</title>
<script>document.documentElement.setAttribute("data-webdriver", String(navigator.webdriver));</script>
</head><body>
<table><tr data-appid="440"><td>Team Fortress 2</td></tr></table>
</body></html>`)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	res, err := Verify(ctx, server.URL, Options{Headless: true, Timeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.HTML, `data-appid="440"`) {
		t.Fatalf("html does not contain game row: %s", truncateText(res.HTML, 300))
	}
	if !strings.Contains(res.HTML, `data-webdriver="false"`) {
		t.Fatalf("navigator.webdriver was not hidden: %s", truncateText(res.HTML, 300))
	}
	if res.UserAgent == "" {
		t.Fatal("user agent is empty")
	}
}

func TestVerifyStopWhenLoaded(t *testing.T) {
	if os.Getenv("UGAL_TEST_FIREFOX") == "" {
		t.Skip("设置 UGAL_TEST_FIREFOX=1 运行真实 Firefox 测试")
	}
	if _, err := findFirefox(""); err != nil {
		t.Skip(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><html><head><title>ugal loaded</title></head>
<body><p>no game table here</p></body></html>`)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	start := time.Now()
	res, err := Verify(ctx, server.URL, Options{
		Headless:       true,
		Timeout:        30 * time.Second,
		StopWhenLoaded: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Fatalf("StopWhenLoaded took too long: %s", elapsed)
	}
	if !strings.Contains(res.HTML, "ugal loaded") {
		t.Fatalf("unexpected html: %s", truncateText(res.HTML, 200))
	}
}

func TestBuildStealthXPI(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stealth.xpi")
	if err := buildStealthXPI(path); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	names := make(map[string]bool)
	for _, f := range zr.File {
		names[f.Name] = true
	}
	for _, name := range []string{"manifest.json", "stealth.js"} {
		if !names[name] {
			t.Fatalf("xpi missing %s: %#v", name, names)
		}
	}
}
