package shared

import (
	"io"
	"time"
)

// DatastoreFileInfo holds the downloaded file stream along with metadata.
type DatastoreFileInfo struct {
	io.ReadCloser
	Size    int64
	ModTime time.Time
}
