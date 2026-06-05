package s3dav

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/spf13/afero"
)

const (
	SyncJobRunning   = "running"
	SyncJobPaused    = "paused"
	SyncJobCompleted = "completed"
	SyncJobFailed    = "failed"
	SyncJobCanceled  = "canceled"

	syncJobRetention = time.Hour
)

type syncJob struct {
	mu     sync.RWMutex
	resp   SyncJobResponse
	ctx    context.Context
	cancel context.CancelFunc

	stateMu sync.Mutex
	paused  bool
	cond    *sync.Cond
}

type syncFileTask struct {
	SourcePath      string
	DestinationPath string
	Size            int64
}

type syncPlan struct {
	Files           []syncFileTask
	Directories     []string
	SourceDirectory bool
}

type syncProgressUpdate struct {
	Response               SyncResponse
	Total                  int
	Processed              int
	CurrentMountKey        string
	CurrentSourcePath      string
	CurrentDestinationPath string
	CurrentSize            int64
	CurrentBytes           int64
}

type syncProgressFunc func(syncProgressUpdate)

type progressReader struct {
	reader     io.Reader
	beforeRead func() error
	onRead     func(int64)
}

func (r progressReader) Read(p []byte) (int, error) {
	if r.beforeRead != nil {
		if err := r.beforeRead(); err != nil {
			return 0, err
		}
	}
	n, err := r.reader.Read(p)
	if n > 0 && r.onRead != nil {
		r.onRead(int64(n))
	}
	return n, err
}

func (s *Service) StartSync(ctx context.Context, req SyncRequest) (SyncJobResponse, error) {
	normalized, err := s.normalizeSyncRequest(ctx, req)
	if err != nil {
		return SyncJobResponse{}, err
	}
	jobCtx, cancel := context.WithCancel(context.Background())
	now := time.Now()
	job := newSyncJob(SyncJobResponse{
		JobID:           newSyncJobID(),
		Status:          SyncJobRunning,
		SourceMountKey:  normalized.SourceMountKey,
		TargetMountKeys: append([]string(nil), normalized.TargetMountKeys...),
		SourcePath:      normalized.SourcePath,
		DestinationPath: normalized.DestinationPath,
		StartedAt:       now,
		UpdatedAt:       now,
	}, jobCtx, cancel)
	s.syncJobsMu.Lock()
	s.pruneSyncJobsLocked(now)
	s.syncJobs[job.resp.JobID] = job
	s.syncJobsMu.Unlock()

	go func() {
		resp, err := s.runSync(job.ctx, normalized, job.update, job.waitIfPaused)
		if err != nil {
			if errors.Is(err, context.Canceled) && job.isCanceled() {
				job.finishCanceled(resp)
				return
			}
			job.fail(err)
			return
		}
		job.complete(resp)
	}()

	return job.snapshot(), nil
}

func newSyncJob(resp SyncJobResponse, ctx context.Context, cancel context.CancelFunc) *syncJob {
	job := &syncJob{resp: resp, ctx: ctx, cancel: cancel}
	job.cond = sync.NewCond(&job.stateMu)
	return job
}

func (s *Service) SyncJobs() SyncJobsResponse {
	now := time.Now()
	s.syncJobsMu.Lock()
	s.pruneSyncJobsLocked(now)
	jobs := make([]SyncJobResponse, 0, len(s.syncJobs))
	for _, job := range s.syncJobs {
		jobs = append(jobs, job.snapshot())
	}
	s.syncJobsMu.Unlock()
	sort.SliceStable(jobs, func(i, j int) bool {
		return jobs[i].StartedAt.After(jobs[j].StartedAt)
	})
	return SyncJobsResponse{Jobs: jobs}
}

func (s *Service) SyncJob(id string) (SyncJobResponse, error) {
	id = strings.TrimSpace(id)
	s.syncJobsMu.RLock()
	job, ok := s.syncJobs[id]
	s.syncJobsMu.RUnlock()
	if !ok {
		return SyncJobResponse{}, fmt.Errorf("sync job %q was not found", id)
	}
	return job.snapshot(), nil
}

