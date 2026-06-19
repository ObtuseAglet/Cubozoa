package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// TMDb is a Provider backed by The Movie Database (themoviedb.org) v3 API.
type TMDb struct {
	apiKey    string
	baseURL   string // e.g. https://api.themoviedb.org/3
	imageBase string // e.g. https://image.tmdb.org/t/p/w780
	client    *http.Client
}

// TMDbOption customizes a TMDb provider (used mainly for tests/self-hosting).
type TMDbOption func(*TMDb)

// WithBaseURL overrides the API base URL.
func WithBaseURL(u string) TMDbOption { return func(t *TMDb) { t.baseURL = strings.TrimRight(u, "/") } }

// WithImageBase overrides the image base URL.
func WithImageBase(u string) TMDbOption {
	return func(t *TMDb) { t.imageBase = strings.TrimRight(u, "/") }
}

// WithHTTPClient overrides the HTTP client.
func WithHTTPClient(c *http.Client) TMDbOption { return func(t *TMDb) { t.client = c } }

// NewTMDb constructs a TMDb provider. An empty apiKey yields ok=false so callers
// can leave metadata disabled.
func NewTMDb(apiKey string, opts ...TMDbOption) (*TMDb, bool) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, false
	}
	t := &TMDb{
		apiKey:    apiKey,
		baseURL:   "https://api.themoviedb.org/3",
		imageBase: "https://image.tmdb.org/t/p/w780",
		client:    &http.Client{Timeout: 15 * time.Second},
	}
	for _, o := range opts {
		o(t)
	}
	return t, true
}

// tmdb JSON shapes (only the fields we read).
type tmdbSearch struct {
	Results []struct {
		ID int `json:"id"`
	} `json:"results"`
}

type tmdbDetails struct {
	Title        string  `json:"title"` // movies
	Name         string  `json:"name"`  // series
	Overview     string  `json:"overview"`
	VoteAverage  float64 `json:"vote_average"`
	ReleaseDate  string  `json:"release_date"`   // movies
	FirstAirDate string  `json:"first_air_date"` // series
	PosterPath   string  `json:"poster_path"`
	BackdropPath string  `json:"backdrop_path"`
	Genres       []struct {
		Name string `json:"name"`
	} `json:"genres"`
}

// Movie searches for a movie and returns its details.
func (t *TMDb) Movie(ctx context.Context, title string, year int) (*Result, error) {
	return t.lookup(ctx, "movie", title, year)
}

// Series searches for a TV series and returns its details.
func (t *TMDb) Series(ctx context.Context, title string, year int) (*Result, error) {
	return t.lookup(ctx, "tv", title, year)
}

func (t *TMDb) lookup(ctx context.Context, kind, title string, year int) (*Result, error) {
	id, err := t.search(ctx, kind, title, year)
	if err != nil {
		return nil, err
	}
	return t.details(ctx, kind, id)
}

func (t *TMDb) search(ctx context.Context, kind, title string, year int) (int, error) {
	q := url.Values{}
	q.Set("api_key", t.apiKey)
	q.Set("query", title)
	if year > 0 {
		if kind == "tv" {
			q.Set("first_air_date_year", strconv.Itoa(year))
		} else {
			q.Set("year", strconv.Itoa(year))
		}
	}
	var out tmdbSearch
	if err := t.getJSON(ctx, "/search/"+kind+"?"+q.Encode(), &out); err != nil {
		return 0, err
	}
	if len(out.Results) == 0 {
		return 0, ErrNotFound
	}
	return out.Results[0].ID, nil
}

func (t *TMDb) details(ctx context.Context, kind string, id int) (*Result, error) {
	var d tmdbDetails
	path := fmt.Sprintf("/%s/%d?api_key=%s", kind, id, url.QueryEscape(t.apiKey))
	if err := t.getJSON(ctx, path, &d); err != nil {
		return nil, err
	}

	res := &Result{
		Title:    firstNonEmpty(d.Title, d.Name),
		Overview: d.Overview,
		Rating:   d.VoteAverage,
		Year:     parseYear(firstNonEmpty(d.ReleaseDate, d.FirstAirDate)),
	}
	for _, g := range d.Genres {
		res.Genres = append(res.Genres, g.Name)
	}
	if d.PosterPath != "" {
		res.PrimaryImageURL = t.imageBase + d.PosterPath
	}
	if d.BackdropPath != "" {
		res.BackdropImageURL = t.imageBase + d.BackdropPath
	}
	return res, nil
}

func (t *TMDb) getJSON(ctx context.Context, path string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("metadata: request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("metadata: unexpected status %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

func parseYear(date string) int {
	if len(date) >= 4 {
		if y, err := strconv.Atoi(date[:4]); err == nil {
			return y
		}
	}
	return 0
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
