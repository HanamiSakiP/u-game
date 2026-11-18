package remote

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchPublishers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != appDetailPath {
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("filters"); got != "publishers" {
			t.Errorf("filters = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("appids") {
		case "440":
			_, _ = w.Write([]byte(`{"440":{"success":true,"data":{"publishers":["Valve"]}}}`))
		case "220":
			_, _ = w.Write([]byte(`{"220":{"success":true,"data":{"publishers":["Valve","Sierra"]}}}`))
		default:
			_, _ = w.Write([]byte(`{"999":{"success":false}}`))
		}
	}))
	defer srv.Close()

	publishers, err := FetchPublishers(context.Background(), PublisherOptions{
		AppIDs:  []int{440, 220, 999},
		BaseURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("FetchPublishers: %v", err)
	}
	if len(publishers) != 2 {
		t.Fatalf("publishers = %#v", publishers)
	}
	if publishers[440] != "Valve" || publishers[220] != "Valve / Sierra" {
		t.Fatalf("publishers = %#v", publishers)
	}
}

func TestFetchPublishersError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"440":{"success":false}}`)
	}))
	defer srv.Close()

	if _, err := FetchPublishers(context.Background(), PublisherOptions{
		AppIDs:  []int{440},
		BaseURL: srv.URL,
	}); err == nil {
		t.Fatal("expected error when no publisher is returned")
	}
}
