package app

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestJobAdmissionEnforcesGlobalLimitBeforeCreate(t *testing.T) {
	store := newJobStore(maxGlobalNonterminalJobs + 10)
	admission := newJobAdmission(store)
	now := time.Now().UTC()
	for i := 0; i < maxGlobalNonterminalJobs; i++ {
		if err := store.create(&Job{ID: fmt.Sprintf("job-%d", i), Status: JobQueued, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	called := false
	err := admission.withCapacity(1, func() error { called = true; return nil })
	var limitErr *jobAdmissionError
	if !errors.As(err, &limitErr) || limitErr.Limit != maxGlobalNonterminalJobs {
		t.Fatalf("admission error = %v", err)
	}
	if called {
		t.Fatal("create callback ran after admission rejection")
	}
}

func TestJobAdmissionSerializesConcurrentCreates(t *testing.T) {
	store := newJobStore(10)
	admission := &jobAdmission{jobs: store, globalLimit: 1}
	now := time.Now().UTC()
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs <- admission.withCapacity(1, func() error {
				return store.create(&Job{ID: fmt.Sprintf("job-%d", i), Status: JobQueued, CreatedAt: now, UpdatedAt: now})
			})
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	var accepted, rejected int
	for err := range errs {
		if err == nil {
			accepted++
		} else {
			var limitErr *jobAdmissionError
			if !errors.As(err, &limitErr) {
				t.Fatalf("unexpected error: %v", err)
			}
			rejected++
		}
	}
	if accepted != 1 || rejected != 1 {
		t.Fatalf("accepted/rejected = %d/%d", accepted, rejected)
	}
}

func TestSelectionExpiryRemainsIndependentOfRemovedUserQuotas(t *testing.T) {
	store := newJobStore(20)
	now := time.Now().UTC()
	for i := 0; i < 2; i++ {
		if err := store.create(&Job{ID: fmt.Sprintf("selection-%d", i), Status: JobSelectionRequired, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	if got := store.expiredSelectionIDs(now.Add(-selectionRequiredTimeout)); len(got) != 2 {
		t.Fatalf("expired IDs = %v", got)
	}
	if got := store.expiredSelectionIDs(now.Add(-2 * time.Hour)); len(got) != 0 {
		t.Fatalf("unexpected expired IDs = %v", got)
	}
}
