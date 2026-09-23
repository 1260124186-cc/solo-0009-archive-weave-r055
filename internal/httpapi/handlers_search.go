package httpapi

import (
	"net/http"
	"strings"

	"example.com/solo-0009-archive-weave/internal/catalog"
)

func queryValues(r *http.Request) map[string]string {
	raw := r.URL.Query()
	values := map[string]string{}
	for _, key := range []string{"q", "tag_mode", "year", "year_from", "year_to", "status", "view", "sort", "offset", "limit"} {
		values[key] = raw.Get(key)
	}
	// tag and exclude_tag accept both repeated parameters and comma-separated lists.
	values["tag"] = strings.Join(raw["tag"], ",")
	values["exclude_tag"] = strings.Join(raw["exclude_tag"], ",")
	return values
}

func (s *Server) listArtifacts(w http.ResponseWriter, r *http.Request) {
	query, err := catalog.ParseQuery(queryValues(r))
	if err != nil {
		writeError(w, err)
		return
	}
	values, err := s.service.List(r.Context(), query)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"count":     len(values),
		"query":     query.Describe(),
		"artifacts": values,
	})
}
