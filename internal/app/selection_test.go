package app

import (
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestApplyItemSelectionRejectsUnknownSourceItem(t *testing.T) {
	noopResolver := newResolveQueue(time.Second, time.Second, 1, func(string, error) {})
	s := &Server{jobs: newJobStore(10), resolver: noopResolver}
	s.jobs.create(&Job{
		ID:     "source-job",
		Status: JobSelectionRequired,
		Stage:  StageSourceSelection,
		Share:  &ShareState{ShareID: "share"},
		Items:  []DownloadItem{{ID: "known", Name: "known"}},
	})

	if _, status, msg := s.applyItemSelection("source-job", "unknown"); status != http.StatusBadRequest {
		t.Fatalf("status=%d msg=%q, want 400", status, msg)
	}
}

func TestApplyItemSelectionDuplicateSubmitOnlyQueuesOnce(t *testing.T) {
	noopResolver := newResolveQueue(time.Second, time.Second, 1, func(string, error) {})
	s := &Server{jobs: newJobStore(10), resolver: noopResolver}
	s.jobs.create(&Job{
		ID:     "source-job",
		Status: JobSelectionRequired,
		Stage:  StageSourceSelection,
		Share:  &ShareState{ShareID: "share"},
		Items:  []DownloadItem{{ID: "known", Name: "known", Path: "folder/known"}},
	})

	updated, status, msg := s.applyItemSelection("source-job", "known")
	if status != 0 {
		t.Fatalf("first selection status=%d msg=%q, want success", status, msg)
	}
	if updated.Share == nil || len(updated.Share.SelectedItems) != 1 || updated.Share.SelectedItems[0].Path != "folder/known" {
		t.Fatalf("selected source item = %+v, want path folder/known", updated.Share)
	}
	if _, status, msg := s.applyItemSelection("source-job", "known"); status != http.StatusConflict {
		t.Fatalf("second selection status=%d msg=%q, want 409", status, msg)
	}
	if queued := noopResolver.queuedIDs(); len(queued) != 1 || queued[0] != "source-job" {
		t.Fatalf("queued IDs = %v, want [source-job]", queued)
	}
}

func TestApplyItemsSelectionAcceptsMultipleSourceItems(t *testing.T) {
	noopResolver := newResolveQueue(time.Second, time.Second, 1, func(string, error) {})
	s := &Server{jobs: newJobStore(10), resolver: noopResolver}
	s.jobs.create(&Job{
		ID:     "source-job",
		Status: JobSelectionRequired,
		Stage:  StageSourceSelection,
		Share:  &ShareState{ShareID: "share"},
		Items: []DownloadItem{
			{ID: "a", Name: "a.bin", Path: "root/a.bin", Size: itoa64(256 * 1024 * 1024)},
			{ID: "b", Name: "b.bin", Path: "root/b.bin", Size: itoa64(256 * 1024 * 1024)},
			{ID: "c", Name: "c.bin", Path: "root/c.bin", Size: itoa64(256 * 1024 * 1024)},
		},
	})

	updated, status, msg := s.applyItemsSelection("source-job", []string{"b", "a", "b"})
	if status != 0 {
		t.Fatalf("source multi selection status=%d msg=%q, want success", status, msg)
	}
	if updated.Status != JobQueued || updated.Stage != StageTransfer {
		t.Fatalf("updated job status/stage = %s/%s, want queued/transfer", updated.Status, updated.Stage)
	}
	if updated.Share == nil || len(updated.Share.SelectedIDs) != 2 || updated.Share.SelectedIDs[0] != "b" || updated.Share.SelectedIDs[1] != "a" {
		t.Fatalf("selected share ids = %+v, want [b a]", updated.Share)
	}
	if len(updated.Share.SelectedItems) != 2 || updated.Share.SelectedItems[0].Path != "root/b.bin" || updated.Share.SelectedItems[1].Path != "root/a.bin" {
		t.Fatalf("selected source items = %+v", updated.Share.SelectedItems)
	}
	if !updated.ResolveSelected || len(updated.Items) != 0 {
		t.Fatalf("selection state = resolve:%v items:%+v", updated.ResolveSelected, updated.Items)
	}
	if queued := noopResolver.queuedIDs(); len(queued) != 1 || queued[0] != "source-job" {
		t.Fatalf("queued IDs = %v, want [source-job]", queued)
	}
}

func TestApplyItemsSelectionRejectsTooManyItems(t *testing.T) {
	ids := make([]string, maxSelectedFilesPerResolve+1)
	for i := range ids {
		ids[i] = "file-" + strconv.Itoa(i)
	}
	s := &Server{jobs: newJobStore(10)}
	if _, status, msg := s.applyItemsSelection("job-any", ids); status != http.StatusBadRequest {
		t.Fatalf("status=%d msg=%q, want 400", status, msg)
	}
}

func TestResultForToken(t *testing.T) {
	job := &Job{
		Result:  &JobResult{ProxyToken: "single-tok", File: DownloadItem{ID: "s"}},
		Results: []JobResult{{ProxyToken: "tok-a", File: DownloadItem{ID: "a"}}, {ProxyToken: "tok-b", File: DownloadItem{ID: "b"}}},
	}
	if r := job.resultForToken("tok-b"); r == nil || r.File.ID != "b" {
		t.Fatalf("expected result b, got %+v", r)
	}
	if r := job.resultForToken("single-tok"); r == nil || r.File.ID != "s" {
		t.Fatalf("expected single result, got %+v", r)
	}
	if r := job.resultForToken("nope"); r != nil {
		t.Fatalf("expected nil for unknown token, got %+v", r)
	}
	if r := job.resultForToken(""); r != nil {
		t.Fatalf("expected nil for empty token, got %+v", r)
	}
}

func itoa64(n int64) string {
	return strconv.FormatInt(n, 10)
}

func TestSelectionReportsQueueRejection(t *testing.T) {
	for _, stage := range []JobStage{StageSourceSelection, StageResultSelection} {
		for _, multiple := range []bool{false, true} {
			t.Run(string(stage)+strconv.FormatBool(multiple), func(t *testing.T) {
				q := newResolveQueue(time.Second, time.Second, 1, nil)
				q.shutdown()
				s := &Server{jobs: newJobStore(10), resolver: q, logs: newLogStore(10)}
				if err := s.jobs.create(&Job{ID: "selected", Status: JobSelectionRequired, Stage: stage,
					Share: &ShareState{ShareID: "share"}, Items: []DownloadItem{{ID: "file", Name: "file.bin"}}}); err != nil {
					t.Fatal(err)
				}
				var status int
				if multiple {
					_, status, _ = s.applyItemsSelection("selected", []string{"file"})
				} else {
					_, status, _ = s.applyItemSelection("selected", "file")
				}
				if status != http.StatusServiceUnavailable {
					t.Fatalf("status=%d", status)
				}
				job, _ := s.jobs.get("selected")
				if job.Status != JobFailed {
					t.Fatalf("job remained %s after queue rejection", job.Status)
				}
			})
		}
	}
}
