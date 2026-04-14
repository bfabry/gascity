package main

import (
	"path/filepath"
	"strings"

	"github.com/gastownhall/gascity/internal/sling"
)

func normalizePathForCompare(path string) string {
	return sling.NormalizePathForCompare(path)
}

func samePath(a, b string) bool {
	return sling.SamePath(a, b)
}

func pathWithinRoot(path, root string) bool {
	path = normalizePathForCompare(path)
	root = normalizePathForCompare(root)
	if path == "" || root == "" {
		return false
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || rel == "" || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
