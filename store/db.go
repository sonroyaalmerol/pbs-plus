package store

import (
	"database/sql"
	"errors"

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
	NextRun          *int64  `db:"next_run" json:"next-run"`
	LastRunUpid      *string `db:"last_run_upid" json:"last-run-upid"`
	LastRunState     *string `db:"last_run_state" json:"last-run-state"`
	LastRunEndtime   *int64  `db:"last_run_endtime" json:"last-run-endtime"`
	Duration         *int64  `db:"duration" json:"duration"`
}

// Target represents the pbs-model-targets model
type Target struct {
	Name string `db:"name" json:"name"`
	Path string `db:"path" json:"path"`
}

// Run represents the run model with a reference to the Job
type Run struct {
	ID        int    `db:"id" json:"id"`
	JobID     string `db:"job_id" json:"job-id"` // References Job ID
	Timestamp int64  `db:"timestamp" json:"timestamp"`
}

// Store holds the database instance
type Store struct {
	Db *sql.DB
}

// Initialize initializes the database connection and returns a Store instance
func Initialize() (*Store, error) {
	db, err := sql.Open("sqlite3", "/var/lib/proxmox-backup/d2d.db")
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
        last_run_state TEXT,
        last_run_endtime INTEGER,
				notification_mode TEXT,
        duration INTEGER
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
	if err != nil {
		return err
	}

	// Create Run table if it doesn't exist
	createRunTable := `
    CREATE TABLE IF NOT EXISTS runs (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        job_id TEXT,
        timestamp INTEGER,
        FOREIGN KEY (job_id) REFERENCES disk_backup_job_status (id)
    );`

	_, err = store.Db.Exec(createRunTable)
	return err
}

// Job CRUD

// CreateJob inserts a new Job into the database
func (store *Store) CreateJob(job Job) error {
	query := `INSERT INTO disk_backup_job_status (id, store, target, schedule, comment, next_run, last_run_upid, last_run_state, last_run_endtime, duration, notification_mode) 
              VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);`
	_, err := store.Db.Exec(query, job.ID, job.Store, job.Target, job.Schedule, job.Comment, job.NextRun, job.LastRunUpid, job.LastRunState, job.LastRunEndtime, job.Duration, job.NotificationMode)
	return err
}

// GetJob retrieves a Job by ID
func (store *Store) GetJob(id string) (*Job, error) {
	query := `SELECT id, store, target, schedule, comment, next_run, last_run_upid, last_run_state, last_run_endtime, duration, notification_mode FROM disk_backup_job_status WHERE id = ?;`
	row := store.Db.QueryRow(query, id)

	var job Job
	err := row.Scan(&job.ID, &job.Store, &job.Target, &job.Schedule, &job.Comment, &job.NextRun, &job.LastRunUpid, &job.LastRunState, &job.LastRunEndtime, &job.Duration, &job.NotificationMode)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &job, nil
}

// UpdateJob updates an existing Job in the database
func (store *Store) UpdateJob(job Job) error {
	query := `UPDATE disk_backup_job_status SET store = ?, target = ?, schedule = ?, comment = ?, next_run = ?, last_run_upid = ?, last_run_state = ?, last_run_endtime = ?, duration = ?, notification_mode = ? 
              WHERE id = ?;`
	_, err := store.Db.Exec(query, job.Store, job.Target, job.Schedule, job.Comment, job.NextRun, job.LastRunUpid, job.LastRunState, job.LastRunEndtime, job.Duration, job.NotificationMode, job.ID)
	return err
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

// Run CRUD

// CreateRun inserts a new Run into the database
func (store *Store) CreateRun(run Run) error {
	query := `INSERT INTO runs (job_id, timestamp) VALUES (?, ?);`
	_, err := store.Db.Exec(query, run.JobID, run.Timestamp)
	return err
}

// GetRun retrieves a Run by ID
func (store *Store) GetRun(id int) (*Run, error) {
	query := `SELECT id, job_id, timestamp FROM runs WHERE id = ?;`
	row := store.Db.QueryRow(query, id)

	var run Run
	err := row.Scan(&run.ID, &run.JobID, &run.Timestamp)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &run, nil
}

// UpdateRun updates an existing Run in the database
func (store *Store) UpdateRun(run Run) error {
	query := `UPDATE runs SET job_id = ?, timestamp = ? WHERE id = ?;`
	_, err := store.Db.Exec(query, run.JobID, run.Timestamp, run.ID)
	return err
}

// DeleteRun deletes a Run from the database
func (store *Store) DeleteRun(id int) error {
	query := `DELETE FROM runs WHERE id = ?;`
	_, err := store.Db.Exec(query, id)
	return err
}

// GetAllJobes retrieves all Job records from the database
func (store *Store) GetAllJobs() ([]Job, error) {
	query := `SELECT id, store, target, schedule, comment, next_run, last_run_upid, last_run_state, last_run_endtime, duration, notification_mode FROM disk_backup_job_status;`
	rows, err := store.Db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []Job
	jobs = make([]Job, 0)
	for rows.Next() {
		var job Job
		err := rows.Scan(&job.ID, &job.Store, &job.Target, &job.Schedule, &job.Comment, &job.NextRun, &job.LastRunUpid, &job.LastRunState, &job.LastRunEndtime, &job.Duration, &job.NotificationMode)
		if err != nil {
			return nil, err
		}
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

// Run CRUD

// GetAllRuns retrieves all Run records from the database
func (store *Store) GetAllRuns() ([]Run, error) {
	query := `SELECT id, job_id, timestamp FROM runs;`
	rows, err := store.Db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []Run
	runs = make([]Run, 0)
	for rows.Next() {
		var run Run
		err := rows.Scan(&run.ID, &run.JobID, &run.Timestamp)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}

	return runs, rows.Err()
}
