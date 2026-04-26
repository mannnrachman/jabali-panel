package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// system.list_nspawn_images — return the names of every immutable image
// directory under /var/lib/jabali-nspawn/images. Used by the admin UI's
// image-pin dropdown and the bulk-upgrade flow.

const nspawnImagesDir = "/var/lib/jabali-nspawn/images"

type systemListNspawnImagesResponse struct {
	Images []nspawnImageEntry `json:"images"`
}

type nspawnImageEntry struct {
	Name     string `json:"name"`
	Manifest string `json:"manifest,omitempty"` // raw JSON manifest if present
}

func systemListNspawnImagesHandler(ctx context.Context, params json.RawMessage) (any, error) {
	entries, err := os.ReadDir(nspawnImagesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return &systemListNspawnImagesResponse{Images: []nspawnImageEntry{}}, nil
		}
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("readdir %s: %v", nspawnImagesDir, err),
		}
	}
	out := make([]nspawnImageEntry, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if !nspawnImageRe.MatchString(e.Name()) {
			continue
		}
		entry := nspawnImageEntry{Name: e.Name()}
		manifest, mErr := os.ReadFile(filepath.Join(nspawnImagesDir, e.Name(), "MANIFEST.json"))
		if mErr == nil {
			entry.Manifest = string(manifest)
		}
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return &systemListNspawnImagesResponse{Images: out}, nil
}

func init() {
	Default.Register("system.list_nspawn_images", systemListNspawnImagesHandler)
}
