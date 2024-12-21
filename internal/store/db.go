//go:build linux

package store

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/sonroyaalmerol/pbs-plus/internal/syslog"
	"github.com/sonroyaalmerol/pbs-plus/internal/utils"
)

// Job represents the pbs-disk-backup-job-status model
type Job struct {
	ID               string      `json:"id"`
	Store            string      `json:"store"`
	Target           string      `json:"target"`
	Subpath          string      `json:"subpath"`
	Schedule         string      `json:"schedule"`
	Comment          string      `json:"comment"`
	NotificationMode string      `json:"notification-mode"`
	Namespace        string      `json:"ns"`
	NextRun          *int64      `json:"next-run"`
	LastRunUpid      *string     `json:"last-run-upid"`
	LastRunState     *string     `json:"last-run-state"`
	LastRunEndtime   *int64      `json:"last-run-endtime"`
	Duration         *int64      `json:"duration"`
	Exclusions       []Exclusion `json:"exclusions"`
	RawExclusions    string      `json:"rawexclusions"`
}

const JobCfgFile = "jobs.cfg"

// Target represents the pbs-model-targets model
type Target struct {
	Name             string `json:"name"`
	Path             string `json:"path"`
	IsAgent          bool   `json:"is_agent"`
	ConnectionStatus bool   `json:"connection_status"`
}

const TargetCfgFile = "targets.cfg"

type Exclusion struct {
	JobID   string `json:"job_id"`
	Path    string `json:"path"`
	Comment string `json:"comment"`
}

const ExclusionCfgFile = "exclusions.cfg"

type PartialFile struct {
	Path    string `json:"path"`
	Comment string `json:"comment"`
}

const PartialFileCfgFile = "partial-files.cfg"

// Store holds the database instance
type Store struct {
	Db         *CfgDatabase
	LastToken  *Token
	APIToken   *APIToken
	HTTPClient *http.Client
}

const DbFolder = "/etc/proxmox-backup/plus"

// Initialize initializes the database connection and returns a Store instance
func Initialize() (*Store, error) {
	err := os.MkdirAll(DbFolder, os.ModeDir)
	if err != nil {
		return nil, fmt.Errorf("Initialize: error creating config folder -> %w", err)
	}

	return &Store{Db: NewCfgDatabase()}, nil
}

// CreateTables creates the necessary tables in the SQLite database
func (store *Store) DefaultExclusions() error {
	file, err := os.Open(filepath.Join(DbFolder, "exclusions.cfg"))
	if err != nil {
		for _, path := range defaultExclusions {
			_ = store.CreateExclusion(Exclusion{
				Path:    path,
				Comment: "Generated from default list of exclusions",
			})
		}
	}
	file.Close()

	return nil
}

// Job CRUD

// CreateJob inserts a new Job into the database
func (store *Store) CreateJob(job Job) error {
	if !utils.IsValidNamespace(job.Namespace) && job.Namespace != "" {
		return fmt.Errorf("CreateJob: invalid namespace string -> %s", job.Namespace)
	}

	if !utils.IsValidPathString(job.Subpath) {
		return fmt.Errorf("CreateJob: invalid subpath string -> %s", job.Subpath)
	}

	jobsMap, err := store.Db.ReadCfgFile(filepath.Join(DbFolder, JobCfgFile))
	if err != nil {
		return fmt.Errorf("CreateJob: error reading cfg -> %w", err)
	}

	if _, ok := jobsMap[job.ID]; ok {
		return fmt.Errorf("CreateJob: %s already exists!", job.ID)
	}

	err = store.Db.WriteObject(filepath.Join(DbFolder, JobCfgFile), map[string]string{
		"id":                job.ID,
		"object":            "job",
		"store":             job.Store,
		"target":            job.Target,
		"subpath":           job.Subpath,
		"schedule":          job.Schedule,
		"comment":           job.Comment,
		"last-run-upid":     *job.LastRunUpid,
		"notification-mode": job.NotificationMode,
		"namespace":         job.Namespace,
	})
	if err != nil {
		return fmt.Errorf("CreateJob: error inserting data to cfg -> %w", err)
	}

	if len(job.Exclusions) > 0 {
		for _, exclusion := range job.Exclusions {
			err := store.CreateExclusion(exclusion)
			if err != nil {
				continue
			}
		}
	}

	err = store.SetSchedule(job)
	if err != nil {
		return fmt.Errorf("CreateJob: error setting job schedule -> %w", err)
	}

	return nil
}

