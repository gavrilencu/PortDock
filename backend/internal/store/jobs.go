package store

// AppJob is a command run on a schedule (cron) for an application, in a new
// container of its current image (or in a running service of a compose
// application), with its variables and persistent volumes.
type AppJob struct {
	ID            int64  `db:"id" json:"id"`
	ApplicationID int64  `db:"application_id" json:"application_id"`
	Name          string `db:"name" json:"name"`
	Schedule      string `db:"schedule" json:"schedule"` // cron, in the instance timezone
	Command       string `db:"command" json:"command"`
	Service       string `db:"service" json:"service"` // compose service ("" = the main one)
	Timeout       int    `db:"timeout" json:"timeout"` // seconds
	Enabled       bool   `db:"enabled" json:"enabled"`
	LastRunAt     string `db:"last_run_at" json:"last_run_at"`
	LastStatus    string `db:"last_status" json:"last_status"`
	CreatedAt     string `db:"created_at" json:"created_at"`
}

// JobRun is one execution of a job.
type JobRun struct {
	ID            int64  `db:"id" json:"id"`
	JobID         int64  `db:"job_id" json:"job_id"`
	ApplicationID int64  `db:"application_id" json:"application_id"`
	Trigger       string `db:"trigger" json:"trigger"` // schedule | manual
	Status        string `db:"status" json:"status"`   // running | success | failed | timeout
	ExitCode      int    `db:"exit_code" json:"exit_code"`
	Output        string `db:"output" json:"output,omitempty"`
	StartedAt     string `db:"started_at" json:"started_at"`
	FinishedAt    string `db:"finished_at" json:"finished_at"`
	StartedBy     int64  `db:"started_by" json:"started_by"`
}

// JobRunCols lists the columns of job_runs without the output (for lists).
const JobRunCols = "id, job_id, application_id, trigger, status, exit_code, '' AS output, started_at, finished_at, started_by"
