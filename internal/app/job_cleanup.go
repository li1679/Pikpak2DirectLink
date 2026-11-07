package app

import (
	"context"
	"errors"
	"time"
)

const jobCleanupInterval = time.Minute

func (s *Server) startJobCleanup() {
	ctx, cancel := context.WithCancel(context.Background())
	s.jobCleanupCancel = cancel
	s.jobCleanupDone = make(chan struct{})
	go func() {
		defer close(s.jobCleanupDone)
		s.cleanupJobs(s.now())
		ticker := time.NewTicker(jobCleanupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.cleanupJobs(s.now())
			}
		}
	}()
}

func (s *Server) cleanupJobs(now time.Time) {
	s.expireSelectionJobs(now)
	if s.durableJobs != nil {
		if _, err := s.durableJobs.scrubExpired(now); err != nil {
			s.logJob(LogWarn, "", "resolve job detail cleanup failed", err.Error())
		}
		if _, err := s.durableJobs.deleteExpired(now); err != nil {
			s.logJob(LogWarn, "", "resolve job record cleanup failed", err.Error())
		}
	}
	s.cleanupProxyTempResources(now)
}

func (s *Server) expireSelectionJobs(now time.Time) {
	if s.jobs == nil {
		return
	}
	cutoff := now.Add(-selectionRequiredTimeout)
	for _, jobID := range s.jobs.expiredSelectionIDs(cutoff) {
		s.expireSelectionJob(jobID, cutoff)
	}
}

var errSelectionNoLongerExpired = errors.New("job is no longer waiting on an expired selection")

func (s *Server) expireSelectionJob(jobID string, cutoff time.Time) {
	_, err := s.jobs.update(jobID, func(job *Job) error {
		if job.Status != JobSelectionRequired || job.UpdatedAt.After(cutoff) {
			return errSelectionNoLongerExpired
		}
		job.Status = JobFailed
		job.Stage = StageFailed
		job.Message = ""
		job.Error = "file selection timed out"
		job.FailureCode = "selection_timeout"
		return nil
	})
	if errors.Is(err, errSelectionNoLongerExpired) {
		return
	}
	if err != nil {
		s.logJob(LogError, jobID, "terminal job persistence failed", err.Error())
		s.requestRestart()
	}
}

func jobResults(job *Job) []JobResult {
	if job == nil {
		return nil
	}
	results := make([]JobResult, 0, len(job.Results)+1)
	if job.Result != nil {
		results = append(results, *job.Result)
	}
	return append(results, job.Results...)
}
