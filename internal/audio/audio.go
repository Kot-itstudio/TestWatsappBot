package audio

import (
	"log"
	"os"
	"path/filepath"
	"strings"
)

var allowedExt = []string{".mp3", ".ogg", ".wav", ".m4a"}

type Resolved struct {
	Name string
	Path string
}

func hasAllowedExt(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	for _, e := range allowedExt {
		if ext == e {
			return true
		}
	}
	return false
}

func Resolve(dir, name string) (*Resolved, error) {
	safeName := filepath.Base(strings.TrimSpace(name))
	if safeName == "" || safeName == "." || safeName == ".." {
		return nil, os.ErrNotExist
	}
	if !hasAllowedExt(safeName) {
		return nil, errBadExt
	}
	full := filepath.Join(dir, safeName)
	cleanDir := filepath.Clean(dir) + string(os.PathSeparator)
	if !strings.HasPrefix(filepath.Clean(full)+string(os.PathSeparator), cleanDir) && filepath.Clean(full) != cleanDir {
		return nil, errEscape
	}
	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		return nil, os.ErrNotExist
	}
	return &Resolved{Name: safeName, Path: full}, nil
}

func List(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("[audio] не удалось прочитать папку %s: %v", dir, err)
		}
		return nil
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && hasAllowedExt(e.Name()) {
			files = append(files, e.Name())
		}
	}
	return files
}