func (s *Service) ControlSyncJob(id, action string) (SyncJobResponse, error) {
	id = strings.TrimSpace(id)
	action = strings.TrimSpace(strings.ToLower(action))
	s.syncJobsMu.RLock()
	job, ok := s.syncJobs[id]
	s.syncJobsMu.RUnlock()
	if !ok {
		return SyncJobResponse{}, fmt.Errorf("sync job %q was not found", id)
	}
	switch action {
	case "pause":
		return job.pause()
	case "resume":
		return job.resume()
	case "cancel":
		return job.cancelJob()
	default:
		return SyncJobResponse{}, fmt.Errorf("unsupported sync job action %q", action)
	}
}

func (s *Service) Sync(ctx context.Context, req SyncRequest) (SyncResponse, error) {
	normalized, err := s.normalizeSyncRequest(ctx, req)
	if err != nil {
		return SyncResponse{}, err
	}
	return s.runSync(ctx, normalized, nil, nil)
}

func (s *Service) normalizeSyncRequest(ctx context.Context, req SyncRequest) (SyncRequest, error) {
	sourceMount, err := s.requireMount(req.SourceMountKey)
	if err != nil {
		return SyncRequest{}, err
	}
	if _, err := s.filesystemForMount(ctx, sourceMount, true); err != nil {
		return SyncRequest{}, err
	}
	sourcePath, err := CleanPath(req.SourcePath, false)
	if err != nil {
		return SyncRequest{}, err
	}
	destinationPath := strings.TrimSpace(req.DestinationPath)
	if destinationPath == "" {
		destinationPath = sourcePath
	}
	destinationPath, err = CleanPath(destinationPath, false)
	if err != nil {
		return SyncRequest{}, err
	}
	seen := make(map[string]struct{})
	targetKeys := make([]string, 0, len(req.TargetMountKeys))
	for _, rawKey := range req.TargetMountKeys {
		targetMount, err := s.requireMount(rawKey)
		if err != nil {
			return SyncRequest{}, err
		}
		if targetMount.Key == sourceMount.Key {
			return SyncRequest{}, fmt.Errorf("cannot sync to itself")
		}
		if _, ok := seen[targetMount.Key]; ok {
			continue
		}
		if _, err := s.filesystemForMount(ctx, targetMount, true); err != nil {
			return SyncRequest{}, err
		}
		seen[targetMount.Key] = struct{}{}
		targetKeys = append(targetKeys, targetMount.Key)
	}
	if len(targetKeys) == 0 {
		return SyncRequest{}, fmt.Errorf("at least one target mount is required")
	}
	return SyncRequest{
		SourceMountKey:  sourceMount.Key,
		TargetMountKeys: targetKeys,
		SourcePath:      sourcePath,
		DestinationPath: destinationPath,
		Overwrite:       req.Overwrite,
	}, nil
}

