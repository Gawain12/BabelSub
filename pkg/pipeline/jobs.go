package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type JobStatus string

const (
	JobQueued    JobStatus = "queued"
	JobRunning   JobStatus = "running"
	JobDone      JobStatus = "done"
	JobFailed    JobStatus = "failed"
	JobCancelled JobStatus = "cancelled"
)

type Job struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	ItemID    string    `json:"item_id,omitempty"`
	Request   Request   `json:"request"`
	Status    JobStatus `json:"status"`
	Step      int       `json:"step"`
	Steps     int       `json:"steps"`
	StepTitle string    `json:"step_title"`
	Logs      []string  `json:"logs"`
	Error     string    `json:"error,omitempty"`
	Output    string    `json:"output,omitempty"`
	Refreshed bool      `json:"refreshed"`
	// TempOutput marks outputs written to BabelSub's scratch dir that the user
	// is expected to download.
	TempOutput bool      `json:"temp_output"`
	CreatedAt  time.Time `json:"created_at"`
	EndedAt    time.Time `json:"ended_at,omitempty"`

	cancel context.CancelFunc
}

// JobManager runs jobs one at a time: ffsubsync is CPU heavy and remote media
// sources should not be hammered concurrently.
type JobManager struct {
	mu      sync.Mutex
	jobs    map[string]*Job
	queue   chan *Job
	seq     int
	workDir string
}

func NewJobManager(workDir string) *JobManager {
	m := &JobManager{jobs: map[string]*Job{}, queue: make(chan *Job, 64), workDir: workDir}
	return m
}

// Start launches the worker. envFor builds the environment per job so config
// changes apply to queued work; onDone runs after a successful job.
func (m *JobManager) Start(envFor func() Env, onDone func(*Job)) {
	go func() {
		for job := range m.queue {
			m.run(job, envFor(), onDone)
		}
	}()
}

func (m *JobManager) Submit(title, itemID string, req Request) (*Job, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.seq++
	job := &Job{
		ID:        fmt.Sprintf("%d-%d", time.Now().Unix(), m.seq),
		Title:     title,
		ItemID:    itemID,
		Request:   req,
		Status:    JobQueued,
		Steps:     4,
		CreatedAt: time.Now(),
		Logs:      []string{},
	}
	m.jobs[job.ID] = job
	m.mu.Unlock()

	select {
	case m.queue <- job:
		return job, nil
	default:
		m.update(job, func(j *Job) { j.Status = JobFailed; j.Error = "队列已满" })
		return nil, fmt.Errorf("任务队列已满，请稍后再试")
	}
}

func (m *JobManager) run(job *Job, env Env, onDone func(*Job)) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m.mu.Lock()
	if job.Status == JobCancelled {
		m.mu.Unlock()
		return
	}
	job.Status = JobRunning
	job.cancel = cancel
	m.mu.Unlock()

	env.Log = func(format string, args ...any) {
		line := time.Now().Format("15:04:05 ") + fmt.Sprintf(format, args...)
		m.update(job, func(j *Job) {
			// Collapse consecutive progress-bar updates into one line.
			if n := len(j.Logs); n > 0 && isProgress(line) && isProgress(j.Logs[n-1]) {
				j.Logs[n-1] = line
				return
			}
			j.Logs = append(j.Logs, line)
			if len(j.Logs) > 500 {
				j.Logs = j.Logs[len(j.Logs)-500:]
			}
		})
	}
	env.Step = func(step, total int, title string) {
		m.update(job, func(j *Job) { j.Step, j.Steps, j.StepTitle = step, total, title })
		env.Log("[%d/%d] %s", step, total, title)
	}

	dir := filepath.Join(m.workDir, job.ID)
	out, err := Run(ctx, env, job.Request, dir)
	os.RemoveAll(dir)

	m.update(job, func(j *Job) {
		j.EndedAt = time.Now()
		j.cancel = nil
		switch {
		case ctx.Err() != nil:
			j.Status = JobCancelled
			j.Error = "已取消"
		case err != nil:
			j.Status = JobFailed
			j.Error = err.Error()
		default:
			j.Status = JobDone
			j.Output = out
		}
	})
	if err == nil && onDone != nil {
		onDone(job)
	}
}

func isProgress(line string) bool {
	// tqdm lines look like "  45%|████▌     | 45/100 [...]"
	for i := 0; i+1 < len(line); i++ {
		if line[i] == '%' && line[i+1] == '|' {
			return true
		}
	}
	return false
}

func (m *JobManager) update(job *Job, fn func(*Job)) {
	m.mu.Lock()
	fn(job)
	m.mu.Unlock()
}

// Annotate lets callers attach post-processing results (e.g. library refresh).
func (m *JobManager) Annotate(job *Job, fn func(*Job)) { m.update(job, fn) }

func (m *JobManager) Cancel(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return false
	}
	switch j.Status {
	case JobQueued:
		j.Status = JobCancelled
		j.Error = "已取消"
		j.EndedAt = time.Now()
	case JobRunning:
		if j.cancel != nil {
			j.cancel()
		}
	default:
		return false
	}
	return true
}

func (m *JobManager) Get(id string) (Job, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return Job{}, false
	}
	cp := *j
	cp.Logs = append([]string(nil), j.Logs...)
	return cp, true
}

func (m *JobManager) List() []Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Job, 0, len(m.jobs))
	for _, j := range m.jobs {
		cp := *j
		cp.Logs = nil
		out = append(out, cp)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].CreatedAt.After(out[b].CreatedAt) })
	return out
}
