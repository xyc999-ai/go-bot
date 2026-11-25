package agent

import (
	"encoding/json"
	"errors"
)

// offloadOldestNativeImages follows the current model surface, not the full
// event log. Image indexes include already-offloaded occurrences, matching the
// DeepSeek v4 image/offload event contract.
func (a *NativeLoop) offloadOldestNativeImages(count int) (bool, error) {
	surface, err := a.session.ModelSurface()
	if err != nil {
		return false, err
	}
	return a.offloadNativeImagesFromSources(count, surface.MessageSeqs)
}

// The summarizer supplies only the surface nodes in its selected historical
// region. A current message outside that region must not lose an image because
// an auxiliary compaction request exceeded its own image budget.
func (a *NativeLoop) offloadNativeImagesFromSources(count int, sourceSeqs []uint64) (bool, error) {
	if count <= 0 {
		return false, nil
	}
	surface, err := a.session.ModelSurface()
	if err != nil {
		return false, err
	}
	if len(surface.Messages) != len(surface.MessageSeqs) {
		return false, errors.New("native surface lost message sequence mapping")
	}
	messageBySeq := make(map[uint64]json.RawMessage, len(surface.Messages))
	for index, seq := range surface.MessageSeqs {
		messageBySeq[seq] = surface.Messages[index]
	}
	targets := make([]imageOffloadTarget, 0)
	for _, seq := range sourceSeqs {
		if count == 0 {
			break
		}
		if _, ok := surface.NodeTypes[seq]; !ok {
			return false, errors.New("native surface names a missing event")
		}
		raw, found := messageBySeq[seq]
		if !found {
			continue // a selected zero-content node contributes no model image
		}
		if surface.NodeTypes[seq] != "user/message" && surface.NodeTypes[seq] != "tool/result" {
			continue
		}
		var message struct {
			Content []struct {
				Type      string `json:"type"`
				Offloaded bool   `json:"offloaded"`
			} `json:"content"`
		}
		if err := json.Unmarshal(raw, &message); err != nil {
			return false, err
		}
		imageIndex := 0
		target := imageOffloadTarget{Seq: seq}
		for _, block := range message.Content {
			if count == 0 {
				break
			}
			if block.Type != "image" {
				continue
			}
			if !block.Offloaded {
				target.ImageIndexes = append(target.ImageIndexes, imageIndex)
				count--
			}
			imageIndex++
		}
		if len(target.ImageIndexes) > 0 {
			targets = append(targets, target)
		}
	}
	if len(targets) == 0 {
		return false, nil
	}
	if _, err := a.append("image/offload", imageOffloadEvent{Targets: targets}, nil, nil); err != nil {
		return false, err
	}
	return true, nil
}
