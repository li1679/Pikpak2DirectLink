package app

import (
	"fmt"
	"sync"
	"time"
)

const (
	maxGlobalNonterminalJobs = 500
	selectionRequiredTimeout = 30 * time.Minute
)

type jobAdmissionError struct{ Limit int }

func (e *jobAdmissionError) Error() string {
	return fmt.Sprintf("active job limit reached (%d)", e.Limit)
}

type jobAdmission struct {
	mu          sync.Mutex
	jobs        *jobStore
	globalLimit int
}

func newJobAdmission(jobs *jobStore) *jobAdmission {
	return &jobAdmission{jobs: jobs, globalLimit: maxGlobalNonterminalJobs}
}

// withCapacity makes capacity inspection and job creation atomic across requests.
func (a *jobAdmission) withCapacity(units int, create func() error) error {
	if a == nil || a.jobs == nil {
		return create()
	}
	if units < 1 {
		units = 1
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.globalLimit > 0 && a.jobs.activeCount()+units > a.globalLimit {
		return &jobAdmissionError{Limit: a.globalLimit}
	}
	return create()
}