func (s *Service) runSync(ctx context.Context, req SyncRequest, progress syncProgressFunc, waitIfPaused func(context.Context) error) (SyncResponse, error) {
	if err := waitForSyncTurn(ctx, waitIfPaused); err != nil {
		return SyncResponse{}, err
	}
	sourceFS, err := s.Filesystem(ctx, req.SourceMountKey)
	if err != nil {
		return SyncResponse{}, err
	}
	plan, err := collectSyncPlan(sourceFS, req.SourcePath, req.DestinationPath)
	if err != nil {
		return SyncResponse{}, err
	}
	if !plan.SourceDirectory && req.DestinationPath == "/" {
		return SyncResponse{}, fmt.Errorf("destination file path is required")
	}
	resp := SyncResponse{
		SourceMountKey:  req.SourceMountKey,
		SourcePath:      req.SourcePath,
		DestinationPath: req.DestinationPath,
		Results:         make([]SyncTargetResult, 0, len(req.TargetMountKeys)),
		BytesTotal:      syncTotalBytes(plan.Files, len(req.TargetMountKeys)),
	}
	total := len(plan.Files) * len(req.TargetMountKeys)
	processed := 0
	report := func(currentMountKey, currentSourcePath, currentDestinationPath string, currentSize, currentBytes int64) {
		if progress == nil {
			return
		}
		progress(syncProgressUpdate{
			Response:               resp,
			Total:                  total,
			Processed:              processed,
			CurrentMountKey:        currentMountKey,
			CurrentSourcePath:      currentSourcePath,
			CurrentDestinationPath: currentDestinationPath,
			CurrentSize:            currentSize,
			CurrentBytes:           currentBytes,
		})
	}
	report("", "", "", 0, 0)

	for _, targetKey := range req.TargetMountKeys {
		if err := waitForSyncTurn(ctx, waitIfPaused); err != nil {
			return resp, err
		}
		targetFS, err := s.Filesystem(ctx, targetKey)
		result := SyncTargetResult{MountKey: targetKey}
		if err != nil {
			result.Failed += len(plan.Files)
			result.Errors = append(result.Errors, err.Error())
			resp.Failed += len(plan.Files)
			processed += len(plan.Files)
			resp.Results = append(resp.Results, result)
			report(targetKey, "", "", 0, 0)
			continue
		}
		if plan.SourceDirectory {
			if err := targetFS.MkdirAll(req.DestinationPath, 0755); err != nil {
				result.Errors = append(result.Errors, err.Error())
			}
			for _, dir := range plan.Directories {
				if err := targetFS.MkdirAll(dir, 0755); err != nil {
					result.Errors = append(result.Errors, err.Error())
				}
			}
		}
		for _, task := range plan.Files {
			if err := waitForSyncTurn(ctx, waitIfPaused); err != nil {
				return resp, err
			}
			report(targetKey, task.SourcePath, task.DestinationPath, task.Size, 0)
			currentBytes := int64(0)
			copiedBytes, status, err := copySyncTask(ctx, sourceFS, targetFS, task, req.Overwrite, func(n int64) {
				currentBytes += n
				resp.BytesCopied += n
				result.BytesCopied += n
				report(targetKey, task.SourcePath, task.DestinationPath, task.Size, currentBytes)
			}, waitIfPaused)
			if copiedBytes > 0 && currentBytes == 0 {
				resp.BytesCopied += copiedBytes
				result.BytesCopied += copiedBytes
			}
			switch status {
			case "copied":
				resp.Copied++
				result.Copied++
			case "skipped":
				resp.Skipped++
				result.Skipped++
			case "failed":
				resp.Failed++
				result.Failed++
				if err != nil {
					result.Errors = append(result.Errors, task.SourcePath+": "+err.Error())
				}
			}
			processed++
			report(targetKey, task.SourcePath, task.DestinationPath, task.Size, currentBytes)
			if err != nil && errors.Is(err, context.Canceled) {
				resp.Results = append(resp.Results, result)
				return resp, err
			}
		}
		resp.Results = append(resp.Results, result)
	}
	report("", "", "", 0, 0)
	return resp, nil
}

func collectSyncPlan(fs afero.Fs, sourcePath, destinationPath string) (syncPlan, error) {
	info, err := fs.Stat(sourcePath)
	if err != nil {
		return syncPlan{}, err
	}
	if !info.IsDir() {
		return syncPlan{
			Files: []syncFileTask{{
				SourcePath:      sourcePath,
				DestinationPath: destinationPath,
				Size:            info.Size(),
			}},
		}, nil
	}
	plan := syncPlan{SourceDirectory: true}
	if err := collectSyncDirectory(fs, sourcePath, sourcePath, destinationPath, &plan); err != nil {
		return syncPlan{}, err
	}
	return plan, nil
}

func collectSyncDirectory(fs afero.Fs, rootPath, dirPath, destinationRoot string, plan *syncPlan) error {
	files, err := listFiles(fs, dirPath)
	if err != nil {
		return err
	}
	for _, entry := range files.Entries {
		cleanEntryPath, err := CleanPath(entry.Path, false)
		if err != nil {
			return err
		}
		if entry.IsDir {
			rel := syncRelativePath(rootPath, cleanEntryPath)
			if rel != "" {
				plan.Directories = append(plan.Directories, JoinPath(destinationRoot, rel))
			}
			if err := collectSyncDirectory(fs, rootPath, cleanEntryPath, destinationRoot, plan); err != nil {
				return err
			}
			continue
		}
		rel := syncRelativePath(rootPath, cleanEntryPath)
		if rel == "" {
			rel = path.Base(cleanEntryPath)
		}
		plan.Files = append(plan.Files, syncFileTask{
			SourcePath:      cleanEntryPath,
			DestinationPath: JoinPath(destinationRoot, rel),
			Size:            entry.Size,
		})
	}
	return nil
}

