package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
)

type file struct {
	Name, Path     string
	IsDir          bool `json:"is_dir"`
	Size, Modified int64
	Peer, Owner    string
	Local          bool
}
type transferItem struct {
	Peer string `json:"peer"`
	Path string `json:"path"`
}

func fileKey(f file) string { return f.Peer + "\x00" + f.Path }
func allLocal(entries []file) bool {
	if len(entries) == 0 {
		return false
	}
	for _, f := range entries {
		if !f.Local {
			return false
		}
	}
	return true
}
func selectionItems(entries []file) []transferItem {
	out := make([]transferItem, 0, len(entries))
	for _, f := range entries {
		out = append(out, transferItem{f.Peer, f.Path})
	}
	return out
}

// Enumeration order never expires an export; ownership and metadata changes do.
func metadataFingerprint(peerID, rel string, entries []file) string {
	copyEntries := append([]file(nil), entries...)
	sort.Slice(copyEntries, func(i, j int) bool { return fileKey(copyEntries[i]) < fileKey(copyEntries[j]) })
	b, _ := json.Marshal(struct {
		Peer, Path string
		Files      []file
	}{peerID, rel, copyEntries})
	return fmt.Sprintf("%x", sha256.Sum256(b))
}
