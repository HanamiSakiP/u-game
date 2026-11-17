package remote

import (
	"strings"
	"testing"
)

func TestParseSteamDBRows(t *testing.T) {
	html := `<html><body>
<table>
<tr data-appid="440"><td><a href="/app/440/"><img src="x"></a></td><td><a href="/app/440/">Team Fortress 2</a></td><td>$0</td></tr>
<tr data-appid="220"><td><a href="/app/220/"></a></td><td><a href="/app/220/">Half-Life&nbsp;2</a></td></tr>
<tr data-appid="730"><td><a href="/app/730/"><img src="x"></a></td></tr>
</table>
<a href="/app/730/">Counter-Strike 2</a>
<a href="/app/999/">Should Not Be Included</a>
</body></html>`
	games, err := ParseSteamDB([]byte(html))
	if err != nil {
		t.Fatalf("ParseSteamDB: %v", err)
	}
	if len(games) != 3 {
		t.Fatalf("games = %d, want 3: %#v", len(games), games)
	}
	got := map[int]string{}
	for _, g := range games {
		got[g.AppID] = g.Name
	}
	if got[440] != "Team Fortress 2" || got[220] != "Half-Life 2" || got[730] != "Counter-Strike 2" {
		t.Fatalf("unexpected names: %#v", got)
	}
	if _, ok := got[999]; ok {
		t.Fatalf("anchor outside rows should not be added: %#v", got)
	}
}

func TestParseSteamDBAnchorsFallback(t *testing.T) {
	html := `<html><body>
<a href="/calculator/">Calculator</a>
<a href="/app/70/">Half-Life</a>
<a href="/app/80/"><span>Counter-Strike</span></a>
<a href="/app/90/"><img src="icon"></a>
<a href="/app/90/" title="Team Fortress Classic"><img src="icon"></a>
</body></html>`
	games, err := ParseSteamDB([]byte(html))
	if err != nil {
		t.Fatalf("ParseSteamDB: %v", err)
	}
	if len(games) != 3 {
		t.Fatalf("games = %d, want 3: %#v", len(games), games)
	}
	got := map[int]string{}
	for _, g := range games {
		got[g.AppID] = g.Name
	}
	if got[80] != "Counter-Strike" {
		t.Fatalf("nested span name = %q", got[80])
	}
	if got[90] != "Team Fortress Classic" {
		t.Fatalf("title fallback = %q", got[90])
	}
}

func TestParseSteamDBJSONFallback(t *testing.T) {
	html := `<script>var data = [{"appid":70,"name":"Half-Life"},{"appid":80,"name":"Counter-Strike"}]</script>`
	games, err := ParseSteamDB([]byte(html))
	if err != nil {
		t.Fatalf("ParseSteamDB: %v", err)
	}
	if len(games) != 2 {
		t.Fatalf("games = %d, want 2", len(games))
	}
}

func TestParseSteamDBEmpty(t *testing.T) {
	if _, err := ParseSteamDB([]byte(`<html><body>nothing</body></html>`)); err == nil {
		t.Fatal("expected ErrNoGames")
	}
}

func TestParseCommunityXML(t *testing.T) {
	data := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<gamesList>
<steamID64>76561197960287930</steamID64>
<games>
<game><appID>440</appID><name><![CDATA[Team Fortress 2]]></name></game>
<game><appID>220</appID><name>Half-Life 2</name></game>
</games>
</gamesList>`)
	games := parseCommunityXML(data)
	if len(games) != 2 {
		t.Fatalf("games = %d, want 2", len(games))
	}
	if games[0].Name != "Team Fortress 2" || games[0].AppID != 440 {
		t.Fatalf("unexpected first game: %#v", games[0])
	}
}

func TestParseCommunityHTML(t *testing.T) {
	html := `<div id="gameslist_config" data-profile-gameslist="{&quot;rgGames&quot;:[{&quot;appid&quot;:440,&quot;name&quot;:&quot;Team Fortress 2&quot;}]}"></div>`
	games := parseCommunityHTML(html)
	if len(games) != 1 || games[0].AppID != 440 || games[0].Name != "Team Fortress 2" {
		t.Fatalf("unexpected games: %#v", games)
	}
}

func TestNormalizeSteamID(t *testing.T) {
	cases := map[string]string{
		"76561197960287930": "76561197960287930",
		"https://steamcommunity.com/profiles/76561197960287930/": "76561197960287930",
		"https://steamcommunity.com/id/gabelogannewell":          "gabelogannewell",
		"gabelogannewell": "gabelogannewell",
	}
	for in, want := range cases {
		if got := NormalizeSteamID(in); got != want {
			t.Errorf("NormalizeSteamID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSteamID64(t *testing.T) {
	cases := map[string]string{
		"76561197960287930": "76561197960287930",
		"22202":             "76561197960287930",
		"STEAM_1:0:11101":   "76561197960287930",
		"[U:1:22202]":       "76561197960287930",
		"gabelogannewell":   "",
	}
	for in, want := range cases {
		if got := SteamID64(in); got != want {
			t.Errorf("SteamID64(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCookieHeaderFor(t *testing.T) {
	netscape := strings.Join([]string{
		"# Netscape HTTP Cookie File",
		".steamdb.info\tTRUE\t/\tTRUE\t1999999999\tcf_clearance\tabc",
		"#HttpOnly_.steamdb.info\tTRUE\t/\tTRUE\t1999999999\t__cf_bm\tdef",
		".steampowered.com\tTRUE\t/\tTRUE\t1999999999\tsteamLoginSecure\tsecret",
	}, "\n")
	got := cookieHeaderFor(netscape, "steamdb.info")
	if got != "cf_clearance=abc; __cf_bm=def" {
		t.Fatalf("cookie = %q", got)
	}
	if got := cookieHeaderFor("a=1; b=2", "steamdb.info"); got != "a=1; b=2" {
		t.Fatalf("raw cookie = %q", got)
	}
}