func syncRelativePath(rootPath, filePath string) string {
	root := strings.Trim(cleanedPathForRelative(rootPath), "/")
	file := strings.Trim(cleanedPathForRelative(filePath), "/")
	if root == "" {
		return file
	}
	return strings.TrimPrefix(file, root+"/")
}

func cleanedPathForRelative(value string) string {
	cleaned, err := CleanPath(value, false)
	if err != nil {
		return strings.TrimSpace(value)
	}
	return cleaned
}

func syncTotalBytes(tasks []syncFileTask, targetCount int) int64 {
	var total int64
	for _, task := range tasks {
		total += task.Size
	}
	return total * int64(targetCount)
}

func waitForSyncTurn(ctx context.Context, waitIfPaused func(context.Context) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if waitIfPaused != nil {
		return waitIfPaused(ctx)
	}
	return nil
}

func copySyncTask(ctx context.Context, sourceFS, targetFS afero.Fs, task syncFileTask, overwrite bool, onRead func(int64), waitIfPaused func(context.Context) error) (int64, string, error) {
	if err := waitForSyncTurn(ctx, waitIfPaused); err != nil {
		return 0, "failed", err
	}
	if !overwrite {
		if _, err := targetFS.Stat(task.DestinationPath); err == nil {
			return 0, "skipped", nil
		} else if !os.IsNotExist(err) {
			return 0, "failed", err
		}
	}
	file, _, err := openFile(sourceFS, task.SourcePath)
	if err != nil {
		return 0, "failed", err
	}
	defer file.Close()
	copied := int64(0)
	reader := progressReader{
		reader:     file,
		beforeRead: func() error { return waitForSyncTurn(ctx, waitIfPaused) },
		onRead: func(n int64) {
			copied += n
			if onRead != nil {
				onRead(n)
			}
		},
	}
	if err := writeFile(targetFS, task.DestinationPath, reader); err != nil {
		return copied, "failed", err
	}
	return copied, "copied", nil
}

func (j *syncJob) waitIfPaused(ctx context.Context) error {
	j.stateMu.Lock()
	defer j.stateMu.Unlock()
	for j.paused {
		if err := ctx.Err(); err != nil {
			return err
		}
		j.cond.Wait()
	}
	return ctx.Err()
}

func (j *syncJob) snapshot() SyncJobResponse {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return cloneSyncJobResponse(j.resp)
}

func (j *syncJob) update(update syncProgressUpdate) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if isTerminalSyncStatus(j.resp.Status) {
		return
	}
	j.resp.Total = update.Total
	j.resp.Processed = update.Processed
	j.resp.CurrentMountKey = update.CurrentMountKey
	j.resp.CurrentSourcePath = update.CurrentSourcePath
	j.resp.CurrentDestinationPath = update.CurrentDestinationPath
	j.resp.CurrentSize = update.CurrentSize
	j.resp.CurrentBytes = update.CurrentBytes
	j.resp.Copied = update.Response.Copied
	j.resp.Skipped = update.Response.Skipped
	j.resp.Failed = update.Response.Failed
	j.resp.BytesCopied = update.Response.BytesCopied
	j.resp.BytesTotal = update.Response.BytesTotal
	j.resp.Results = cloneSyncResults(update.Response.Results)
	j.resp.UpdatedAt = time.Now()
}

func (j *syncJob) complete(resp SyncResponse) {
	now := time.Now()
	j.mu.Lock()
	defer j.mu.Unlock()
	if isTerminalSyncStatus(j.resp.Status) {
		return
	}
	j.resp.Status = SyncJobCompleted
	j.resp.CurrentMountKey = ""
	j.resp.CurrentSourcePath = ""
	j.resp.CurrentDestinationPath = ""
	j.resp.CurrentSize = 0
	j.resp.CurrentBytes = 0
	j.resp.Processed = j.resp.Total
	j.resp.Copied = resp.Copied
	j.resp.Skipped = resp.Skipped
	j.resp.Failed = resp.Failed
	j.resp.BytesCopied = resp.BytesCopied
	j.resp.BytesTotal = resp.BytesTotal
	j.resp.Results = cloneSyncResults(resp.Results)
	j.resp.UpdatedAt = now
	j.resp.FinishedAt = &now
}

