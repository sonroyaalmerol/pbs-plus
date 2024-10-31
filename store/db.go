package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Job represents the pbs-disk-backup-job-status model
type Job struct {
	ID               string  `db:"id" json:"id"`
	Store            string  `db:"store" json:"store"`
	Target           string  `db:"target" json:"target"`
	Schedule         string  `db:"schedule" json:"schedule"`
	Comment          string  `db:"comment" json:"comment"`
	NotificationMode string  `db:"notification_mode" json:"notification-mode"`
	Namespace        string  `db:"namespace" json:"namespace"`
	NextRun          *int64  `db:"next_run" json:"next-run"`
	LastRunUpid      *string `db:"last_run_upid" json:"last-run-upid"`
	LastRunState     *string `json:"last-run-state"`
	LastRunEndtime   *int64  `json:"last-run-endtime"`
	Duration         *int64  `json:"duration"`
}

// Target represents the pbs-model-targets model
type Target struct {
	Name string `db:"name" json:"name"`
	Path string `db:"path" json:"path"`
}

// Store holds the database instance
type Store struct {
	Db        *sql.DB
	LastToken *Token
}

// Initialize initializes the database connection and returns a Store instance
func Initialize() (*Store, error) {
	db, err := sql.Open("sqlite3", filepath.Join(DbBasePath, "d2d.db"))
	if err != nil {
		return nil, err
	}

	return &Store{Db: db}, nil
}

// CreateTables creates the necessary tables in the SQLite database
func (store *Store) CreateTables() error {
	// Create Job table if it doesn't exist
	createJobTable := `
    CREATE TABLE IF NOT EXISTS disk_backup_job_status (
        id TEXT PRIMARY KEY,
        store TEXT,
        target TEXT,
        schedule TEXT,
        comment TEXT,
        next_run INTEGER,
        last_run_upid TEXT,
				namespace TEXT,
				notification_mode TEXT
    );`

	_, err := store.Db.Exec(createJobTable)
	if err != nil {
		return err
	}

	// Create Target table if it doesn't exist
	createTargetTable := `
    CREATE TABLE IF NOT EXISTS targets (
        name TEXT PRIMARY KEY,
        path TEXT
    );`

	_, err = store.Db.Exec(createTargetTable)
	return err
}

// Job CRUD

// CreateJob inserts a new Job into the database
func (store *Store) CreateJob(job Job) error {
	query := `INSERT INTO disk_backup_job_status (id, store, target, schedule, comment, next_run, last_run_upid, notification_mode, namespace) 
              VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);`
	_, err := store.Db.Exec(query, job.ID, job.Store, job.Target, job.Schedule, job.Comment, job.NextRun, job.LastRunUpid, job.NotificationMode, job.Namespace)
	if err != nil {
		return err
	}

	err = store.SetSchedule(job)
	return err
}

// GetJob retrieves a Job by ID
func (store *Store) GetJob(id string) (*Job, error) {
	query := `SELECT id, store, target, schedule, comment, next_run, last_run_upid, notification_mode, namespace FROM disk_backup_job_status WHERE id = ?;`
	row := store.Db.QueryRow(query, id)

	var job Job
	err := row.Scan(&job.ID, &job.Store, &job.Target, &job.Schedule, &job.Comment, &job.NextRun, &job.LastRunUpid, &job.NotificationMode, &job.Namespace)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	if job.LastRunUpid != nil {
		task, err := GetTaskByUPID(*job.LastRunUpid, store.LastToken)
		if err != nil {
			return nil, err
		}

		job.LastRunEndtime = &task.EndTime

		if task.Status == "stopped" {
			job.LastRunState = &task.ExitStatus

			tmpDuration := task.EndTime - task.StartTime
			job.Duration = &tmpDuration
		} else {
			tmpDuration := time.Now().Unix() - task.StartTime
			job.Duration = &tmpDuration
		}
	}

	nextSchedule, err := getNextSchedule(&job)
	if err != nil {
		return nil, err
	}
	nextSchedUnix := nextSchedule.Unix()

	job.NextRun = &nextSchedUnix

	return &job, nil
}

// UpdateJob updates an existing Job in the database
func (store *Store) UpdateJob(job Job) error {
	query := `UPDATE disk_backup_job_status SET store = ?, target = ?, schedule = ?, comment = ?, next_run = ?, last_run_upid = ?, notification_mode = ?, namespace = ? WHERE id = ?;`
	_, err := store.Db.Exec(query, job.Store, job.Target, job.Schedule, job.Comment, job.NextRun, job.LastRunUpid, job.NotificationMode, job.Namespace, job.ID)
	if err != nil {
		return err
	}

	err = store.SetSchedule(job)
	return err
}