// GetJob retrieves a Job by ID
func (store *Store) GetJob(id string) (*Job, error) {
	jobsMap, err := store.Db.ReadCfgFile(filepath.Join(DbFolder, JobCfgFile))
	if err != nil {
		return nil, fmt.Errorf("GetJob: error scanning job row -> %w", err)
	}

	jobMap := jobsMap[id]

	lastUpid := jobMap["last-run-upid"]
	job := Job{
		ID:               jobMap["id"],
		Store:            jobMap["store"],
		Target:           jobMap["target"],
		Subpath:          jobMap["subpath"],
		Schedule:         jobMap["schedule"],
		Comment:          jobMap["comment"],
		LastRunUpid:      &lastUpid,
		NotificationMode: jobMap["notification-mode"],
		Namespace:        jobMap["namespace"],
	}

	exclusions, err := store.GetAllJobExclusions(id)
	if err == nil {
		job.Exclusions = exclusions

		pathSlice := []string{}
		for _, exclusion := range exclusions {
			pathSlice = append(pathSlice, exclusion.Path)
		}

		job.RawExclusions = strings.Join(pathSlice, "\n")
	}

	globalExclusions, err := store.GetAllGlobalExclusions()
	if err == nil {
		for _, exclusion := range globalExclusions {
			job.Exclusions = append(job.Exclusions, exclusion)
		}
	}

	if job.LastRunUpid != nil && *job.LastRunUpid != "" {
		task, err := store.GetTaskByUPID(*job.LastRunUpid)
		if err != nil {
			log.Printf("GetJob: error getting task by UPID -> %v\n", err)
		} else {
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
	}

	nextSchedule, err := getNextSchedule(&job)
	if err != nil {
		nextSchedule = nil
	}

	if nextSchedule != nil {
		nextSchedUnix := nextSchedule.Unix()
		job.NextRun = &nextSchedUnix
	}

	return &job, nil
}

// UpdateJob updates an existing Job in the database
func (store *Store) UpdateJob(job Job) error {
	if !utils.IsValidNamespace(job.Namespace) && job.Namespace != "" {
		return fmt.Errorf("UpdateJob: invalid namespace string -> %s", job.Namespace)
	}

	if !utils.IsValidPathString(job.Subpath) {
		return fmt.Errorf("UpdateJob: invalid subpath string -> %s", job.Subpath)
	}

	jobsMap, err := store.Db.ReadCfgFile(filepath.Join(DbFolder, JobCfgFile))
	if err != nil {
		return fmt.Errorf("UpdateJob: error reading cfg -> %w", err)
	}

	jobsMap[job.ID] = map[string]string{
		"id":                job.ID,
		"object":            "job",
		"store":             job.Store,
		"target":            job.Target,
		"subpath":           job.Subpath,
		"schedule":          job.Schedule,
		"comment":           job.Comment,
		"last-run-upid":     *job.LastRunUpid,
		"notification-mode": job.NotificationMode,
		"namespace":         job.Namespace,
	}

	err = store.Db.WriteAllObjects(filepath.Join(DbFolder, JobCfgFile), jobsMap)
	if err != nil {
		return fmt.Errorf("UpdateJob: error updating cfg -> %w", err)
	}

	exclusionsMap, err := store.Db.ReadCfgFile(filepath.Join(DbFolder, ExclusionCfgFile))
	if err != nil {
		return fmt.Errorf("UpdateJob: error reading exclusions from cfg -> %w", err)
	}

	for k, exclusionMap := range exclusionsMap {
		if exclusionMap["job-id"] == job.ID {
			delete(exclusionsMap, k)
		}
	}

	err = store.Db.WriteAllObjects(filepath.Join(DbFolder, ExclusionCfgFile), exclusionsMap)
	if err != nil {
		return fmt.Errorf("UpdateJob: error deleting exclusions from cfg -> %w", err)
	}

	if len(job.Exclusions) > 0 {
		for _, exclusion := range job.Exclusions {
			if exclusion.JobID != job.ID {
				continue
			}

			err := store.CreateExclusion(exclusion)
			if err != nil {
				continue
			}
		}
	}

	err = store.SetSchedule(job)
	if err != nil {
		return fmt.Errorf("UpdateJob: error setting job schedule -> %w", err)
	}

	return nil
}

func (store *Store) SetSchedule(job Job) error {
	svcPath := fmt.Sprintf("pbs-plus-job-%s.service", strings.ReplaceAll(job.ID, " ", "-"))
	fullSvcPath := filepath.Join(TimerBasePath, svcPath)

	timerPath := fmt.Sprintf("pbs-plus-job-%s.timer", strings.ReplaceAll(job.ID, " ", "-"))
	fullTimerPath := filepath.Join(TimerBasePath, timerPath)

	if job.Schedule == "" {
		cmd := exec.Command("/usr/bin/systemctl", "disable", "--now", timerPath)
		cmd.Env = os.Environ()
		_ = cmd.Run()

		_ = os.Remove(fullSvcPath)
		_ = os.Remove(fullTimerPath)
	} else {
		err := generateService(&job)
		if err != nil {
			return fmt.Errorf("SetSchedule: error generating service -> %w", err)
		}

		err = generateTimer(&job)
		if err != nil {
			return fmt.Errorf("SetSchedule: error generating timer -> %w", err)
		}
	}

	cmd := exec.Command("/usr/bin/systemctl", "daemon-reload")
	cmd.Env = os.Environ()
	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("SetSchedule: error running daemon reload -> %w", err)
	}

	if job.Schedule == "" {
		return nil
	}

	cmd = exec.Command("/usr/bin/systemctl", "enable", "--now", timerPath)
	cmd.Env = os.Environ()
	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("SetSchedule: error running enable -> %w", err)
	}

	return nil
}

