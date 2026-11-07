package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

type selectionUpdateError struct {
	status  int
	message string
}

func (e selectionUpdateError) Error() string {
	return e.message
}

func selectionHTTPError(status int, message string) error {
	return selectionUpdateError{status: status, message: message}
}

func selectionErrorResponse(err error) (int, string) {
	var selectionErr selectionUpdateError
	if errors.As(err, &selectionErr) {
		return selectionErr.status, selectionErr.message
	}
	return http.StatusConflict, err.Error()
}

// applyItemSelection resolves a pending selection on a job and kicks the next
// stage. It returns the updated job, or an HTTP status code + message on error
// (status 0 means success).
func (s *Server) applyItemSelection(jobID, itemID string) (*Job, int, string) {
	itemID = strings.TrimSpace(itemID)
	if itemID == "" {
		return nil, http.StatusBadRequest, "item_id is required"
	}

	job, ok := s.jobs.get(jobID)
	if !ok {
		return nil, http.StatusNotFound, "job not found"
	}
	if job.Status != JobSelectionRequired {
		return nil, http.StatusConflict, "job is not waiting for a selection"
	}

	var selectedItem *DownloadItem
	if job.Stage == StageResultSelection {
		for _, item := range job.Items {
			if item.ID == itemID {
				copyItem := item
				selectedItem = &copyItem
				break
			}
		}
		if selectedItem == nil {
			return nil, http.StatusBadRequest, "selected file was not found in the current result set"
		}
	}

	_, err := s.jobs.update(jobID, func(current *Job) error {
		if current.Status != JobSelectionRequired {
			return selectionHTTPError(http.StatusConflict, "job is not waiting for a selection")
		}
		current.Status = JobQueued
		current.Message = "queued"
		current.Error = ""
		switch current.Stage {
		case StageSourceSelection:
			if current.Share == nil {
				return selectionHTTPError(http.StatusConflict, "share context is missing")
			}
			var selected DownloadItem
			found := false
			for _, item := range current.Items {
				if item.ID == itemID {
					selected = item
					found = true
					break
				}
			}
			if !found {
				return selectionHTTPError(http.StatusBadRequest, "selected item was not found in the current share")
			}
			current.Items = nil
			current.Share.SelectedID = itemID
			current.Share.SelectedIDs = []string{itemID}
			current.Share.SelectedItems = []DownloadItem{selected}
			current.Stage = StageTransfer
			current.ResolveSelected = true
		case StageResultSelection:
			current.Message = "queued"
		default:
			return selectionHTTPError(http.StatusConflict, "job cannot accept selections right now")
		}
		return nil
	})
	if err != nil {
		status, msg := selectionErrorResponse(err)
		return nil, status, msg
	}

	// Continue selected work before new submissions.
	if job.Stage == StageSourceSelection {
		return s.queueSelectedJob(jobID, func(ctx context.Context) { s.processJob(ctx, jobID) })
	}
	item := *selectedItem
	return s.queueSelectedJob(jobID, func(ctx context.Context) { s.resolveExistingFile(ctx, jobID, item) })
}

// applyItemsSelection resolves each selected file into its own download link.
func (s *Server) applyItemsSelection(jobID string, itemIDs []string) (*Job, int, string) {
	seen := make(map[string]bool, len(itemIDs))
	ordered := make([]string, 0, len(itemIDs))
	for _, id := range itemIDs {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ordered = append(ordered, id)
	}
	if len(ordered) == 0 {
		return nil, http.StatusBadRequest, "请至少选择一个文件"
	}
	if len(ordered) > maxSelectedFilesPerResolve {
		return nil, http.StatusBadRequest, fmt.Sprintf("单次最多解析 %d 个文件，请减少选择后重试", maxSelectedFilesPerResolve)
	}

	job, ok := s.jobs.get(jobID)
	if !ok {
		return nil, http.StatusNotFound, "job not found"
	}
	if job.Status != JobSelectionRequired {
		return nil, http.StatusConflict, "job is not waiting for a selection"
	}

	if job.Stage == StageSourceSelection {

		_, err := s.jobs.update(jobID, func(current *Job) error {
			if current.Status != JobSelectionRequired {
				return selectionHTTPError(http.StatusConflict, "job is not waiting for a selection")
			}
			if current.Stage != StageSourceSelection {
				return selectionHTTPError(http.StatusConflict, "job cannot accept selections right now")
			}
			if current.Share == nil {
				return selectionHTTPError(http.StatusConflict, "share context is missing")
			}
			byID := make(map[string]DownloadItem, len(current.Items))
			for _, item := range current.Items {
				byID[item.ID] = item
			}
			selected := make([]DownloadItem, 0, len(ordered))
			for _, id := range ordered {
				item, ok := byID[id]
				if !ok {
					return selectionHTTPError(http.StatusBadRequest, "selected item was not found in the current share")
				}
				selected = append(selected, item)
			}
			current.Status = JobQueued
			current.Message = "queued"
			current.Error = ""
			current.Items = nil
			current.Share.SelectedID = ordered[0]
			current.Share.SelectedIDs = append([]string(nil), ordered...)
			current.Share.SelectedItems = selected
			current.Stage = StageTransfer
			current.ResolveSelected = true
			return nil
		})
		if err != nil {
			status, msg := selectionErrorResponse(err)
			return nil, status, msg
		}

		return s.queueSelectedJob(jobID, func(ctx context.Context) { s.processJob(ctx, jobID) })
	}
	if job.Stage != StageResultSelection {
		return nil, http.StatusConflict, "job cannot accept selections right now"
	}

	byID := make(map[string]DownloadItem, len(job.Items))
	for _, item := range job.Items {
		byID[item.ID] = item
	}
	selected := make([]DownloadItem, 0, len(ordered))
	for _, id := range ordered {
		item, ok := byID[id]
		if !ok {
			return nil, http.StatusBadRequest, "selected file was not found in the current result set"
		}
		selected = append(selected, item)
	}

	_, err := s.jobs.update(jobID, func(current *Job) error {
		if current.Status != JobSelectionRequired {
			return selectionHTTPError(http.StatusConflict, "job is not waiting for a selection")
		}
		if current.Stage != StageResultSelection {
			return selectionHTTPError(http.StatusConflict, "job cannot accept selections right now")
		}
		byID := make(map[string]struct{}, len(current.Items))
		for _, item := range current.Items {
			byID[item.ID] = struct{}{}
		}
		for _, id := range ordered {
			if _, ok := byID[id]; !ok {
				return selectionHTTPError(http.StatusBadRequest, "selected file was not found in the current result set")
			}
		}
		current.Status = JobQueued
		current.Message = "queued"
		current.Error = ""
		return nil
	})
	if err != nil {
		status, msg := selectionErrorResponse(err)
		return nil, status, msg
	}

	return s.queueSelectedJob(jobID, func(ctx context.Context) { s.resolveExistingFiles(ctx, jobID, selected) })
}

func (s *Server) queueSelectedJob(jobID string, run func(context.Context)) (*Job, int, string) {
	if err := s.resolver.enqueue(jobID, priorityResume, run); err != nil {
		s.failJob(jobID, err)
		return nil, http.StatusServiceUnavailable, "解析队列暂不可用，请重新解析"
	}
	updated, _ := s.jobs.get(jobID)
	return updated, 0, ""
}
