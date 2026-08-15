package logging

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestRotatesAndBoundsHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nabud.log")
	writer, err := Open(path, 16, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"first-entry\n", "second-entry\n", "third-entry\n", "fourth-entry\n"} {
		if _, err := writer.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatal("most recent rotated log is missing:", err)
	}
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Fatal("rotation retained more backups than configured")
	}
}

func TestConcurrentWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nabud.log")
	writer, err := Open(path, 1024*1024, 2)
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for index := 0; index < 8; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for line := 0; line < 100; line++ {
				_, _ = writer.Write([]byte("one complete line\n"))
			}
		}()
	}
	group.Wait()
	_ = writer.Close()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(content), "one complete line") != 800 {
		t.Fatal("concurrent log writes were lost")
	}
}