// DeleteJob deletes a Job from the database
func (store *Store) DeleteJob(id string) error {
	jobsMap, err := store.Db.ReadCfgFile(filepath.Join(DbFolder, JobCfgFile))
	if err != nil {
		return fmt.Errorf("DeleteJob: error reading cfg -> %w", err)
	}

	delete(jobsMap, id)

	err = store.Db.WriteAllObjects(filepath.Join(DbFolder, JobCfgFile), jobsMap)
	if err != nil {
		return fmt.Errorf("DeleteJob: error deleting job from cfg -> %w", err)
	}

	deleteSchedule(id)

	return nil
}

// GetAllJobs retrieves all Job records from the database
func (store *Store) GetAllJobs() ([]Job, error) {
	jobsMap, err := store.Db.ReadCfgFile(filepath.Join(DbFolder, JobCfgFile))
	if err != nil {
		return nil, fmt.Errorf("GetAllJobs: error reading cfg -> %w", err)
	}

	var jobs []Job

	for _, jobMap := range jobsMap {
		lastUpid := jobMap["last-run-upid"]
		job := Job{
			ID:               jobMap["id"],
			Store:            jobMap["store"],
			Target:           jobMap["target"],
			Subpath:          jobMap["subpath"],
			Schedule:         jobMap["schedule"],
			Comment:          jobMap["comment"],
			LastRunUpid:      &lastUpid,
			NotificationMode: jobMap["notification-mode"],
			Namespace:        jobMap["namespace"],
		}

		exclusions, err := store.GetAllJobExclusions(job.ID)
		if err == nil {
			job.Exclusions = exclusions
			pathSlice := []string{}
			for _, exclusion := range exclusions {
				pathSlice = append(pathSlice, exclusion.Path)
			}

			job.RawExclusions = strings.Join(pathSlice, "\n")
		}

		if job.LastRunUpid != nil && *job.LastRunUpid != "" {
			task, err := store.GetTaskByUPID(*job.LastRunUpid)
			if err != nil {
				log.Printf("GetAllJobs: error getting task by UPID -> %v\n", err)
			} else {
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
		}

		nextSchedule, err := getNextSchedule(&job)
		if err != nil {
			nextSchedule = nil
		}

		if nextSchedule != nil {
			nextSchedUnix := nextSchedule.Unix()
			job.NextRun = &nextSchedUnix
		}

		jobs = append(jobs, job)
	}

	return jobs, nil
}

func (store *Store) GetAllJobExclusions(jobId string) ([]Exclusion, error) {
	exclusionsMap, err := store.Db.ReadCfgFile(filepath.Join(DbFolder, ExclusionCfgFile))
	if err != nil {
		return nil, fmt.Errorf("GetAllJobExclusions: error reading cfg -> %w", err)
	}

	var exclusions []Exclusion
	for _, exclusionMap := range exclusionsMap {
		if excJobId, ok := exclusionMap["job-id"]; ok && excJobId == jobId {
			exclusion := Exclusion{
				Path:    exclusionMap["path"],
				Comment: exclusionMap["comment"],
				JobID:   exclusionMap["job-id"],
			}

			exclusions = append(exclusions, exclusion)
		}
	}

	return exclusions, nil
}

// Target CRUD

// CreateTarget inserts a new Target into the database
func (store *Store) CreateTarget(target Target) error {
	targetsMap, err := store.Db.ReadCfgFile(filepath.Join(DbFolder, TargetCfgFile))
	if err != nil {
		return fmt.Errorf("CreateTarget: error reading cfg -> %w", err)
	}

	if _, ok := targetsMap[target.Name]; ok {
		return store.UpdateTarget(target)
	}

	if _, ok := targetsMap[target.Path]; ok {
		return fmt.Errorf("CreateTarget: %s already exists!", target.Path)
	}

	err = store.Db.WriteObject(filepath.Join(DbFolder, TargetCfgFile), map[string]string{
		"object": "target",
		"id":     target.Name,
		"name":   target.Name,
		"path":   target.Path,
	})
	if err != nil {
		return fmt.Errorf("CreateTarget: error writing to target cfg -> %w", err)
	}

	return nil
}

// GetTarget retrieves a Target by ID
func (store *Store) GetTarget(name string) (*Target, error) {
	targetsMap, err := store.Db.ReadCfgFile(filepath.Join(DbFolder, TargetCfgFile))
	if err != nil {
		return nil, fmt.Errorf("GetTarget: error reading cfg -> %w", err)
	}

	var target Target
	if targetMap, ok := targetsMap[name]; ok {
		target = Target{
			Name: targetMap["name"],
			Path: targetMap["path"],
		}
	} else {
		return nil, fmt.Errorf("GetTarget: error getting target from cfg -> %w", err)
	}

	syslogger, err := syslog.InitializeLogger()
	if err != nil {
		return nil, fmt.Errorf("GetTarget: failed to initialize logger -> %w", err)
	}

	if strings.HasPrefix(target.Path, "agent://") {
		target.ConnectionStatus, err = store.AgentPing(&target)
		if err != nil {
			syslogger.Errorf("GetTarget: error agent ping -> %v", err)
		}
		target.IsAgent = true
	} else {
		target.ConnectionStatus = utils.IsValid(target.Path)
		target.IsAgent = false
	}

	return &target, nil
}

func (store *Store) GetAllTargetsByIP(ip string) ([]Target, error) {
	query := fmt.Sprintf("agent://%s/", ip)
	targetsMap, err := store.Db.ReadCfgFile(filepath.Join(DbFolder, TargetCfgFile))
	if err != nil {
		return nil, fmt.Errorf("GetAllTargetsByIP: error reading cfg -> %w", err)
	}

	var targets []Target
	for _, targetMap := range targetsMap {
		if !strings.HasPrefix(targetMap["path"], query) {
			continue
		}

		target := Target{
			Name: targetMap["name"],
			Path: targetMap["path"],
		}

		syslogger, err := syslog.InitializeLogger()
		if err != nil {
			return nil, fmt.Errorf("GetAllTargetsByIP: failed to initialize logger -> %w", err)
		}
		if strings.HasPrefix(target.Path, "agent://") {
			target.ConnectionStatus, err = store.AgentPing(&target)
			if err != nil {
				syslogger.Errorf("GetAllTargetsByIP: error agent ping -> %v", err)
			}
			target.IsAgent = true
		} else {
			target.ConnectionStatus = utils.IsValid(target.Path)
			target.IsAgent = false
		}

		targets = append(targets, target)
	}

	return targets, nil
}

// UpdateTarget updates an existing Target in the database
func (store *Store) UpdateTarget(target Target) error {
	targetsMap, err := store.Db.ReadCfgFile(filepath.Join(DbFolder, TargetCfgFile))
	if err != nil {
		return fmt.Errorf("UpdateTarget: error getting job exclusions select query -> %w", err)
	}

	targetsMap[target.Name] = map[string]string{
		"id":     target.Name,
		"object": "target",
		"path":   target.Path,
		"name":   target.Name,
	}

	err = store.Db.WriteAllObjects(filepath.Join(DbFolder, TargetCfgFile), targetsMap)
	if err != nil {
		return fmt.Errorf("UpdateTarget: error updating job table -> %w", err)
	}

	return nil
}

// DeleteTarget deletes a Target from the database
func (store *Store) DeleteTarget(name string) error {
	targetsMap, err := store.Db.ReadCfgFile(filepath.Join(DbFolder, TargetCfgFile))
	if err != nil {
		return fmt.Errorf("DeleteTarget: error getting job exclusions select query -> %w", err)
	}

	delete(targetsMap, name)

	err = store.Db.WriteAllObjects(filepath.Join(DbFolder, TargetCfgFile), targetsMap)
	if err != nil {
		return fmt.Errorf("DeleteTarget: error updating job table -> %w", err)
	}

	return nil
}

func (store *Store) GetAllTargets() ([]Target, error) {
	targetsMap, err := store.Db.ReadCfgFile(filepath.Join(DbFolder, TargetCfgFile))
	if err != nil {
		return nil, fmt.Errorf("GetAllTargets: error getting job exclusions select query -> %w", err)
	}
	var targets []Target
	for _, targetMap := range targetsMap {
		target := Target{
			Name: targetMap["name"],
			Path: targetMap["path"],
		}

		syslogger, err := syslog.InitializeLogger()
		if err != nil {
			return nil, fmt.Errorf("GetTarget: failed to initialize logger -> %w", err)
		}
		if strings.HasPrefix(target.Path, "agent://") {
			target.ConnectionStatus, err = store.AgentPing(&target)
			if err != nil {
				syslogger.Errorf("GetTarget: error agent ping -> %v", err)
			}
			target.IsAgent = true
		} else {
			target.ConnectionStatus = utils.IsValid(target.Path)
			target.IsAgent = false
		}

		targets = append(targets, target)
	}

	return targets, nil
}

func (store *Store) CreateExclusion(exclusion Exclusion) error {
	exclusionsMap, err := store.Db.ReadCfgFile(filepath.Join(DbFolder, ExclusionCfgFile))
	if err != nil {
		return fmt.Errorf("CreateExclusion: error getting job exclusions select query -> %w", err)
	}

	if _, ok := exclusionsMap[exclusion.Path]; ok {
		return nil
	}

	exclusion.Path = strings.ReplaceAll(exclusion.Path, "\\", "/")

	if !utils.IsValidPattern(exclusion.Path) {
		return fmt.Errorf("CreateExclusion: invalid path pattern -> %s", exclusion.Path)
	}

	err = store.Db.WriteObject(filepath.Join(DbFolder, ExclusionCfgFile), map[string]string{
		"object":  "exclusion",
		"id":      exclusion.Path,
		"path":    exclusion.Path,
		"comment": exclusion.Comment,
		"job-id":  exclusion.JobID,
	})
	if err != nil {
		return fmt.Errorf("CreateExclusion: error inserting data to job cfg -> %w", err)
	}

	return nil
}

func (store *Store) GetExclusion(path string) (*Exclusion, error) {
	exclusionsMap, err := store.Db.ReadCfgFile(filepath.Join(DbFolder, ExclusionCfgFile))
	if err != nil {
		return nil, fmt.Errorf("GetExclusion: error getting job exclusions select query -> %w", err)
	}

	if exclusionMap, ok := exclusionsMap[path]; ok {
		exclusion := Exclusion{
			Path:    exclusionMap["path"],
			Comment: exclusionMap["comment"],
			JobID:   exclusionMap["job-id"],
		}

		return &exclusion, nil
	}

	return nil, fmt.Errorf("GetExclusion: error scanning row from targets table -> %w", err)
}

// UpdateExclusion updates an existing Exclusion in the database
func (store *Store) UpdateExclusion(exclusion Exclusion) error {
	exclusion.Path = strings.ReplaceAll(exclusion.Path, "\\", "/")

	if !utils.IsValidPattern(exclusion.Path) {
		return fmt.Errorf("UpdateExclusion: invalid path pattern -> %s", exclusion.Path)
	}

	exclusionsMap, err := store.Db.ReadCfgFile(filepath.Join(DbFolder, ExclusionCfgFile))
	if err != nil {
		return fmt.Errorf("UpdateExclusion: error updating job table -> %w", err)
	}

	exclusionsMap[exclusion.Path] = map[string]string{
		"id":      exclusion.Path,
		"object":  "exclusion",
		"path":    exclusion.Path,
		"comment": exclusion.Comment,
		"job-id":  exclusion.JobID,
	}

	err = store.Db.WriteAllObjects(filepath.Join(DbFolder, JobCfgFile), exclusionsMap)
	if err != nil {
		return fmt.Errorf("UpdateExclusion: error updating job table -> %w", err)
	}

	return nil
}

// DeleteExclusion deletes a Exclusion from the database
func (store *Store) DeleteExclusion(path string) error {
	exclusionsMap, err := store.Db.ReadCfgFile(filepath.Join(DbFolder, ExclusionCfgFile))
	if err != nil {
		return fmt.Errorf("DeleteExclusion: error updating job table -> %w", err)
	}

	delete(exclusionsMap, path)

	err = store.Db.WriteAllObjects(filepath.Join(DbFolder, JobCfgFile), exclusionsMap)
	if err != nil {
		return fmt.Errorf("DeleteExclusion: error updating job table -> %w", err)
	}

	return nil
}

func (store *Store) GetAllGlobalExclusions() ([]Exclusion, error) {
	exclusionsMap, err := store.Db.ReadCfgFile(filepath.Join(DbFolder, ExclusionCfgFile))
	if err != nil {
		return nil, fmt.Errorf("GetAllGlobalExclusions: error updating job table -> %w", err)
	}

	var exclusions []Exclusion
	for _, exclusionMap := range exclusionsMap {
		if exclusionMap["job-id"] != "" {
			continue
		}
		exclusion := Exclusion{
			Path:    exclusionMap["path"],
			JobID:   exclusionMap["job-id"],
			Comment: exclusionMap["comment"],
		}

		exclusions = append(exclusions, exclusion)
	}

	return exclusions, nil
}

func (store *Store) CreatePartialFile(partialFile PartialFile) error {
	partialFilesMap, err := store.Db.ReadCfgFile(filepath.Join(DbFolder, PartialFileCfgFile))
	if err != nil {
		return fmt.Errorf("CreatePartialFile: error getting job exclusions select query -> %w", err)
	}

	partialFile.Path = strings.ReplaceAll(partialFile.Path, "\\", "/")

	if _, ok := partialFilesMap[partialFile.Path]; ok {
		return fmt.Errorf("CreatePartialFile: %s already exists!", partialFile.Path)
	}

	err = store.Db.WriteObject(filepath.Join(DbFolder, PartialFileCfgFile), map[string]string{
		"object":  "partial-file",
		"id":      partialFile.Path,
		"path":    partialFile.Path,
		"comment": partialFile.Comment,
	})
	if err != nil {
		return fmt.Errorf("CreatePartialFile: error inserting data to job cfg -> %w", err)
	}

	return nil
}
func (store *Store) GetPartialFile(path string) (*PartialFile, error) {
	partialFilesMap, err := store.Db.ReadCfgFile(filepath.Join(DbFolder, PartialFileCfgFile))
	if err != nil {
		return nil, fmt.Errorf("GetPartialFile: error getting job exclusions select query -> %w", err)
	}

	if partialFileMap, ok := partialFilesMap[path]; ok {
		partialFile := PartialFile{
			Path:    partialFileMap["path"],
			Comment: partialFileMap["comment"],
		}
		return &partialFile, nil
	}
	return nil, fmt.Errorf("GetPartialFile: error scanning row from targets table -> %w", err)
}

// UpdatePartialFile updates an existing PartialFile in the database
func (store *Store) UpdatePartialFile(partialFile PartialFile) error {
	partialFilesMap, err := store.Db.ReadCfgFile(filepath.Join(DbFolder, PartialFileCfgFile))
	if err != nil {
		return fmt.Errorf("UpdatePartialFile: error updating job table -> %w", err)
	}

	partialFilesMap[partialFile.Path] = map[string]string{
		"id":      partialFile.Path,
		"object":  "partial-file",
		"path":    partialFile.Path,
		"comment": partialFile.Comment,
	}

	err = store.Db.WriteAllObjects(filepath.Join(DbFolder, JobCfgFile), partialFilesMap)
	if err != nil {
		return fmt.Errorf("UpdatePartialFile: error updating job table -> %w", err)
	}

	return nil
}

// DeletePartialFile deletes a PartialFile from the database
func (store *Store) DeletePartialFile(path string) error {
	partialFilesMap, err := store.Db.ReadCfgFile(filepath.Join(DbFolder, PartialFileCfgFile))
	if err != nil {
		return fmt.Errorf("DeletePartialFile: error updating job table -> %w", err)
	}

	delete(partialFilesMap, path)

	err = store.Db.WriteAllObjects(filepath.Join(DbFolder, JobCfgFile), partialFilesMap)
	if err != nil {
		return fmt.Errorf("DeletePartialFile: error updating job table -> %w", err)
	}

	return nil
}

func (store *Store) GetAllPartialFiles() ([]PartialFile, error) {
	partialFilesMap, err := store.Db.ReadCfgFile(filepath.Join(DbFolder, PartialFileCfgFile))
	if err != nil {
		return nil, fmt.Errorf("GetAllPartialFiles: error updating job table -> %w", err)
	}

	var partialFiles []PartialFile
	for _, partialFileMap := range partialFilesMap {
		partialFile := PartialFile{
			Path:    partialFileMap["path"],
			Comment: partialFileMap["comment"],
		}

		partialFiles = append(partialFiles, partialFile)
	}

	return partialFiles, nil
}
