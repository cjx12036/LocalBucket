package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/cjx12036/LocalBucket/app/internal/node"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	dufs := flag.String("dufs", "", "bundled Dufs binary")
	data := flag.String("data", "", "data directory")
	port := flag.Int("port", 47931, "LAN port")
	flag.Parse()
	if *dufs == "" {
		fmt.Fprintln(os.Stderr, "缺少内置文件服务")
		os.Exit(1)
	}
	s, e := node.Start(node.Options{DufsPath: *dufs, DataDir: *data, Port: *port})
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	defer s.Close()
	json.NewEncoder(os.Stdout).Encode(map[string]string{"admin_url": s.AdminURL, "token": s.Token, "bucket_dir": s.BucketDir, "data_dir": s.DataDir})
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	eof := make(chan struct{})
	go func() {
		var b [1]byte
		for {
			if _, e := os.Stdin.Read(b[:]); e != nil {
				close(eof)
				return
			}
		}
	}()
	select {
	case <-stop:
	case <-eof:
	}
}