func (store *Store) SetSchedule(job Job) error {
	svcPath := fmt.Sprintf("proxmox-d2d-job-%s.service", strings.ReplaceAll(job.ID, " ", "-"))
	fullSvcPath := filepath.Join(TimerBasePath, svcPath)

	timerPath := fmt.Sprintf("proxmox-d2d-job-%s.timer", strings.ReplaceAll(job.ID, " ", "-"))
	fullTimerPath := filepath.Join(TimerBasePath, timerPath)

	if job.Schedule == "" {
		err := os.Remove(fullSvcPath)
		if err != nil {
			return err
		}

		err = os.Remove(fullTimerPath)
		if err != nil {
			return err
		}
	} else {
		err := generateService(&job)
		if err != nil {
			return err
		}

		err = generateTimer(&job)
		if err != nil {
			return err
		}
	}

	cmd := exec.Command("/usr/bin/systemctl", "daemon-reload")
	cmd.Env = os.Environ()
	err := cmd.Run()
	if err != nil {
		return err
	}

	if job.Schedule == "" {
		return nil
	}

	cmd = exec.Command("/usr/bin/systemctl", "enable", "--now", timerPath)
	cmd.Env = os.Environ()
	err = cmd.Run()
	if err != nil {
		return err
	}

	return nil
}

// DeleteJob deletes a Job from the database
func (store *Store) DeleteJob(id string) error {
	query := `DELETE FROM disk_backup_job_status WHERE id = ?;`
	_, err := store.Db.Exec(query, id)
	return err
}

// Target CRUD

// CreateTarget inserts a new Target into the database
func (store *Store) CreateTarget(target Target) error {
	query := `INSERT INTO targets (name, path) VALUES (?, ?);`
	_, err := store.Db.Exec(query, target.Name, target.Path)
	return err
}

// GetTarget retrieves a Target by ID
func (store *Store) GetTarget(name string) (*Target, error) {
	query := `SELECT name, path FROM targets WHERE name = ?;`
	row := store.Db.QueryRow(query, name)

	var target Target
	err := row.Scan(&target.Name, &target.Path)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &target, nil
}

// UpdateTarget updates an existing Target in the database
func (store *Store) UpdateTarget(target Target) error {
	query := `UPDATE targets SET path = ? WHERE name = ?;`
	_, err := store.Db.Exec(query, target.Path, target.Name)
	return err
}

// DeleteTarget deletes a Target from the database
func (store *Store) DeleteTarget(name string) error {
	query := `DELETE FROM targets WHERE name = ?;`
	_, err := store.Db.Exec(query, name)
	return err
}

// GetAllJobes retrieves all Job records from the database
func (store *Store) GetAllJobs() ([]Job, error) {
	query := `SELECT id, store, target, schedule, comment, next_run, last_run_upid, notification_mode, namespace FROM disk_backup_job_status;`
	rows, err := store.Db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []Job
	jobs = make([]Job, 0)
	for rows.Next() {
		var job Job
		err := rows.Scan(&job.ID, &job.Store, &job.Target, &job.Schedule, &job.Comment, &job.NextRun, &job.LastRunUpid, &job.NotificationMode, &job.Namespace)
		if err != nil {
			return nil, err
		}

		if job.LastRunUpid != nil {
			task, err := GetTaskByUPID(*job.LastRunUpid, store.LastToken)
			if err != nil {
				return nil, err
			}

			job.LastRunEndtime = &task.EndTime

			if task.Status == "stopped" {
				job.LastRunState = &task.ExitStatus

				tmpDuration := task.EndTime - task.StartTime
				job.Duration = &tmpDuration
			} else {
				tmpDuration := time.Now().Unix() - task.StartTime
				job.Duration = &tmpDuration
			}
		}

		nextSchedule, err := getNextSchedule(&job)
		if err != nil {
			return nil, err
		}
		nextSchedUnix := nextSchedule.Unix()

		job.NextRun = &nextSchedUnix
		jobs = append(jobs, job)
	}

	return jobs, rows.Err()
}

// Target CRUD

// GetAllTargets retrieves all Target records from the database
func (store *Store) GetAllTargets() ([]Target, error) {
	query := `SELECT name, path FROM targets;`
	rows, err := store.Db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var targets []Target
	targets = make([]Target, 0)
	for rows.Next() {
		var target Target
		err := rows.Scan(&target.Name, &target.Path)
		if err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}

	return targets, rows.Err()
}
