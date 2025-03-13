package snapshots

import (
	"errors"
	"time"

	"github.com/sonroyaalmerol/pbs-plus/internal/arpc/arpcdata"
)

// Snapshot represents a generic snapshot
type Snapshot struct {
	Path        string          `json:"path"`
	TimeStarted time.Time       `json:"time_started"`
	SourcePath  string          `json:"source_path"`
	Direct      bool            `json:"direct"`
	Handler     SnapshotHandler `json:"-"`
}

func (req *Snapshot) Encode() ([]byte, error) {
	enc := arpcdata.NewEncoderWithSize(len(req.Path))
	if err := enc.WriteString(req.Path); err != nil {
		return nil, err
	}
	if err := enc.WriteInt64(req.TimeStarted.UnixNano()); err != nil {
		return nil, err
	}
	if err := enc.WriteString(req.SourcePath); err != nil {
		return nil, err
	}
	if err := enc.WriteBool(req.Direct); err != nil {
		return nil, err
	}
	return enc.Bytes(), nil
}

func (req *Snapshot) Decode(buf []byte) error {
	dec, err := arpcdata.NewDecoder(buf)
	if err != nil {
		return err
	}
	path, err := dec.ReadString()
	if err != nil {
		return err
	}
	req.Path = path
	timeStarted, err := dec.ReadInt64()
	if err != nil {
		return err
	}
	req.TimeStarted = time.Unix(0, timeStarted)
	sourcePath, err := dec.ReadString()
	if err != nil {
		return err
	}
	req.SourcePath = sourcePath
	direct, err := dec.ReadBool()
	if err != nil {
		return err
	}
	req.Direct = direct
	return nil
}

// SnapshotHandler defines the interface for snapshot operations
type SnapshotHandler interface {
	CreateSnapshot(jobId string, sourcePath string) (Snapshot, error)
	DeleteSnapshot(snapshot Snapshot) error
	IsSupported(sourcePath string) bool
}

var (
	ErrSnapshotTimeout  = errors.New("timeout waiting for in-progress snapshot")
	ErrSnapshotCreation = errors.New("failed to create snapshot")
	ErrInvalidSnapshot  = errors.New("invalid snapshot")
)
