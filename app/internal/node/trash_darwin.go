package node

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func trash(p string) error {
	executable, e := os.Executable()
	if e != nil {
		return e
	}
	helper := filepath.Join(filepath.Dir(executable), "localbucket-trash")
	if data, e := exec.Command(helper, p).CombinedOutput(); e != nil {
		return fmt.Errorf("无法移入系统废纸篓：%s", string(data))
	}
	return nil
}
