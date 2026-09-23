package workflowcheck

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"example.com/solo-0009-archive-weave/internal/catalog"
	"example.com/solo-0009-archive-weave/internal/domain"
	"example.com/solo-0009-archive-weave/internal/httpapi"
	"example.com/solo-0009-archive-weave/internal/observability"
	"example.com/solo-0009-archive-weave/internal/storage"
)

type harness struct {
	client *http.Client
	base   string
	dir    string
}

func Run(ctx context.Context, workflow string) error {
	check := strings.TrimSpace(workflow)
	switch check {
	case "intake-artifact":
		return runWithHarness(ctx, verifyIntake)
	case "update-metadata":
		return runWithHarness(ctx, checkUpdateMetadata)
	case "submit-review":
		return runWithHarness(ctx, checkSubmitReview)
	case "decide-review":
		return runWithHarness(ctx, checkDecideReview)
	case "search-export":
		return runWithHarness(ctx, checkSearchExport)
	case "import-batch":
		return runWithHarness(ctx, checkImportBatch)
	case "all":
		for _, name := range []string{
			"intake-artifact",
			"update-metadata",
			"submit-review",
			"decide-review",
			"search-export",
			"import-batch",
		} {
			if err := Run(ctx, name); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown workflow %q", workflow)
	}
}

func runWithHarness(ctx context.Context, check func(context.Context, *harness) error) error {
	directory, err := os.MkdirTemp("", "archive-weave-check-")
	if err != nil {
		return fmt.Errorf("create check directory: %w", err)
	}
	defer os.RemoveAll(directory)

	repository := storage.NewJSONStore(filepath.Join(directory, "artifacts.json"))
	auditRepository := storage.NewJSONAuditStore(filepath.Join(directory, "history.json"))
	service := catalog.NewService(repository, auditRepository)
	server := httptest.NewServer(httpapi.NewServer(service, observability.NewLogger(), &observability.Metrics{}, false).Handler())
	defer server.Close()

	h := &harness{
		client: &http.Client{Timeout: 10 * time.Second},
		base:   server.URL,
		dir:    directory,
	}
	return check(ctx, h)
}

func verifyIntake(ctx context.Context, h *harness) error {
	artifact, err := h.create(ctx, "Floodplain field notes", "Field notebook from the lower river basin", []string{"river-basin", "field-record"})
	if err != nil {
		return err
	}
	if artifact.Status != domain.StatusDraft || artifact.Version != 1 {
		return fmt.Errorf("unexpected created artifact: %#v", artifact)
	}
	fetched, err := h.getArtifact(ctx, artifact.ID)
	if err != nil {
		return err
	}
	if fetched.Title != artifact.Title {
		return fmt.Errorf("stored title mismatch: %q", fetched.Title)
	}
	timeline, err := h.history(ctx, artifact.ID)
	if err != nil {
		return err
	}
	if len(timeline.Events) != 1 || timeline.Events[0].Action != domain.ActionCreate {
		return fmt.Errorf("unexpected intake timeline: %#v", timeline)
	}
	return nil
}

func checkUpdateMetadata(ctx context.Context, h *harness) error {
	artifact, err := h.create(ctx, "Paper register fragment", "Register fragment with household notes and marginal remarks", []string{"register", "household"})
	if err != nil {
		return err
	}
	updated, err := h.update(ctx, artifact.ID, domain.CreateArtifact{
		Title:   "Revised paper register fragment",
		Summary: "Revised catalog record for a household register fragment",
		Source:  "Reading room box 18",
		Year:    1936,
		Tags:    []string{"register", "household", "marginalia"},
	})
	if err != nil {
		return err
	}
	if updated.Version != 2 || updated.Title != "Revised paper register fragment" {
		return fmt.Errorf("metadata update did not advance version: %#v", updated)
	}
	timeline, err := h.history(ctx, artifact.ID)
	if err != nil {
		return err
	}
	if len(timeline.Events) != 2 || timeline.Events[1].Action != domain.ActionUpdateMetadata {
		return fmt.Errorf("unexpected metadata timeline: %#v", timeline)
	}
	return nil
}

func checkSubmitReview(ctx context.Context, h *harness) error {
	artifact, err := h.create(ctx, "Railway timetable notes", "Working notes describing a regional railway timetable", []string{"railway", "timetable"})
	if err != nil {
		return err
	}
	submitted, err := h.submit(ctx, artifact.ID)
	if err != nil {
		return err
	}
	if submitted.Status != domain.StatusPendingReview || submitted.SubmittedAt == nil || submitted.Version != 2 {
		return fmt.Errorf("unexpected submitted artifact: %#v", submitted)
	}
	timeline, err := h.history(ctx, artifact.ID)
	if err != nil {
		return err
	}
	if len(timeline.Events) != 2 || timeline.Events[1].Action != domain.ActionSubmitReview {
		return fmt.Errorf("unexpected submission timeline: %#v", timeline)
	}
	return nil
}

func checkDecideReview(ctx context.Context, h *harness) error {
	artifact, err := h.create(ctx, "Botanical field card", "Field card describing plants observed near a wetland", []string{"botany", "wetland"})
	if err != nil {
		return err
	}
	submitted, err := h.submit(ctx, artifact.ID)
	if err != nil {
		return err
	}
	approved, err := h.review(ctx, submitted.ID, "approve", "curator-lin", "")
	if err != nil {
		return err
	}
	if approved.Status != domain.StatusApproved || approved.DecidedAt == nil || approved.Version != 3 {
		return fmt.Errorf("unexpected approved artifact: %#v", approved)
	}
	exported, err := h.exportArtifact(ctx, approved.ID)
	if err != nil {
		return err
	}
	if exported.ID != approved.ID || exported.Status != domain.StatusApproved {
		return fmt.Errorf("unexpected exported artifact: %#v", exported)
	}
	return nil
}

func checkSearchExport(ctx context.Context, h *harness) error {
	first, err := h.create(ctx, "Mining map annotation", "Annotation for a mining map from the north district", []string{"mining", "map"})
	if err != nil {
		return err
	}
	second, err := h.create(ctx, "Harbor shipping register", "Register entry describing vessel arrivals at the harbor", []string{"harbor", "register"})
	if err != nil {
		return err
	}
	third, err := h.createWithYear(ctx, "Mining town census", "Census tables for a mining town and its households", 1948, []string{"mining", "census"})
	if err != nil {
		return err
	}
	if _, err := h.create(ctx, "Orchard survey draft", "Draft survey notes for orchard plots and boundaries", []string{"orchard", "survey"}); err != nil {
		return err
	}
	if _, err := h.submit(ctx, first.ID); err != nil {
		return err
	}
	if _, err := h.review(ctx, first.ID, "approve", "curator-lin", ""); err != nil {
		return err
	}
	if _, err := h.submit(ctx, second.ID); err != nil {
		return err
	}
	if _, err := h.submit(ctx, third.ID); err != nil {
		return err
	}
	if _, err := h.review(ctx, third.ID, "approve", "curator-lin", ""); err != nil {
		return err
	}

	// Single tag filtering keeps working and produces a verifiable checksum.
	collection, err := h.exportCollection(ctx, "tag=map")
	if err != nil {
		return err
	}
	if collection.Count != 1 || len(collection.Artifacts) != 1 || !catalog.VerifyCollectionChecksum(collection) {
		return fmt.Errorf("unexpected collection export: %#v", collection)
	}

	// Combined tag, exclusion and year-range filters in the public view.
	for _, step := range []struct {
		query string
		want  int
	}{
		{"tag=mining,map", 1},            // all tags required
		{"tag=mining&tag=map", 1},        // repeated parameters combine like commas
		{"tag=map,census&tag_mode=any", 2}, // any tag matches
		{"tag=mining&exclude_tag=map", 1},
		{"year_from=1940&year_to=1950", 1},
		{"year=1932", 1},
		{"tag=orchard", 0}, // drafts stay hidden in the public view
		{"tag=harbor", 0},  // pending review stays hidden in the public view
		{"view=working&tag=orchard", 1},
		{"view=working&tag=harbor", 1},
		{"view=working&tag=mining&tag_mode=any", 2},
	} {
		if err := h.expectExportCount(ctx, step.query, step.want); err != nil {
			return err
		}
	}

	// Empty results are explicit: count zero, an empty array and a valid checksum.
	empty, err := h.exportCollection(ctx, "tag=mining,map,census")
	if err != nil {
		return err
	}
	if empty.Count != 0 || len(empty.Artifacts) != 0 || !catalog.VerifyCollectionChecksum(empty) {
		return fmt.Errorf("unexpected empty collection export: %#v", empty)
	}
	if !strings.Contains(empty.Query, "tag=census+map+mining") {
		return fmt.Errorf("empty export query description missing filters: %q", empty.Query)
	}
	body, err := h.getRaw(ctx, queryPath("/artifacts", "tag=mining,map,census"))
	if err != nil {
		return err
	}
	if !strings.Contains(body, `"count":0`) || !strings.Contains(body, `"artifacts":[]`) {
		return fmt.Errorf("empty list response is not explicit: %s", body)
	}

	// Invalid filter combinations are rejected on both list and export routes.
	for _, bad := range []string{
		"year=1932&year_from=1930",
		"year=1932&year_to=1940",
		"year_from=1950&year_to=1940",
		"tag=mining&exclude_tag=mining",
		"tag_mode=any",
		"tag=mining&tag_mode=sideways",
	} {
		if err := h.expectInvalidQuery(ctx, queryPath("/artifacts", bad)); err != nil {
			return err
		}
		if err := h.expectInvalidQuery(ctx, queryPath("/collections/export", bad)); err != nil {
			return err
		}
	}

	// List and export apply identical filters and describe them identically.
	for _, query := range []string{
		"tag=mining&tag_mode=any",
		"tag=mining&exclude_tag=map&year_from=1900&year_to=1950",
		"view=working&tag=harbor",
	} {
		if err := h.expectListExportConsistency(ctx, query); err != nil {
			return err
		}
	}

	// Every filter dimension shows up in the exported query description.
	described, err := h.exportCollection(ctx, "tag=mining,map&tag_mode=any&exclude_tag=census&year_from=1900&year_to=1950")
	if err != nil {
		return err
	}
	for _, part := range []string{"tag=map|mining", "exclude_tag=census", "year_from=1900", "year_to=1950"} {
		if !strings.Contains(described.Query, part) {
			return fmt.Errorf("export query description %q missing %q", described.Query, part)
		}
	}
	return nil
}

func (h *harness) expectExportCount(ctx context.Context, query string, want int) error {
	collection, err := h.exportCollection(ctx, query)
	if err != nil {
		return err
	}
	if collection.Count != want || len(collection.Artifacts) != want {
		return fmt.Errorf("export %q count = %d, want %d", query, collection.Count, want)
	}
	if !catalog.VerifyCollectionChecksum(collection) {
		return fmt.Errorf("export %q checksum invalid", query)
	}
	return nil
}

func (h *harness) expectListExportConsistency(ctx context.Context, query string) error {
	listed, err := h.listArtifacts(ctx, query)
	if err != nil {
		return err
	}
	exported, err := h.exportCollection(ctx, query)
	if err != nil {
		return err
	}
	if listed.Query != exported.Query {
		return fmt.Errorf("query description mismatch for %q: list %q vs export %q", query, listed.Query, exported.Query)
	}
	if listed.Count != exported.Count || len(listed.Artifacts) != len(exported.Artifacts) {
		return fmt.Errorf("count mismatch for %q: list %d vs export %d", query, listed.Count, exported.Count)
	}
	for index := range listed.Artifacts {
		if listed.Artifacts[index].ID != exported.Artifacts[index].ID {
			return fmt.Errorf("artifact mismatch for %q at %d: list %s vs export %s", query, index, listed.Artifacts[index].ID, exported.Artifacts[index].ID)
		}
	}
	return nil
}

func checkImportBatch(ctx context.Context, h *harness) error {
	items := []domain.CreateArtifact{
		{
			Title:   "Canal field notebook",
			Summary: "Notebook recording canal measurements and field observations",
			Source:  "Engineering transfer 4",
			Year:    1918,
			Tags:    []string{"canal", "field-record"},
		},
		{
			Title:   "Market notice poster",
			Summary: "Printed notice describing market opening times and rules",
			Source:  "Broadside collection",
			Year:    1927,
			Tags:    []string{"market", "poster"},
		},
		{
			Title:   "School inspection report",
			Summary: "Inspection observations for a rural school and its facilities",
			Source:  "Education office box 6",
			Year:    1941,
			Tags:    []string{"school", "inspection"},
		},
	}
	result, err := h.importBatch(ctx, items)
	if err != nil {
		return err
	}
	if result.Count != len(items) {
		return fmt.Errorf("batch count = %d, want %d", result.Count, len(items))
	}
	summary, err := h.summary(ctx)
	if err != nil {
		return err
	}
	if summary.Total != len(items) || summary.Working != len(items) {
		return fmt.Errorf("unexpected summary after batch: %#v", summary)
	}
	return nil
}

func (h *harness) create(ctx context.Context, title, summary string, tags []string) (domain.Artifact, error) {
	return h.createWithYear(ctx, title, summary, 1932, tags)
}

func (h *harness) createWithYear(ctx context.Context, title, summary string, year int, tags []string) (domain.Artifact, error) {
	input := domain.CreateArtifact{
		Title:   title,
		Summary: summary,
		Source:  "Reading room transfer",
		Year:    year,
		Tags:    tags,
	}
	var artifact domain.Artifact
	if err := h.request(ctx, http.MethodPost, "/artifacts", input, &artifact); err != nil {
		return domain.Artifact{}, err
	}
	return artifact, nil
}

func (h *harness) getArtifact(ctx context.Context, id string) (domain.Artifact, error) {
	var artifact domain.Artifact
	if err := h.request(ctx, http.MethodGet, "/artifacts/"+id, nil, &artifact); err != nil {
		return domain.Artifact{}, err
	}
	return artifact, nil
}

func (h *harness) update(ctx context.Context, id string, input domain.CreateArtifact) (domain.Artifact, error) {
	var artifact domain.Artifact
	if err := h.request(ctx, http.MethodPut, "/artifacts/"+id+"/metadata", input, &artifact); err != nil {
		return domain.Artifact{}, err
	}
	return artifact, nil
}

func (h *harness) submit(ctx context.Context, id string) (domain.Artifact, error) {
	var artifact domain.Artifact
	if err := h.request(ctx, http.MethodPost, "/artifacts/"+id+"/submit", map[string]any{}, &artifact); err != nil {
		return domain.Artifact{}, err
	}
	return artifact, nil
}

func (h *harness) review(ctx context.Context, id, decision, reviewer, note string) (domain.Artifact, error) {
	body := map[string]string{"decision": decision, "reviewer": reviewer, "note": note}
	var artifact domain.Artifact
	if err := h.request(ctx, http.MethodPost, "/artifacts/"+id+"/review", body, &artifact); err != nil {
		return domain.Artifact{}, err
	}
	return artifact, nil
}

func (h *harness) history(ctx context.Context, id string) (domain.Timeline, error) {
	var timeline domain.Timeline
	if err := h.request(ctx, http.MethodGet, "/artifacts/"+id+"/history", nil, &timeline); err != nil {
		return domain.Timeline{}, err
	}
	return timeline, nil
}

func (h *harness) exportArtifact(ctx context.Context, id string) (domain.Artifact, error) {
	var artifact domain.Artifact
	if err := h.request(ctx, http.MethodGet, "/artifacts/"+id+"/export", nil, &artifact); err != nil {
		return domain.Artifact{}, err
	}
	return artifact, nil
}

type listResult struct {
	Count     int               `json:"count"`
	Query     string            `json:"query"`
	Artifacts []domain.Artifact `json:"artifacts"`
}

func queryPath(base, query string) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return base + "?view=public"
	}
	if strings.Contains(query, "view=") {
		return base + "?" + query
	}
	return base + "?view=public&" + query
}

