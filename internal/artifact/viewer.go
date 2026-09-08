package artifact

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
)

// Reinterpret keeps every data/context/asset byte from the selected checkpoint
// and replaces only executable viewer files. The caller authorizes both source
// revisions in the same artifact. This does not commit or change either one.
func Reinterpret(data, renderer Manifest, sourceRevision, rendererRevision string) (Manifest, error) {
	if err := data.Validate(); err != nil {
		return Manifest{}, err
	}
	if err := renderer.Validate(); err != nil {
		return Manifest{}, err
	}
	format, _ := data.Dataset["format"].(string)
	if format == "" || !reflect.DeepEqual(data.Dataset["format"], renderer.Dataset["format"]) || !reflect.DeepEqual(data.Dataset["schema"], renderer.Dataset["schema"]) {
		return Manifest{}, fmt.Errorf("the viewer must declare the same dataset format and schema")
	}
	// Deep copy metadata so the immutable source objects remain untouched.
	raw, _ := json.Marshal(data)
	var out Manifest
	if err := json.Unmarshal(raw, &out); err != nil {
		return Manifest{}, err
	}
	out.Files = []File{}
	for _, file := range data.Files {
		if file.Role != "viewer" {
			out.Files = append(out.Files, file)
		}
	}
	for _, file := range renderer.Files {
		if file.Role == "viewer" {
			out.Files = append(out.Files, file)
		}
	}
	sort.Slice(out.Files, func(i, j int) bool { return out.Files[i].Path < out.Files[j].Path })
	out.Entrypoint = renderer.Entrypoint
	// Reinterpreted websites have no settings authority. Current editing must
	// reopen the integration's original, currently bound settings surface.
	out.SettingsEntrypoint = ""
	out.Viewer = map[string]any{"source_revision_id": sourceRevision, "renderer_revision_id": rendererRevision, "producer": renderer.Producer, "declared_viewer": renderer.Viewer}
	if err := out.Validate(); err != nil {
		return Manifest{}, fmt.Errorf("viewer and data files cannot be combined: %w", err)
	}
	return out, nil
}
