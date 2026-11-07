package app

import (
	"testing"
	"time"
)

func TestPersonalHistoryListsTerminalRootJobs(t *testing.T) {
	store, _, now := newTestSQLJobStore(t)
	for _, job := range []*Job{
		{ID: "successful", Kind: ResourceMagnet, Mode: "direct", Status: JobCompleted, Result: &JobResult{File: DownloadItem{ID: "file", Size: "1024"}}, CreatedAt: now, UpdatedAt: now},
		{ID: "failed", Kind: ResourceMagnet, Mode: "direct", Status: JobFailed, FailureCode: "source_failed", CreatedAt: now.Add(time.Minute), UpdatedAt: now.Add(time.Minute)},
		{ID: "queued", Kind: ResourceMagnet, Mode: "direct", Status: JobQueued, CreatedAt: now, UpdatedAt: now},
		{ID: "child", ParentID: "successful", Kind: ResourceMagnet, Mode: "direct", Status: JobCompleted, CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.create(job); err != nil {
			t.Fatal(err)
		}
	}
	records, err := store.listRecords(now.Add(2*time.Minute), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[0].ID != "failed" || records[1].ID != "successful" || records[1].ResultCount != 1 {
		t.Fatalf("personal history=%+v", records)
	}
	all, err := store.listRecords(now.Add(2*time.Minute), false)
	if err != nil || len(all) != 4 {
		t.Fatalf("all durable jobs=%+v err=%v", all, err)
	}
}

func TestPersonalHistoryPersistsPartialBatchDetailsEncrypted(t *testing.T) {
	store, db, now := newTestSQLJobStore(t)
	job := &Job{
		ID: "partial-batch", Kind: ResourceBatch, Mode: "direct", Status: JobCompleted,
		OriginalInput: "private batch input", Results: []JobResult{{File: DownloadItem{ID: "file-a"}, DirectURL: "https://cdn.example/private-token"}},
		Batch:     &BatchProgress{Total: 2, Succeeded: 1, Failed: 1, Failures: []BatchFailure{{Label: "link 2", Error: "private source error"}}},
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.create(job); err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.get(job.ID, now)
	if err != nil || !ok || got.Batch == nil || got.Batch.Total != 2 || got.Batch.Succeeded != 1 || got.Batch.Failed != 1 || len(got.Results) != 1 || len(got.Batch.Failures) != 1 {
		t.Fatalf("partial batch=%+v ok=%v err=%v", got, ok, err)
	}
	assertEncryptedColumn(t, db, `SELECT payload_encrypted FROM resolve_jobs WHERE id=?`, job.ID, "private source error")
}
