package catalog

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"example.com/solo-0009-archive-weave/internal/domain"
)

const (
	ViewPublic  = "public"
	ViewWorking = "working"

	SortRecent = "recent"
	SortOldest = "oldest"
	SortTitle  = "title"
)

const (
	minFilterYear = 1000
	maxFilterYear = 2100
	maxFilterTags = 48
)

// QueryValues holds the repeated query string values of a list/export request.
// Repeated keys and comma separated entries are both supported for tag filters.
type QueryValues map[string][]string

type Query struct {
	Keyword     string
	Tags        []string // all must be present (AND)
	ExcludeTags []string // none may be present
	Year        int      // exact year, 0 when unset
	YearFrom    int      // inclusive lower bound, 0 when unset
	YearTo      int      // inclusive upper bound, 0 when unset
	Status      domain.Status
	View        string
	Sort        string
	Offset      int
	Limit       int
}

func DefaultQuery() Query {
	return Query{View: ViewPublic, Sort: SortRecent, Limit: 50}
}

func ParseQuery(values QueryValues) (Query, error) {
	query := DefaultQuery()
	query.Keyword = strings.TrimSpace(scalarValue(values, "q"))
	query.View = strings.ToLower(scalarValue(values, "view"))
	query.Sort = strings.ToLower(scalarValue(values, "sort"))
	query.Status = domain.Status(strings.ToLower(scalarValue(values, "status")))
	if query.View == "" {
		query.View = ViewPublic
	}
	if query.View != ViewPublic && query.View != ViewWorking {
		return query, domain.Invalid("view", "must be public or working")
	}
	if query.Sort == "" {
		query.Sort = SortRecent
	}
	if query.Sort != SortRecent && query.Sort != SortOldest && query.Sort != SortTitle {
		return query, domain.Invalid("sort", "must be recent, oldest or title")
	}
	if query.Status != "" && !query.Status.Valid() {
		return query, domain.Invalid("status", "unknown status")
	}
	if query.View == ViewPublic && query.Status != "" && query.Status != domain.StatusApproved {
		return query, domain.Invalid("status", "public queries can only request approved artifacts")
	}

	tags, err := parseTagFilter("tag", values["tag"])
	if err != nil {
		return query, err
	}
	query.Tags = tags
	excludeTags, err := parseTagFilter("exclude_tag", values["exclude_tag"])
	if err != nil {
		return query, err
	}
	query.ExcludeTags = excludeTags
	for _, tag := range query.Tags {
		if containsName(query.ExcludeTags, tag) {
			return query, domain.Invalid("exclude_tag", "a tag cannot be both required by tag and excluded by exclude_tag")
		}
	}

	if raw := scalarValue(values, "year"); raw != "" {
		year, err := parseYear("year", raw)
		if err != nil {
			return query, err
		}
		query.Year = year
	}
	if raw := scalarValue(values, "year_from"); raw != "" {
		year, err := parseYear("year_from", raw)
		if err != nil {
			return query, err
		}
		query.YearFrom = year
	}
	if raw := scalarValue(values, "year_to"); raw != "" {
		year, err := parseYear("year_to", raw)
		if err != nil {
			return query, err
		}
		query.YearTo = year
	}
	if query.Year > 0 && (query.YearFrom > 0 || query.YearTo > 0) {
		return query, domain.Invalid("year_from", "year cannot be combined with year_from or year_to")
	}
	if query.YearFrom > 0 && query.YearTo > 0 && query.YearFrom > query.YearTo {
		return query, domain.Invalid("year_to", "year_to must be greater than or equal to year_from")
	}

	if raw := scalarValue(values, "offset"); raw != "" {
		offset, err := strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return query, domain.Invalid("offset", "offset must be a non-negative integer")
		}
		query.Offset = offset
	}
	if raw := scalarValue(values, "limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 200 {
			return query, domain.Invalid("limit", "limit must be between 1 and 200")
		}
		query.Limit = limit
	}
	if len([]rune(query.Keyword)) > 160 {
		return query, domain.Invalid("q", "keyword is too long")
	}
	return query, nil
}

func scalarValue(values QueryValues, key string) string {
	entries := values[key]
	if len(entries) == 0 {
		return ""
	}
	return strings.TrimSpace(entries[0])
}

func parseYear(field, raw string) (int, error) {
	year, err := strconv.Atoi(raw)
	if err != nil || year < minFilterYear || year > maxFilterYear {
		return 0, domain.Invalid(field, "year must be a valid four digit number")
	}
	return year, nil
}

