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
	draft, err := h.create(ctx, "River survey log", "Survey log recording river soundings and channel notes", []string{"river", "survey"})
	if err != nil {
		return err
	}
	if _, err := h.update(ctx, second.ID, domain.CreateArtifact{
		Title:   "Harbor shipping register",
		Summary: "Register entry describing vessel arrivals at the harbor",
		Source:  "Reading room transfer",
		Year:    1958,
		Tags:    []string{"harbor", "register"},
	}); err != nil {
		return err
	}
	for _, id := range []string{first.ID, second.ID} {
		if _, err := h.submit(ctx, id); err != nil {
			return err
		}
		if _, err := h.review(ctx, id, "approve", "curator-lin", ""); err != nil {
			return err
		}
	}

	// Tags combine with AND: both tags are required.
	match, err := h.exportCollection(ctx, "view=public&tag=mining&tag=map")
	if err != nil {
		return err
	}
	if match.Count != 1 || len(match.Artifacts) != 1 || match.Artifacts[0].ID != first.ID ||
		!catalog.VerifyCollectionChecksum(match) {
		return fmt.Errorf("unexpected AND tag collection: %#v", match)
	}
	if match.Query != "view=public,tag=map,tag=mining,sort=recent" {
		return fmt.Errorf("collection query description missing tag filters: %q", match.Query)
	}

	// An AND combination that no artifact satisfies is an empty 200 response.
	empty, err := h.exportCollection(ctx, "view=public&tag=mining&tag=harbor")
	if err != nil {
		return err
	}
	if empty.Count != 0 || len(empty.Artifacts) != 0 || !catalog.VerifyCollectionChecksum(empty) {
		return fmt.Errorf("unexpected empty collection: %#v", empty)
	}

	// Tag exclusion removes matching artifacts but keeps the other approved one.
	excluded, err := h.exportCollection(ctx, "view=public&exclude_tag=map")
	if err != nil {
		return err
	}
	if excluded.Count != 1 || excluded.Artifacts[0].ID != second.ID {
		return fmt.Errorf("unexpected exclusion collection: %#v", excluded)
	}

	// Year ranges are inclusive on both ends.
	ranged, err := h.exportCollection(ctx, "view=public&year_from=1940")
	if err != nil {
		return err
	}
	if ranged.Count != 1 || ranged.Artifacts[0].ID != second.ID {
		return fmt.Errorf("unexpected year_from collection: %#v", ranged)
	}
	ranged, err = h.exportCollection(ctx, "view=public&year_from=1900&year_to=1940")
	if err != nil {
		return err
	}
	if ranged.Count != 1 || ranged.Artifacts[0].ID != first.ID {
		return fmt.Errorf("unexpected year range collection: %#v", ranged)
	}
	exact, err := h.exportCollection(ctx, "view=public&year=1932")
	if err != nil {
		return err
	}
	if exact.Count != 1 || exact.Artifacts[0].ID != first.ID {
		return fmt.Errorf("exact year must stay supported and hide drafts: %#v", exact)
	}

	// The draft is invisible in the public view but visible in the working view.
	hidden, err := h.exportCollection(ctx, "view=public&tag=river&tag=survey")
	if err != nil {
		return err
	}
	if hidden.Count != 0 {
		return fmt.Errorf("public view leaked a draft artifact: %#v", hidden)
	}
	working, err := h.exportCollection(ctx, "view=working&tag=river&tag=survey")
	if err != nil {
		return err
	}
	if working.Count != 1 || working.Artifacts[0].ID != draft.ID {
		return fmt.Errorf("working view must include drafts: %#v", working)
	}

	// List and export must apply identical filters and describe them the same way.
	list, err := h.listArtifacts(ctx, "tag=map&tag=mining")
	if err != nil {
		return err
	}
	if list.Count != 1 || len(list.Artifacts) != 1 || list.Artifacts[0].ID != first.ID {
		return fmt.Errorf("unexpected AND tag list: %#v", list)
	}
	if list.Query != match.Query || list.Query != "view=public,tag=map,tag=mining,sort=recent" {
		return fmt.Errorf("list and export query descriptions differ: %q vs %q", list.Query, match.Query)
	}

	// Illegal combinations are rejected with 400 on both filtered endpoints.
	for _, raw := range []string{
		"year=1932&year_from=1900",
		"year_from=2000&year_to=1900",
		"tag=map&exclude_tag=map",
		"year=nineteen-thirty",
	} {
		for _, prefix := range []string{"/artifacts?", "/collections/export?"} {
			status, err := h.statusFor(ctx, prefix+raw)
			if err != nil {
				return err
			}
			if status != http.StatusBadRequest {
				return fmt.Errorf("GET %s%s status = %d, want 400", prefix, raw, status)
			}
		}
	}
	return nil
}

type artifactList struct {
	Count     int               `json:"count"`
	Query     string            `json:"query"`
	Artifacts []domain.Artifact `json:"artifacts"`
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
	input := domain.CreateArtifact{
		Title:   title,
		Summary: summary,
		Source:  "Reading room transfer",
		Year:    1932,
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

func (h *harness) exportCollection(ctx context.Context, query string) (catalog.Collection, error) {
	path := "/collections/export"
	if strings.TrimSpace(query) != "" {
		path += "?" + strings.TrimSpace(query)
	}
	var collection catalog.Collection
	if err := h.request(ctx, http.MethodGet, path, nil, &collection); err != nil {
		return catalog.Collection{}, err
	}
	return collection, nil
}

func (h *harness) listArtifacts(ctx context.Context, query string) (artifactList, error) {
	path := "/artifacts?" + strings.TrimSpace(query)
	var list artifactList
	if err := h.request(ctx, http.MethodGet, path, nil, &list); err != nil {
		return artifactList{}, err
	}
	return list, nil
}

func (h *harness) statusFor(ctx context.Context, path string) (int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, h.base+path, nil)
	if err != nil {
		return 0, fmt.Errorf("create request: %w", err)
	}
	response, err := h.client.Do(request)
	if err != nil {
		return 0, fmt.Errorf("GET %s: %w", path, err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 2<<20))
	return response.StatusCode, nil
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
