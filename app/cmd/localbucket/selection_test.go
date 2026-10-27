package main

import "testing"

func TestOwnerIdentityAndSelection(t *testing.T) {
	entries := []file{{Peer: "a", Path: "same.txt", Local: true}, {Peer: "b", Path: "same.txt"}}
	if fileKey(entries[0]) == fileKey(entries[1]) {
		t.Fatal("owners collapsed")
	}
	if allLocal(entries) || allLocal(nil) || !allLocal(entries[:1]) {
		t.Fatal("local selection guard")
	}
	items := selectionItems(entries)
	if len(items) != 2 || items[0].Peer != "a" || items[1].Peer != "b" {
		t.Fatal("owner lost in transfer")
	}
	before := metadataFingerprint("", "", entries)
	entries[1].Peer = "c"
	if before == metadataFingerprint("", "", entries) {
		t.Fatal("owner change did not expire export")
	}
	entries[0], entries[1] = entries[1], entries[0]
	if metadataFingerprint("", "", entries) != metadataFingerprint("", "", []file{entries[1], entries[0]}) {
		t.Fatal("enumeration order expired export")
	}
}
