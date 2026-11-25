package subagent

import (
	"context"
	"encoding/json"
	"errors"

	"go-bot/harness/session"
)

// RecordNativeChild publishes one direct-child discovery fact in its parent
// v4 Session after the child has accepted its first task. The child's own
// journal is a sibling Session directory under the same initial project cwd.
func RecordNativeChild(ctx context.Context, parent *sessionformat.LiveSession, child sessionformat.Header, label string) (sessionformat.SubagentCatalogEntry, error) {
	if parent == nil || label == "" {
		return sessionformat.SubagentCatalogEntry{}, errors.New("native child catalog requires a parent and label")
	}
	if err := child.Validate(); err != nil {
		return sessionformat.SubagentCatalogEntry{}, err
	}
	root := parent.Header()
	if child.ID == "" || child.ID == root.ID || child.ParentSession != root.ID || child.Origin != "subagent" ||
		child.DelegationDepth != root.DelegationDepth+1 {
		return sessionformat.SubagentCatalogEntry{}, errors.New("native child lineage differs from its direct parent")
	}
	children, err := parent.Children(sessionformat.LiveCut)
	if err != nil {
		return sessionformat.SubagentCatalogEntry{}, err
	}
	for _, existing := range children {
		if existing.ChildID == child.ID {
			return sessionformat.SubagentCatalogEntry{}, errors.New("native child is already cataloged")
		}
	}
	entry := sessionformat.SubagentCatalogEntry{
		Version: 0, ChildID: child.ID, ChildCreatedAt: uint64(child.CreatedAt),
		Mode: "continuable", Label: &label,
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return sessionformat.SubagentCatalogEntry{}, err
	}
	if _, err := parent.Append(sessionformat.EventInput{Type: "subagent/catalog", Data: data}); err != nil {
		return sessionformat.SubagentCatalogEntry{}, err
	}
	if err := parent.Flush(ctx); err != nil {
		return sessionformat.SubagentCatalogEntry{}, err
	}
	return entry, nil
}
