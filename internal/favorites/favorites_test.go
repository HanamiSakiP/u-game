package favorites

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/HanamiSakiP/u-game/internal/model"
)

func TestParseFormats(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []int
	}{
		{"export", `{"version":1,"count":2,"games":[{"appid":440,"name":"TF2"},{"appid":220}]}`, []int{220, 440}},
		{"wrapped array", `{"games":[440,220]}`, []int{220, 440}},
		{"plain array", `[440,"220"]`, []int{220, 440}},
		{"map", `{"440":"TF2","220":"HL2"}`, []int{220, 440}},
		{"lines", "440,Team Fortress 2\n220\thalf-life\n# comment\n", []int{220, 440}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := Parse([]byte(tc.raw))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			got := make([]int, len(data.Games))
			for i, g := range data.Games {
				got[i] = g.AppID
			}
			if len(got) != len(tc.want) {
				t.Fatalf("games = %#v", got)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("games = %#v, want %#v", got, tc.want)
				}
			}
		})
	}
}

func TestParseInvalid(t *testing.T) {
	for _, raw := range []string{"", "   ", "hello world", `{"foo":1}`} {
		if _, err := Parse([]byte(raw)); err == nil {
			t.Fatalf("Parse(%q) expected error", raw)
		}
	}
}

func TestParsePatchesFile(t *testing.T) {
	raw := `{"version":1,"count":2,"patches":[
		{"appid":440,"name":"HD Patch","applied":true},
		{"appid":220,"name":"patch.xp3","applied":false}
	]}`
	data, err := Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if data.Count != 2 || data.Games[0].AppID != 220 || data.Games[1].AppID != 440 {
		t.Fatalf("games = %#v", data.Games)
	}
	for _, g := range data.Games {
		if g.Name != "" {
			t.Fatalf("patch name should not become game name: %#v", g)
		}
	}
}

func TestAddRemoveAndNormalize(t *testing.T) {
	addedAt := time.Now().UTC().Add(-time.Hour)
	data := &model.FavoritesData{Games: []model.Favorite{
		{AppID: 440, Name: "TF2", AddedAt: addedAt},
		{AppID: 440, Note: "dup"},
	}}
	added := Add(data, []model.Favorite{
		{AppID: 220, Name: "HL2"},
		{AppID: 440, Note: "keep"},
	})
	if added != 1 {
		t.Fatalf("added = %d", added)
	}
	if data.Count != 2 || len(data.Games) != 2 {
		t.Fatalf("data = %#v", data)
	}
	if data.Games[0].AppID != 220 || data.Games[1].AppID != 440 {
		t.Fatalf("not sorted: %#v", data.Games)
	}
	if data.Games[0].AddedAt.IsZero() || data.Games[1].AddedAt.IsZero() {
		t.Fatal("added_at not set")
	}
	if removed := Remove(data, []int{220, 999}); removed != 1 {
		t.Fatalf("removed = %d", removed)
	}
	if data.Count != 1 || data.Games[0].AppID != 440 {
		t.Fatalf("data = %#v", data)
	}
}

func TestMarshalRoundTrip(t *testing.T) {
	data := &model.FavoritesData{Games: []model.Favorite{{AppID: 440, Name: "TF2"}}}
	raw, err := Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	var decoded model.FavoritesData
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Count != 1 || decoded.Games[0].AppID != 440 {
		t.Fatalf("decoded = %#v", decoded)
	}
	again, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if again.Count != 1 || again.Games[0].Name != "TF2" {
		t.Fatalf("again = %#v", again)
	}
}

func TestNormalizeStatus(t *testing.T) {
	cases := map[string]string{
		"":            "",
		"  在玩 ":       StatusPlaying,
		"playing":     StatusPlaying,
		"IN-PROGRESS": StatusPlaying,
		"已通关":         StatusFinished,
		"completed":   StatusFinished,
		"想玩":          StatusWant,
		"wishlist":    StatusWant,
		"搁置":          StatusOnHold,
		"on-hold":     StatusOnHold,
		"none":        "",
		"未标记":         "",
		"自定义状态":       "自定义状态",
	}
	for in, want := range cases {
		if got := NormalizeStatus(in); got != want {
			t.Fatalf("NormalizeStatus(%q) = %q, want %q", in, got, want)
		}
	}
	if StatusLabel("") != "未标记" {
		t.Fatalf("StatusLabel(empty) = %q", StatusLabel(""))
	}
}

func TestAddMergeStatus(t *testing.T) {
	data := &model.FavoritesData{Games: []model.Favorite{
		{AppID: 440, Name: "TF2", Status: StatusPlaying},
	}}
	Add(data, []model.Favorite{
		{AppID: 440, Note: "keep", Status: StatusFinished},
		{AppID: 220, Name: "HL2", Status: "playing"},
	})
	if data.Games[1].Status != StatusPlaying {
		t.Fatalf("existing status should be kept: %#v", data.Games[1])
	}
	if data.Games[0].Status != StatusPlaying {
		t.Fatalf("new status should be normalized: %#v", data.Games[0])
	}
	if data.Games[1].Note != "keep" {
		t.Fatalf("note should merge: %#v", data.Games[1])
	}
}

func TestParseStatus(t *testing.T) {
	data, err := Parse([]byte(`{"games":[{"appid":440,"name":"TF2","status":"playing"},{"appid":220,"status":"已通关"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	byID := map[int]string{}
	for _, g := range data.Games {
		byID[g.AppID] = g.Status
	}
	if byID[440] != StatusPlaying || byID[220] != StatusFinished {
		t.Fatalf("status not parsed: %#v", byID)
	}
}