func (h *harness) listArtifacts(ctx context.Context, query string) (listResult, error) {
	var result listResult
	if err := h.request(ctx, http.MethodGet, queryPath("/artifacts", query), nil, &result); err != nil {
		return listResult{}, err
	}
	return result, nil
}

func (h *harness) exportCollection(ctx context.Context, query string) (catalog.Collection, error) {
	var collection catalog.Collection
	if err := h.request(ctx, http.MethodGet, queryPath("/collections/export", query), nil, &collection); err != nil {
		return catalog.Collection{}, err
	}
	return collection, nil
}

func (h *harness) getRaw(ctx context.Context, path string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, h.base+path, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	response, err := h.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("GET %s: %w", path, err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return "", fmt.Errorf("read GET %s: %w", path, err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("GET %s returned %d: %s", path, response.StatusCode, strings.TrimSpace(string(data)))
	}
	return string(data), nil
}

func (h *harness) expectInvalidQuery(ctx context.Context, path string) error {
	err := h.request(ctx, http.MethodGet, path, nil, nil)
	if err == nil {
		return fmt.Errorf("GET %s unexpectedly succeeded", path)
	}
	if !strings.Contains(err.Error(), "returned 400") || !strings.Contains(err.Error(), "invalid_input") {
		return fmt.Errorf("GET %s returned unexpected error: %w", path, err)
	}
	return nil
}

func (h *harness) importBatch(ctx context.Context, items []domain.CreateArtifact) (catalog.BatchResult, error) {
	var result catalog.BatchResult
	if err := h.request(ctx, http.MethodPost, "/artifacts/batch", map[string]any{"actor": "batch-check", "items": items}, &result); err != nil {
		return catalog.BatchResult{}, err
	}
	return result, nil
}

func (h *harness) summary(ctx context.Context) (domain.CatalogSummary, error) {
	var summary domain.CatalogSummary
	if err := h.request(ctx, http.MethodGet, "/catalog/summary", nil, &summary); err != nil {
		return domain.CatalogSummary{}, err
	}
	return summary, nil
}

func (h *harness) request(ctx context.Context, method, path string, body any, target any) error {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request body: %w", err)
		}
		payload = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, h.base+path, payload)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("X-Archive-Actor", "workflow-check")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := h.client.Do(request)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return fmt.Errorf("read %s %s: %w", method, path, err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("%s %s returned %d: %s", method, path, response.StatusCode, strings.TrimSpace(string(data)))
	}
	if target == nil {
		return nil
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("decode %s %s: %w", method, path, err)
	}
	return nil
}
