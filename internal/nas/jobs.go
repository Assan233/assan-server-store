package nas

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"syscall"
	"time"

	_ "modernc.org/sqlite"
)

// ErrQueueFull reports that the conversion queue has no more capacity.
var ErrQueueFull = errors.New("NAS conversion queue is full")

// Job is a persistent record of one NAS conversion.
type Job struct {
	ID        int64     `json:"id"`
	Source    string    `json:"source"`
	Directory string    `json:"directory"`
	Status    string    `json:"status"`
	Path      string    `json:"path,omitempty"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

// Jobs owns the persistent queue and its single background worker.
type Jobs struct {
	db     *sql.DB
	wake   chan struct{}
	done   chan struct{}
	cancel context.CancelFunc
}

// OpenJobs opens the conversion database and starts processing pending jobs.
func OpenJobs(ctx context.Context, filename string, media *Service) (*Jobs, error) {
	return openJobs(ctx, filename, media.Convert)
}

func openJobs(ctx context.Context, filename string, convert func(context.Context, string, string) (string, error)) (*Jobs, error) {
	db, err := sql.Open("sqlite", filename+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS conversions (
		id INTEGER PRIMARY KEY, source TEXT NOT NULL, directory TEXT NOT NULL,
		status TEXT NOT NULL, path TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL
	);
	CREATE INDEX IF NOT EXISTS conversions_status_id ON conversions(status, id);
	UPDATE conversions SET status='failed', error='转换因服务重启而中断，请检查输出目录后重新提交。' WHERE status='running';`); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize NAS conversions: %w", err)
	}
	worker, cancel := context.WithCancel(ctx)
	j := &Jobs{db: db, wake: make(chan struct{}, 1), done: make(chan struct{}), cancel: cancel}
	go j.run(worker, convert)
	return j, nil
}

// Close stops the worker before closing the database.
func (j *Jobs) Close() error {
	j.cancel()
	<-j.done
	return j.db.Close()
}

// Submit validates and persists one task without waiting for conversion.
func (j *Jobs) Submit(ctx context.Context, source, directory string) (Job, error) {
	if _, err := videoDemuxer(source, directory); err != nil {
		return Job{}, err
	}
	job := Job{Source: source, Directory: directory, Status: "queued", CreatedAt: time.Now().UTC().Truncate(time.Millisecond)}
	err := j.db.QueryRowContext(ctx, `INSERT INTO conversions(source, directory, status, created_at)
		SELECT ?, ?, 'queued', ? WHERE (SELECT COUNT(*) FROM conversions WHERE status IN ('queued','running')) < 512
		RETURNING id`, source, directory, job.CreatedAt.UnixMilli()).Scan(&job.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrQueueFull
	}
	if err == nil {
		select {
		case j.wake <- struct{}{}:
		default:
		}
	}
	return job, err
}

// List returns all active tasks and the 100 most recent finished records.
func (j *Jobs) List(ctx context.Context) ([]Job, error) {
	rows, err := j.db.QueryContext(ctx, `SELECT id,source,directory,status,path,error,created_at FROM conversions
		WHERE status IN ('queued','running') OR id IN (
			SELECT id FROM conversions WHERE status IN ('completed','failed') ORDER BY id DESC LIMIT 100
		) ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := []Job{}
	for rows.Next() {
		var job Job
		var createdAt int64
		if err := rows.Scan(&job.ID, &job.Source, &job.Directory, &job.Status, &job.Path, &job.Error, &createdAt); err != nil {
			return nil, err
		}
		job.CreatedAt = time.UnixMilli(createdAt).UTC()
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (j *Jobs) run(ctx context.Context, convert func(context.Context, string, string) (string, error)) {
	defer close(j.done)
	for ctx.Err() == nil {
		var job Job
		err := j.db.QueryRowContext(ctx, `UPDATE conversions SET status='running'
			WHERE id=(SELECT id FROM conversions WHERE status='queued' ORDER BY id LIMIT 1)
			RETURNING id,source,directory`).Scan(&job.ID, &job.Source, &job.Directory)
		if err == nil {
			j.process(ctx, job, convert)
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) && ctx.Err() == nil {
			slog.Error("claim NAS conversion", "error", err)
		}
		select {
		case <-ctx.Done():
		case <-j.wake:
		case <-time.After(2 * time.Second):
		}
	}
}

func (j *Jobs) process(ctx context.Context, job Job, convert func(context.Context, string, string) (string, error)) {
	var output string
	var err error
	for {
		output, err = convert(ctx, job.Source, job.Directory)
		if !errors.Is(err, ErrBusy) {
			break
		}
		select {
		case <-ctx.Done():
			err = ctx.Err()
		case <-time.After(time.Second):
			continue
		}
		break
	}
	status, message := "completed", ""
	if err != nil {
		status, message = "failed", jobError(err)
	}
	// Persist the result even if shutdown canceled the worker context.
	for {
		writeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, dbErr := j.db.ExecContext(writeCtx, `UPDATE conversions SET status=?, path=?, error=? WHERE id=?`, status, output, message, job.ID)
		cancel()
		if dbErr == nil {
			return
		}
		slog.Error("save NAS conversion result", "job_id", job.ID, "error", dbErr)
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

func jobError(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "转换因服务停止而中断，请重新提交。"
	case errors.Is(err, context.DeadlineExceeded):
		return "转换超过 15 分钟，已停止。"
	case errors.Is(err, fs.ErrNotExist):
		return "源文件或输出文件夹已不存在。"
	case errors.Is(err, fs.ErrExist):
		return "目标文件已存在，未覆盖原文件。"
	case errors.Is(err, fs.ErrPermission), errors.Is(err, syscall.EROFS):
		return "无法读取源文件或写入输出目录，请检查 NAS 权限。"
	case errors.Is(err, ErrMedia), errors.Is(err, ErrInput), errors.Is(err, ErrChanged):
		return "无法处理源文件，请确认文件未被替换，并检查格式、音轨和输出大小。"
	default:
		return "无法访问 NAS 文件，请检查目录权限和存储状态。"
	}
}