func (j *syncJob) fail(err error) {
	now := time.Now()
	j.mu.Lock()
	defer j.mu.Unlock()
	if isTerminalSyncStatus(j.resp.Status) {
		return
	}
	j.resp.Status = SyncJobFailed
	j.resp.Error = err.Error()
	j.resp.UpdatedAt = now
	j.resp.FinishedAt = &now
}

func (j *syncJob) finishCanceled(resp SyncResponse) {
	now := time.Now()
	j.mu.Lock()
	defer j.mu.Unlock()
	j.resp.Status = SyncJobCanceled
	j.resp.CurrentMountKey = ""
	j.resp.CurrentSourcePath = ""
	j.resp.CurrentDestinationPath = ""
	j.resp.CurrentSize = 0
	j.resp.CurrentBytes = 0
	j.resp.Copied = resp.Copied
	j.resp.Skipped = resp.Skipped
	j.resp.Failed = resp.Failed
	j.resp.BytesCopied = resp.BytesCopied
	j.resp.BytesTotal = resp.BytesTotal
	j.resp.Results = cloneSyncResults(resp.Results)
	j.resp.UpdatedAt = now
	j.resp.FinishedAt = &now
}

func (j *syncJob) pause() (SyncJobResponse, error) {
	j.stateMu.Lock()
	defer j.stateMu.Unlock()
	j.mu.Lock()
	defer j.mu.Unlock()
	if isTerminalSyncStatus(j.resp.Status) {
		return cloneSyncJobResponse(j.resp), nil
	}
	if j.resp.Status == SyncJobRunning {
		j.paused = true
		j.resp.Status = SyncJobPaused
		j.resp.UpdatedAt = time.Now()
	}
	return cloneSyncJobResponse(j.resp), nil
}

func (j *syncJob) resume() (SyncJobResponse, error) {
	j.stateMu.Lock()
	defer j.stateMu.Unlock()
	j.mu.Lock()
	defer j.mu.Unlock()
	if isTerminalSyncStatus(j.resp.Status) {
		return cloneSyncJobResponse(j.resp), nil
	}
	if j.resp.Status == SyncJobPaused {
		j.paused = false
		j.resp.Status = SyncJobRunning
		j.resp.UpdatedAt = time.Now()
		j.cond.Broadcast()
	}
	return cloneSyncJobResponse(j.resp), nil
}

func (j *syncJob) cancelJob() (SyncJobResponse, error) {
	j.stateMu.Lock()
	j.paused = false
	j.cond.Broadcast()
	j.stateMu.Unlock()
	if j.cancel != nil {
		j.cancel()
	}
	now := time.Now()
	j.mu.Lock()
	defer j.mu.Unlock()
	if !isTerminalSyncStatus(j.resp.Status) {
		j.resp.Status = SyncJobCanceled
		j.resp.CurrentMountKey = ""
		j.resp.CurrentSourcePath = ""
		j.resp.CurrentDestinationPath = ""
		j.resp.CurrentSize = 0
		j.resp.CurrentBytes = 0
		j.resp.UpdatedAt = now
		j.resp.FinishedAt = &now
	}
	return cloneSyncJobResponse(j.resp), nil
}

func (j *syncJob) isCanceled() bool {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.resp.Status == SyncJobCanceled
}

func isTerminalSyncStatus(status string) bool {
	return status == SyncJobCompleted || status == SyncJobFailed || status == SyncJobCanceled
}

func cloneSyncJobResponse(resp SyncJobResponse) SyncJobResponse {
	resp.TargetMountKeys = append([]string(nil), resp.TargetMountKeys...)
	resp.Results = cloneSyncResults(resp.Results)
	return resp
}

func cloneSyncResults(results []SyncTargetResult) []SyncTargetResult {
	if len(results) == 0 {
		return nil
	}
	out := make([]SyncTargetResult, len(results))
	for i, result := range results {
		out[i] = result
		out[i].Errors = append([]string(nil), result.Errors...)
	}
	return out
}

func (s *Service) pruneSyncJobsLocked(now time.Time) {
	for id, job := range s.syncJobs {
		resp := job.snapshot()
		if resp.FinishedAt != nil && now.Sub(*resp.FinishedAt) > syncJobRetention {
			delete(s.syncJobs, id)
		}
	}
}

func newSyncJobID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("sync-%d", time.Now().UnixNano())
	}
	return "sync-" + hex.EncodeToString(b[:])
}
