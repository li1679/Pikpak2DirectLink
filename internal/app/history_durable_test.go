package app

import (
	"testing"
	"time"
)

func TestDurableHistoryRetainsMetadataAfterDetailExpiry(t *testing.T) {
	durable, _, now := newTestSQLJobStore(t)
	job := &Job{
		ID: "history-durable", Kind: ResourceMagnet, Mode: "direct", Input: "magnet:?xt=urn:btih:private",
		Status: JobFailed, Stage: StageFailed, FailureCode: "service_restart", Error: resolveJobRestartError, ChargedBytes: 456,
		CreatedAt: now.Add(-time.Minute), UpdatedAt: now,
	}
	if err := durable.create(job); err != nil {
		t.Fatal(err)
	}
	items, err := durable.listRecords(now.Add(time.Minute), true)
	if err != nil || len(items) != 1 {
		t.Fatalf("history before expiry=%+v err=%v", items, err)
	}
	if !items[0].DetailsAvailable || items[0].FailureCode != "service_restart" || items[0].ChargedBytes != 456 {
		t.Fatalf("history metadata=%+v", items[0])
	}
	expiredAt := now.Add(resolveJobDetailsTTL)
	items, err = durable.listRecords(expiredAt, true)
	if err != nil || len(items) != 1 {
		t.Fatalf("history after details expire=%+v err=%v", items, err)
	}
	if items[0].DetailsAvailable || items[0].Job != nil || items[0].FailureCode != "service_restart" || items[0].ChargedBytes != 456 {
		t.Fatalf("expired history metadata=%+v", items[0])
	}
	if _, ok, err := durable.get(job.ID, expiredAt); ok || err != nil {
		t.Fatalf("expired detail lookup: ok=%v err=%v", ok, err)
	}
	if record, ok, err := durable.getRecord(job.ID, expiredAt); !ok || err != nil || record.DetailsAvailable {
		t.Fatalf("retained metadata=%+v ok=%v err=%v", record, ok, err)
	}
	items, err = durable.listRecords(now.Add(resolveJobRecordTTL), true)
	if err != nil || len(items) != 0 {
		t.Fatalf("history after record expiry=%+v err=%v", items, err)
	}
}