// parseTagFilter accepts repeated parameters and comma separated lists,
// normalizes and deduplicates tag names, and keeps the result sorted so that
// the same selection always produces the same query description.
func parseTagFilter(field string, groups []string) ([]string, error) {
	seen := map[string]bool{}
	names := []string{}
	for _, group := range groups {
		for _, part := range strings.Split(group, ",") {
			name := domain.NormalizeTagName(part)
			if name == "" || seen[name] {
				continue
			}
			if utf8.RuneCountInString(name) > 48 {
				return nil, domain.Invalid(field, "tag names must be at most 48 characters")
			}
			seen[name] = true
			names = append(names, name)
		}
	}
	if len(names) > maxFilterTags {
		return nil, domain.Invalid(field, "at most 48 tags can be given")
	}
	sort.Strings(names)
	return names, nil
}

func containsName(names []string, wanted string) bool {
	for _, name := range names {
		if name == wanted {
			return true
		}
	}
	return false
}

func (q Query) includes(value domain.Artifact) bool {
	if q.Status != "" {
		return value.Status == q.Status
	}
	if q.View == ViewWorking {
		return true
	}
	return value.Status == domain.StatusApproved
}

// Describe renders the normalized filters as a stable string. It is shared by
// the list response and collection exports so the two never disagree about
// which filters produced the result set.
func (q Query) Describe() string {
	parts := []string{"view=" + q.View}
	if q.Keyword != "" {
		parts = append(parts, "q="+q.Keyword)
	}
	for _, tag := range q.Tags {
		parts = append(parts, "tag="+tag)
	}
	for _, tag := range q.ExcludeTags {
		parts = append(parts, "exclude_tag="+tag)
	}
	if q.Year > 0 {
		parts = append(parts, fmt.Sprintf("year=%d", q.Year))
	}
	if q.YearFrom > 0 {
		parts = append(parts, fmt.Sprintf("year_from=%d", q.YearFrom))
	}
	if q.YearTo > 0 {
		parts = append(parts, fmt.Sprintf("year_to=%d", q.YearTo))
	}
	if q.Status != "" {
		parts = append(parts, "status="+string(q.Status))
	}
	parts = append(parts, "sort="+q.Sort)
	return strings.Join(parts, ",")
}

func (s *Service) List(ctx context.Context, query Query) ([]domain.Artifact, error) {
	values, err := s.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	filtered := filterArtifacts(values, query)
	sortArtifacts(filtered, query.Sort)
	start := query.Offset
	if start >= len(filtered) {
		return []domain.Artifact{}, nil
	}
	end := len(filtered)
	if query.Limit > 0 && start+query.Limit < end {
		end = start + query.Limit
	}
	return append([]domain.Artifact(nil), filtered[start:end]...), nil
}

func filterArtifacts(values []domain.Artifact, query Query) []domain.Artifact {
	result := make([]domain.Artifact, 0, len(values))
	keyword := strings.ToLower(strings.TrimSpace(query.Keyword))
	for _, value := range values {
		if !query.includes(value) {
			continue
		}
		metadata := domain.BuildMetadata(domain.CreateArtifact{
			Title:   value.Title,
			Summary: value.Summary,
			Source:  value.Source,
			Year:    value.Year,
			Tags:    domain.TagNames(value.Tags),
		})
		if keyword != "" && !strings.Contains(metadata.SearchText(), keyword) {
			continue
		}
		if !hasAllTags(value.Tags, query.Tags) {
			continue
		}
		if hasAnyTag(value.Tags, query.ExcludeTags) {
			continue
		}
		if query.Year > 0 && value.Year != query.Year {
			continue
		}
		if query.YearFrom > 0 && value.Year < query.YearFrom {
			continue
		}
		if query.YearTo > 0 && value.Year > query.YearTo {
			continue
		}
		result = append(result, value)
	}
	return result
}

func hasAllTags(tags []domain.Tag, wanted []string) bool {
	for _, name := range wanted {
		if !domain.HasTag(tags, name) {
			return false
		}
	}
	return true
}

func hasAnyTag(tags []domain.Tag, rejected []string) bool {
	for _, name := range rejected {
		if domain.HasTag(tags, name) {
			return true
		}
	}
	return false
}

func sortArtifacts(values []domain.Artifact, order string) {
	sort.Slice(values, func(i, j int) bool {
		switch order {
		case SortOldest:
			if values[i].UpdatedAt.Equal(values[j].UpdatedAt) {
				return values[i].ID < values[j].ID
			}
			return values[i].UpdatedAt.Before(values[j].UpdatedAt)
		case SortTitle:
			left := strings.ToLower(values[i].Title)
			right := strings.ToLower(values[j].Title)
			if left == right {
				return values[i].ID < values[j].ID
			}
			return left < right
		default:
			if values[i].UpdatedAt.Equal(values[j].UpdatedAt) {
				return values[i].ID < values[j].ID
			}
			return values[i].UpdatedAt.After(values[j].UpdatedAt)
		}
	})
}
