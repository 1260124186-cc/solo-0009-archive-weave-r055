package httpapi

import (
	"net/http"

	"example.com/solo-0009-archive-weave/internal/catalog"
)

// queryValues forwards every supported filter to the catalog parser. The list
// and collection export endpoints share this helper, so repeated tags, tag
// exclusions and year ranges are parsed identically by both.
func queryValues(r *http.Request) catalog.QueryValues {
	raw := r.URL.Query()
	values := catalog.QueryValues{}
	for _, key := range []string{
		"q", "tag", "exclude_tag",
		"year", "year_from", "year_to",
		"status", "view", "sort", "offset", "limit",
	} {
		if entries, ok := raw[key]; ok {
			values[key] = entries
		}
	}
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
