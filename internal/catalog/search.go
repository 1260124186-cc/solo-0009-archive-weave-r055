package catalog

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"example.com/solo-0009-archive-weave/internal/domain"
)

const (
	ViewPublic  = "public"
	ViewWorking = "working"

	SortRecent = "recent"
	SortOldest = "oldest"
	SortTitle  = "title"

	TagMatchAll = "all"
	TagMatchAny = "any"
)

const maxFilterTags = 24

type Query struct {
	Keyword     string
	Tags        []string
	TagMode     string
	ExcludeTags []string
	Year        int
	YearFrom    int
	YearTo      int
	Status      domain.Status
	View        string
	Sort        string
	Offset      int
	Limit       int
}

func DefaultQuery() Query {
	return Query{View: ViewPublic, Sort: SortRecent, TagMode: TagMatchAll, Limit: 50}
}

func ParseQuery(values map[string]string) (Query, error) {
	query := DefaultQuery()
	query.Keyword = strings.TrimSpace(values["q"])
	query.View = strings.ToLower(strings.TrimSpace(values["view"]))
	query.Sort = strings.ToLower(strings.TrimSpace(values["sort"]))
	query.Status = domain.Status(strings.ToLower(strings.TrimSpace(values["status"])))
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
	tags, err := parseTagFilter(values["tag"], "tag")
	if err != nil {
		return query, err
	}
	query.Tags = tags
	excludes, err := parseTagFilter(values["exclude_tag"], "exclude_tag")
	if err != nil {
		return query, err
	}
	query.ExcludeTags = excludes
	if raw := strings.ToLower(strings.TrimSpace(values["tag_mode"])); raw != "" {
		if raw != TagMatchAll && raw != TagMatchAny {
			return query, domain.Invalid("tag_mode", "must be all or any")
		}
		if len(query.Tags) == 0 {
			return query, domain.Invalid("tag_mode", "tag_mode requires at least one tag")
		}
		query.TagMode = raw
	}
	for _, tag := range query.Tags {
		if containsString(query.ExcludeTags, tag) {
			return query, domain.Invalid("exclude_tag", "tag cannot be both included and excluded: "+tag)
		}
	}
	if raw := strings.TrimSpace(values["year"]); raw != "" {
		year, ok := parseYearValue(raw)
		if !ok {
			return query, domain.Invalid("year", "year must be a valid four digit number")
		}
		query.Year = year
	}
	if raw := strings.TrimSpace(values["year_from"]); raw != "" {
		year, ok := parseYearValue(raw)
		if !ok {
			return query, domain.Invalid("year_from", "year_from must be a valid four digit number")
		}
		query.YearFrom = year
	}
	if raw := strings.TrimSpace(values["year_to"]); raw != "" {
		year, ok := parseYearValue(raw)
		if !ok {
			return query, domain.Invalid("year_to", "year_to must be a valid four digit number")
		}
		query.YearTo = year
	}
	if query.Year > 0 && (query.YearFrom > 0 || query.YearTo > 0) {
		return query, domain.Invalid("year", "year cannot be combined with year_from or year_to")
	}
	if query.YearFrom > 0 && query.YearTo > 0 && query.YearFrom > query.YearTo {
		return query, domain.Invalid("year_to", "year_to must not be earlier than year_from")
	}
	if raw := strings.TrimSpace(values["offset"]); raw != "" {
		offset, err := strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return query, domain.Invalid("offset", "offset must be a non-negative integer")
		}
		query.Offset = offset
	}
	if raw := strings.TrimSpace(values["limit"]); raw != "" {
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

func parseTagFilter(raw string, field string) ([]string, error) {
	seen := map[string]bool{}
	result := []string{}
	for _, part := range strings.Split(raw, ",") {
		name := domain.NormalizeTagName(part)
		if name == "" || seen[name] {
			continue
		}
		if len([]rune(name)) > 48 {
			return nil, domain.Invalid(field, field+" entries must be at most 48 characters")
		}
		seen[name] = true
		result = append(result, name)
	}
	if len(result) > maxFilterTags {
		return nil, domain.Invalid(field, field+" accepts at most 24 tags")
	}
	sort.Strings(result)
	return result, nil
}

func parseYearValue(raw string) (int, bool) {
	year, err := strconv.Atoi(raw)
	if err != nil || year < 1000 || year > 2100 {
		return 0, false
	}
	return year, true
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
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

func (q Query) Describe() string {
	parts := []string{"view=" + q.View}
	if q.Keyword != "" {
		parts = append(parts, "q="+q.Keyword)
	}
	if len(q.Tags) > 0 {
		separator := "+"
		if q.TagMode == TagMatchAny {
			separator = "|"
		}
		parts = append(parts, "tag="+strings.Join(q.Tags, separator))
	}
	if len(q.ExcludeTags) > 0 {
		parts = append(parts, "exclude_tag="+strings.Join(q.ExcludeTags, "|"))
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
		if len(query.Tags) > 0 && !tagsMatch(value.Tags, query.Tags, query.TagMode) {
			continue
		}
		if len(query.ExcludeTags) > 0 && hasAnyTag(value.Tags, query.ExcludeTags) {
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

func tagsMatch(tags []domain.Tag, wanted []string, mode string) bool {
	if mode == TagMatchAny {
		return hasAnyTag(tags, wanted)
	}
	for _, name := range wanted {
		if !domain.HasTag(tags, name) {
			return false
		}
	}
	return true
}

func hasAnyTag(tags []domain.Tag, wanted []string) bool {
	for _, name := range wanted {
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
