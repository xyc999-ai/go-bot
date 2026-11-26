package httpapi

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"go-bot/internal/agentfs"

	"github.com/gin-gonic/gin"
)

func downloadWorkspaceFile(c *gin.Context, workspace string) {
	serveWorkspaceFile(c.Writer, c.Request, workspace)
}

func serveWorkspaceFile(w http.ResponseWriter, r *http.Request, workspace string) {
	path := r.URL.Query().Get("path")
	flags := os.O_RDONLY | syscall.O_NONBLOCK
	if filepath.IsAbs(path) {
		// A present card carries the Host path. Resolve it within this Session
		// and apply the same final-symlink rule as the present tool.
		_, relative, err := agentfs.PresentedPath(workspace, workspace, path)
		if err != nil {
			writeSessionJSON(w, 404, gin.H{"error": "file_not_found"})
			return
		}
		path = relative
		flags |= syscall.O_NOFOLLOW
	}
	rel, err := workspacePath(path, false)
	if err != nil {
		writeSessionJSON(w, 400, gin.H{"error": "invalid_workspace_path"})
		return
	}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		writeSessionJSON(w, 404, gin.H{"error": "workspace_unavailable"})
		return
	}
	defer root.Close()
	file, err := root.OpenFile(rel, flags, 0)
	if err != nil {
		writeSessionJSON(w, 404, gin.H{"error": "file_not_found"})
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		writeSessionJSON(w, 400, gin.H{"error": "not_a_regular_file"})
		return
	}
	disposition := "attachment"
	if r.URL.Query().Get("inline") == "1" {
		mediaType := map[string]string{".mp4": "video/mp4", ".m4v": "video/mp4", ".webm": "video/webm", ".mov": "video/quicktime"}[strings.ToLower(filepath.Ext(rel))]
		if mediaType != "" {
			disposition = "inline"
			w.Header().Set("Content-Type", mediaType)
		}
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": filepath.Base(rel)}))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, info.Name(), info.ModTime(), file)
}

const maxWorkspaceListEntries = 200

type workspaceFileEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"isDir"`
	Size  int64  `json:"size"`
}

type workspaceFilePage struct {
	RootPath   string               `json:"rootPath,omitempty"`
	Path       string               `json:"path"`
	Files      []workspaceFileEntry `json:"files"`
	Truncated  bool                 `json:"truncated"`
	NextOffset int                  `json:"nextOffset,omitempty"`
}

// workspacePath is a path within the authenticated Session workspace. It is
// deliberately not an OSS key or an arbitrary container path.
func workspacePath(raw string, allowRoot bool) (string, error) {
	if raw == "" && allowRoot {
		return ".", nil
	}
	if raw == "" || filepath.IsAbs(raw) || strings.ContainsRune(raw, 0) || strings.Contains(raw, "\\") {
		return "", errors.New("invalid workspace path")
	}
	for _, part := range strings.Split(raw, "/") {
		if part == ".." {
			return "", errors.New("workspace path cannot leave the Session")
		}
	}
	clean := filepath.Clean(raw)
	if clean == "." && !allowRoot {
		return "", errors.New("workspace file path is required")
	}
	return clean, nil
}

func serveHostFiles(w http.ResponseWriter, r *http.Request, rootPath string) {
	if rootPath == "" {
		writeSessionJSON(w, http.StatusServiceUnavailable, gin.H{"error": "host_files_unavailable"})
		return
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		writeSessionJSON(w, http.StatusServiceUnavailable, gin.H{"error": "host_files_unavailable"})
		return
	}
	defer root.Close()
	listWorkspaceFiles(w, r, root, rootPath, func(err error) {
		if errors.Is(err, os.ErrNotExist) {
			writeSessionJSON(w, http.StatusNotFound, gin.H{"error": "directory_not_found"})
			return
		}
		writeSessionJSON(w, http.StatusBadRequest, gin.H{"error": "directory_unavailable"})
	})
}

func listWorkspaceFiles(w http.ResponseWriter, r *http.Request, root *os.Root, rootPath string, onError func(error)) {
	query := r.URL.Query()
	requestedPath := query.Get("path")
	rel, err := workspacePath(requestedPath, true)
	if err != nil {
		writeSessionJSON(w, http.StatusBadRequest, gin.H{"error": "invalid_workspace_path"})
		return
	}
	page := workspaceFilePage{RootPath: rootPath, Path: requestedPath, Files: []workspaceFileEntry{}}
	if rootPath != "" {
		page.Path = ""
		if rel != "." {
			page.Path = filepath.ToSlash(rel)
		}
	}
	offset := 0
	if raw := query.Get("offset"); raw != "" {
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 {
			writeSessionJSON(w, http.StatusBadRequest, gin.H{"error": "invalid_workspace_offset"})
			return
		}
	}
	// The Agent may create FIFOs or devices in its workspace. Opening a path
	// before checking its type must not block an HTTP worker on a FIFO reader.
	directory, err := root.OpenFile(rel, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		onError(err)
		return
	}
	defer directory.Close()
	info, err := directory.Stat()
	if err != nil || !info.IsDir() {
		writeSessionJSON(w, http.StatusBadRequest, gin.H{"error": "workspace_path_not_directory"})
		return
	}
	for remaining := offset; remaining > 0; {
		batchSize := min(remaining, maxWorkspaceListEntries)
		batch, readErr := directory.ReadDir(batchSize)
		remaining -= len(batch)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			onError(readErr)
			return
		}
		if remaining > 0 && (len(batch) == 0 || errors.Is(readErr, io.EOF)) {
			writeSessionJSON(w, http.StatusOK, page)
			return
		}
	}
	items, err := directory.ReadDir(maxWorkspaceListEntries + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		onError(err)
		return
	}
	page.Truncated = len(items) > maxWorkspaceListEntries
	if page.Truncated {
		items = items[:maxWorkspaceListEntries]
		page.NextOffset = offset + len(items)
	}
	for _, item := range items {
		itemInfo, statErr := item.Info()
		if statErr != nil || (!itemInfo.IsDir() && !itemInfo.Mode().IsRegular()) {
			continue
		}
		itemPath := item.Name()
		if rel != "." {
			itemPath = filepath.ToSlash(filepath.Join(rel, item.Name()))
		}
		page.Files = append(page.Files, workspaceFileEntry{Name: item.Name(), Path: itemPath, IsDir: itemInfo.IsDir(), Size: itemInfo.Size()})
	}
	writeSessionJSON(w, http.StatusOK, page)
}
