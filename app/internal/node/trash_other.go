//go:build !windows && !darwin

package node

import "errors"

func trash(string) error { return errors.New("当前平台不支持系统回收站") }
