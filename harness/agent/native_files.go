package agent

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

// NativeFileAttachmentRef is DeepSeek's durable, verbatim file reference.
// Its bytes remain in attachment storage; a model request receives a handle,
// never a file content block or an inferred reading of those bytes.
type NativeFileAttachmentRef struct {
	ID    string
	Name  string
	Bytes int64
}

// NativeFilePathResolver maps a durable reference into the current execution
// world's readable saved-copy path. An empty result means that no such path is
// available. The resolver must not invent a path from the display filename.
type NativeFilePathResolver func(NativeFileAttachmentRef) string

// SetFilePathResolver changes only future model-request projections. The
// original file block remains unchanged in the durable Session surface.
func (a *NativeLoop) SetFilePathResolver(resolver NativeFilePathResolver) {
	a.runMu.Lock()
	defer a.runMu.Unlock()
	a.filePath = resolver
	a.modelProjection = nil
}

func nativeFileHandleText(ref NativeFileAttachmentRef, savedPath string) string {
	digest := strings.TrimPrefix(ref.ID, "sha256:")
	if len(digest) > 8 {
		digest = digest[:8]
	}
	identity := "File " + nativeJSONQuote(ref.Name) + " (" + strconv.FormatInt(ref.Bytes, 10) + " bytes, sha256:" + digest + ")"
	if savedPath == "" {
		return "[" + identity + " was uploaded, but the current execution environment cannot access a readable path. Report that limitation if its contents are needed; do not claim to have read it.]"
	}
	return "[" + identity + ": verbatim read-only copy saved at " + nativeJSONQuote(savedPath) + ". Read that path with your file tools when its contents are needed; copy it to a writable location before modifying it. When delegating file work, include this saved path in the delegation prompt; only subagents sharing this execution environment can read it.]"
}

func nativeJSONQuote(value string) string {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
	return strings.TrimSuffix(buffer.String(), "\n")
}
