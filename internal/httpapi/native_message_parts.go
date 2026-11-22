package httpapi

import (
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"os"
	"strings"

	"go-bot/harness/attachments"
)

const maxCreatorInputBytes int64 = 200 << 20
const maxCreatorFileBytes int64 = 32 << 20
const maxMessageImages = 20
const maxCreatorInputRequestBytes int64 = maxCreatorInputBytes + 1<<20
const maxMessageFiles = 8
const maxMessageContentBytes = 256 << 10

var errInputTooLarge = errors.New("input file exceeds limit")
var errInvalidInputUpload = errors.New("invalid input upload")

// A native multipart request keeps explicit image and verbatim file parts in
// submission order. The text field is read first for admission; the final message
// puts attachments first and appends the user text.
type stagedNativePart struct {
	file      *os.File
	name      string
	mediaType string
	kind      string // image or file
}

func stageNativeMessageParts(ctx context.Context, reader *multipart.Reader) (string, []stagedNativePart, error) {
	var content string
	var contentSeen bool
	parts := make([]stagedNativePart, 0)
	fail := func(err error) (string, []stagedNativePart, error) {
		for _, part := range parts {
			_ = part.file.Close()
		}
		return "", nil, err
	}
	var total int64
	var imageCount, fileCount int
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fail(err)
		}
		switch part.FormName() {
		case "content":
			if contentSeen || part.FileName() != "" || len(parts) != 0 {
				return fail(errInvalidInputUpload)
			}
			value, err := io.ReadAll(io.LimitReader(part, maxMessageContentBytes+1))
			if err != nil {
				return fail(err)
			}
			if len(value) > maxMessageContentBytes {
				return fail(errInputTooLarge)
			}
			content, contentSeen = string(value), true
		case "files", "file", "images", "image":
			if !contentSeen || part.FileName() == "" || len(part.FileName()) > 255 {
				return fail(errInvalidInputUpload)
			}
			kind := "file"
			partLimit := maxCreatorFileBytes
			var mediaType string
			if part.FormName() == "images" || part.FormName() == "image" {
				kind = "image"
				imageCount++
				if imageCount > maxMessageImages {
					return fail(errInputTooLarge)
				}
				partLimit = attachments.MaxImageSourceBytes
				mediaType, _, err = mime.ParseMediaType(part.Header.Get("Content-Type"))
				if err != nil || mediaType == "" {
					return fail(errInvalidInputUpload)
				}
			}
			if kind == "file" {
				fileCount++
				if fileCount > maxMessageFiles {
					return fail(errInputTooLarge)
				}
			}
			limit := min(partLimit, maxCreatorInputBytes-total)
			file, err := os.CreateTemp("", "serveragent-native-message-")
			if err != nil {
				return fail(err)
			}
			if err := os.Remove(file.Name()); err != nil {
				_ = file.Close()
				return fail(err)
			}
			n, err := io.Copy(file, io.LimitReader(part, limit+1))
			if err != nil || n > limit {
				_ = file.Close()
				if err != nil {
					return fail(err)
				}
				return fail(errInputTooLarge)
			}
			total += n
			parts = append(parts, stagedNativePart{file: file, name: part.FileName(), mediaType: mediaType, kind: kind})
		default:
			return fail(errInvalidInputUpload)
		}
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
	}
	if !contentSeen || (strings.TrimSpace(content) == "" && len(parts) == 0) {
		return fail(errInvalidInputUpload)
	}
	return content, parts, nil
}
