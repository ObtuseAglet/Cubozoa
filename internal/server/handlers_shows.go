package server

import (
	"net/http"
	"sort"
	"time"

	"github.com/obtuseaglet/cubozoa/internal/jellyfin"
	"github.com/obtuseaglet/cubozoa/internal/store"
)

// GET /Shows/{seriesId}/Seasons — the seasons of a series, ordered by number.
func (s *Server) handleSeasons(w http.ResponseWriter, r *http.Request) {
	seriesID := r.PathValue("seriesId")
	seasons, err := s.media.Seasons(seriesID)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError)
		return
	}
	udMap := s.userData.Map(userFrom(r).ID)
	s.writeJSON(w, http.StatusOK, jellyfin.QueryResult[jellyfin.BaseItemDto]{
		Items:            s.itemsToDtos(seasons, udMap),
		TotalRecordCount: len(seasons),
	})
}

// GET /Shows/{seriesId}/Episodes — the episodes of a series, ordered by season
// then episode. An optional ?seasonId= narrows to a single season.
func (s *Server) handleEpisodes(w http.ResponseWriter, r *http.Request) {
	seriesID := r.PathValue("seriesId")
	episodes, err := s.media.Episodes(seriesID, r.URL.Query().Get("seasonId"))
	if err != nil {
		s.writeError(w, http.StatusInternalServerError)
		return
	}
	udMap := s.userData.Map(userFrom(r).ID)
	s.writeJSON(w, http.StatusOK, jellyfin.QueryResult[jellyfin.BaseItemDto]{
		Items:            s.itemsToDtos(episodes, udMap),
		TotalRecordCount: len(episodes),
	})
}

// GET /Shows/NextUp — for each series the user has started, the episode to watch
// next: the in-progress episode if any, otherwise the one after the last
// watched. Series are ordered by most recent activity.
func (s *Server) handleNextUp(w http.ResponseWriter, r *http.Request) {
	episodes, err := s.media.AllEpisodes()
	if err != nil {
		s.writeError(w, http.StatusInternalServerError)
		return
	}
	uid := userFrom(r).ID
	udMap := s.userData.Map(uid)

	picks := nextUp(episodes, udMap)
	if limit := parseInt(r.URL.Query().Get("Limit"), 0); limit > 0 && limit < len(picks) {
		picks = picks[:limit]
	}
	s.writeJSON(w, http.StatusOK, jellyfin.QueryResult[jellyfin.BaseItemDto]{
		Items:            s.itemsToDtos(picks, udMap),
		TotalRecordCount: len(picks),
	})
}

// nextUp computes the "continue watching the next episode" list. episodes must
// already be ordered by series, then season, then episode.
func nextUp(episodes []*store.MediaItem, ud map[string]*store.UserItemData) []*store.MediaItem {
	bySeries := map[string][]*store.MediaItem{}
	var order []string
	for _, e := range episodes {
		if _, ok := bySeries[e.SeriesID]; !ok {
			order = append(order, e.SeriesID)
		}
		bySeries[e.SeriesID] = append(bySeries[e.SeriesID], e)
	}

	type candidate struct {
		ep   *store.MediaItem
		seen time.Time
	}
	var cands []candidate

	for _, sid := range order {
		list := bySeries[sid]
		lastPlayed, inProgress := -1, -1
		var activity time.Time
		for i, e := range list {
			d := ud[e.ID]
			if d == nil {
				continue
			}
			if d.Played {
				lastPlayed = i
			} else if d.PlaybackPositionTicks > 0 && inProgress < 0 {
				inProgress = i
			}
			if d.LastPlayedAt.After(activity) {
				activity = d.LastPlayedAt
			}
		}

		var pick *store.MediaItem
		switch {
		case inProgress >= 0:
			pick = list[inProgress]
		case lastPlayed >= 0 && lastPlayed+1 < len(list):
			pick = list[lastPlayed+1]
		}
		if pick != nil {
			cands = append(cands, candidate{pick, activity})
		}
	}

	sort.SliceStable(cands, func(i, j int) bool { return cands[i].seen.After(cands[j].seen) })
	out := make([]*store.MediaItem, len(cands))
	for i, c := range cands {
		out[i] = c.ep
	}
	return out
}
