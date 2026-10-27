//go:build !windows

package node

import "os"

func isReparse(_ string, _ os.FileInfo) bool { return false }
