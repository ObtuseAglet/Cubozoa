package metadata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mockTMDb stands in for the TMDb API, serving canned search and details JSON.
func mockTMDb(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/search/movie", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("query") == "" {
			http.Error(w, "no query", http.StatusBadRequest)
			return
		}
		w.Write([]byte(`{"results":[{"id":27205}]}`))
	})
	mux.HandleFunc("/movie/27205", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{
			"title":"Inception","overview":"A thief who steals secrets.",
			"vote_average":8.4,"release_date":"2010-07-16",
			"poster_path":"/poster.jpg","backdrop_path":"/back.jpg",
			"genres":[{"name":"Action"},{"name":"Science Fiction"}]
		}`))
	})
	mux.HandleFunc("/search/tv", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"results":[]}`)) // no match
	})
	return httptest.NewServer(mux)
}

func TestTMDbMovieLookup(t *testing.T) {
	srv := mockTMDb(t)
	defer srv.Close()

	tmdb, ok := NewTMDb("test-key", WithBaseURL(srv.URL), WithImageBase("https://img.example"))
	if !ok {
		t.Fatal("provider should be enabled with a key")
	}

	res, err := tmdb.Movie(context.Background(), "Inception", 2010)
	if err != nil {
		t.Fatal(err)
	}
	if res.Title != "Inception" || res.Year != 2010 {
		t.Fatalf("title/year = %q/%d", res.Title, res.Year)
	}
	if res.Rating != 8.4 {
		t.Fatalf("rating = %v", res.Rating)
	}
	if len(res.Genres) != 2 || res.Genres[0] != "Action" {
		t.Fatalf("genres = %v", res.Genres)
	}
	if !strings.HasPrefix(res.Overview, "A thief") {
		t.Fatalf("overview = %q", res.Overview)
	}
	if res.PrimaryImageURL != "https://img.example/poster.jpg" {
		t.Fatalf("poster url = %q", res.PrimaryImageURL)
	}
	if res.BackdropImageURL != "https://img.example/back.jpg" {
		t.Fatalf("backdrop url = %q", res.BackdropImageURL)
	}
}

func TestTMDbNoMatch(t *testing.T) {
	srv := mockTMDb(t)
	defer srv.Close()
	tmdb, _ := NewTMDb("test-key", WithBaseURL(srv.URL))

	if _, err := tmdb.Series(context.Background(), "Nonexistent Show", 0); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestNewTMDbDisabledWithoutKey(t *testing.T) {
	if _, ok := NewTMDb(""); ok {
		t.Fatal("provider must be disabled without an API key")
	}
	if _, ok := NewTMDb("   "); ok {
		t.Fatal("provider must be disabled with a blank API key")
	}
}
