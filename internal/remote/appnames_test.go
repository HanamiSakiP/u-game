package remote

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchAppNames(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case appListPath:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"applist":{"apps":[
				{"appid":440,"name":"Team Fortress 2"},
				{"appid":220,"name":"Half-Life 2"},
				{"appid":730,"name":""}
			]}}`))
		case appDetailPath:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"730":{"success":false}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	names, err := FetchAppNames(context.Background(), AppNamesOptions{
		AppIDs:  []int{440, 730, 999},
		BaseURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("FetchAppNames: %v", err)
	}
	if len(names) != 1 || names[440] != "Team Fortress 2" {
		t.Fatalf("names = %#v", names)
	}
}

func TestFetchAppNamesFallsBackToAppDetails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case appListPath:
			http.Error(w, "blocked", http.StatusServiceUnavailable)
		case appDetailPath:
			appid := r.URL.Query().Get("appids")
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{%q:{"success":true,"data":{"name":"Fallback Game"}}}`, appid)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	names, err := FetchAppNames(context.Background(), AppNamesOptions{
		AppIDs:  []int{440},
		BaseURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("FetchAppNames: %v", err)
	}
	if names[440] != "Fallback Game" {
		t.Fatalf("names = %#v", names)
	}
}

func TestFetchAppNamesHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "blocked", http.StatusForbidden)
	}))
	defer srv.Close()

	if _, err := FetchAppNames(context.Background(), AppNamesOptions{
		AppIDs:  []int{440},
		BaseURL: srv.URL,
	}); err == nil {
		t.Fatal("expected HTTP error")
	}
}
