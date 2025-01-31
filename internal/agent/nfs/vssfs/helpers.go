//go:build windows

package vssfs

import (
	"math"
	"runtime"

	"golang.org/x/sys/windows"
)

func skipPathWithAttributes(attrs uint32) bool {
	return attrs&(windows.FILE_ATTRIBUTE_REPARSE_POINT|
		windows.FILE_ATTRIBUTE_DEVICE|
		windows.FILE_ATTRIBUTE_OFFLINE|
		windows.FILE_ATTRIBUTE_VIRTUAL|
		windows.FILE_ATTRIBUTE_RECALL_ON_OPEN|
		windows.FILE_ATTRIBUTE_RECALL_ON_DATA_ACCESS) != 0
}

func calculateShardCount() int {
	cpuCount := runtime.NumCPU()
	targetCount := cpuCount * 2 // We want 2 shards per CPU core
	// Find next power of 2 above target count for efficient bit masking
	return int(math.Pow(2, math.Ceil(math.Log2(float64(targetCount)))))
}
