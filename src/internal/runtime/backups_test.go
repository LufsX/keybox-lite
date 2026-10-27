package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRuntimeBackupRetention(t *testing.T) {
	e := fixture(t)
	target := e.host("/data/adb/tricky_store/keybox.xml")
	now := time.Now()
	for i := 0; i < 7; i++ {
		name := fmt.Sprintf("%s.bak.%d", target, i)
		mustWrite(t, name, []byte(fmt.Sprint(i)))
		at := now.Add(time.Duration(i-10) * time.Hour)
		if err := os.Chtimes(name, at, at); err != nil {
			t.Fatal(err)
		}
	}
	foreign := target + ".bak.manual"
	mustWrite(t, foreign, []byte("manual"))
	link := target + ".bak.999"
	if err := os.Symlink(foreign, link); err != nil {
		t.Fatal(err)
	}
	directory := target + ".bak.888"
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	hardlink := target + ".bak.777"
	if err := os.Link(foreign, hardlink); err != nil {
		t.Fatal(err)
	}
	tgt, err := e.target()
	if err != nil {
		t.Fatal(err)
	}
	if err := e.install(context.Background(), tgt, []byte("NEW")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 7; i++ {
		_, err := os.Stat(fmt.Sprintf("%s.bak.%d", target, i))
		if i < 5 && !os.IsNotExist(err) {
			t.Fatalf("old backup %d retained: %v", i, err)
		}
		if i >= 5 && err != nil {
			t.Fatalf("recent backup %d removed: %v", i, err)
		}
	}
	for _, p := range []string{foreign, link, directory, hardlink} {
		if _, err := os.Lstat(p); err != nil {
			t.Fatalf("unrelated entry removed: %s: %v", p, err)
		}
	}
	files, _ := filepath.Glob(target + ".bak.*")
	found := false
	for _, p := range files {
		st, err := os.Lstat(p)
		if err == nil && st.Mode().IsRegular() && read(t, p) == "OLD" {
			found = true
		}
	}
	if !found {
		t.Fatal("newest backup missing")
	}
	// More installations keep three ordinary backups.
	os.Remove(link)
	os.Remove(directory)
	os.Remove(hardlink)
	os.Remove(foreign)
	for i := 0; i < 5; i++ {
		if err := e.install(context.Background(), tgt, []byte(fmt.Sprint(i))); err != nil {
			t.Fatal(err)
		}
		files, _ := filepath.Glob(target + ".bak.*")
		if len(files) != backupLimit {
			t.Fatalf("backups %d want %d", len(files), backupLimit)
		}
	}
}

func TestRuntimeFailedInstallKeepsExistingBackups(t *testing.T) {
	e := fixture(t)
	target := e.host("/data/adb/tricky_store/keybox.xml")
	for i := 0; i < 5; i++ {
		mustWrite(t, fmt.Sprintf("%s.bak.%d", target, i), []byte("existing"))
	}
	tgt, err := e.target()
	if err != nil {
		t.Fatal(err)
	}
	e.d.rename = func(string, string) error { return fmt.Errorf("injected failure") }
	if err := e.install(context.Background(), tgt, []byte("NEW")); err == nil {
		t.Fatal("expected failure")
	}
	if got := read(t, target); got != "OLD" {
		t.Fatal("old keybox changed")
	}
	files, _ := filepath.Glob(target + ".bak.*")
	if len(files) != 5 {
		t.Fatalf("failed install changed backups: %v", files)
	}
}
