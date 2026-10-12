package editor

// editor_polyhaven_stage.go — the Poly Haven browser and the Generator
// editor, joined (docs/canyon/PHASE_4.md):
//
//   - Add to stage appends an asset ID to a stage's asset list (a list
//     param with procgen.ParamDef.AssetList set): the list whose Browse…
//     opened the browser, else the selected stage's first list of the
//     asset's type. One undo step in the Generator.
//   - genAssetListHeader is the id-list widget's extras in the param
//     panel: each item's thumbnail beside its ID, and the Browse… button.

import (
	"fmt"
	"strings"

	"foundry/internal/polyhaven"
	"foundry/internal/procgen"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// phTarget is the Generator list Add to stage writes to, or ok false.
func phTarget(typ string) (s *procgen.Stage, k *procgen.StageKind, path genParamPath, def *procgen.ParamDef, ok bool) {
	if genDoc == nil {
		return nil, nil, nil, nil, false
	}
	if phUI.targetStage != "" {
		if s = genDoc.StageByID(phUI.targetStage); s != nil {
			if k = procgen.KindByName(s.Kind); k != nil {
				if def = genDefAt(k.Params, phUI.targetPath); def != nil && def.Type == procgen.ParamList && def.AssetList == typ {
					return s, k, phUI.targetPath, def, true
				}
			}
		}
	}
	if !GeneratorEditorOpen() {
		return nil, nil, nil, nil, false
	}
	if s = genSelectedStage(); s == nil {
		return nil, nil, nil, nil, false
	}
	if k = procgen.KindByName(s.Kind); k == nil {
		return nil, nil, nil, nil, false
	}
	for i := range k.Params {
		if d := &k.Params[i]; d.Type == procgen.ParamList && d.AssetList == typ {
			return s, k, genParamPath{{Name: d.Name, Item: -1}}, d, true
		}
	}
	return nil, nil, nil, nil, false
}

func phAssetType(a *polyhaven.Asset) string {
	if a.IsTexture() {
		return polyhaven.TypeTextures
	}
	return polyhaven.TypeModels
}

// phTargetLabel names where Add to stage would write ("" for nowhere).
func phTargetLabel(a *polyhaven.Asset) string {
	s, _, _, def, ok := phTarget(phAssetType(a))
	if !ok {
		return ""
	}
	return fmt.Sprintf("%s's %s", s.ID, def.DisplayLabel())
}

// phAddToStage appends a's ID to the target list.
func phAddToStage(a *polyhaven.Asset) error {
	s, k, path, def, ok := phTarget(phAssetType(a))
	if !ok {
		return fmt.Errorf("no Poly Haven stage to add to")
	}
	return genAppendAsset(s, k, path, def, a.ID)
}

// genAppendAsset appends an item naming id to the list at path (refused if
// it is there already). A texture layer's name is the ID unless taken.
func genAppendAsset(s *procgen.Stage, k *procgen.StageKind, path genParamPath, def *procgen.ParamDef, id string) error {
	if s.Params == nil {
		s.Params = procgen.Params{}
	}
	cur, _ := genValueAt(k.Params, s.Params, path)
	names := map[string]bool{}
	for _, it := range cur.Items {
		if v, ok := it["id"]; ok && v.Str == id {
			return fmt.Errorf("%s is already in %s", id, s.ID)
		}
		if v, ok := it["name"]; ok {
			names[v.Str] = true
		}
	}
	item := def.NewItem()
	item["id"] = procgen.String(id)
	if _, ok := item["name"]; ok {
		n := id
		for i := 2; names[n]; i++ {
			n = fmt.Sprintf("%s_%d", id, i)
		}
		item["name"] = procgen.String(n)
	}
	v := cur.Clone()
	v.Items = append(v.Items, item)
	GeneratorPushUndo()
	genSetValueAt(k.Params, s.Params, path, v)
	genAfterEdit()
	return nil
}

// genAssetItemThumb draws an asset item's thumbnail (small, before its
// header) when the list is an asset list. Returns the label to use.
func genAssetItemThumb(def *procgen.ParamDef, item procgen.Params, i int) string {
	label := fmt.Sprintf("Item %d", i+1)
	if def.AssetList == "" {
		return label
	}
	id := ""
	if v, ok := item["id"]; ok {
		id = strings.TrimSpace(v.Str)
	}
	if id == "" {
		return label + " (no ID)"
	}
	if tex := phThumb(id); tex != 0 {
		imgui.Image(imgui.TextureID(tex), imgui.Vec2{X: 40, Y: 40})
	} else {
		imgui.Dummy(imgui.Vec2{X: 40, Y: 40})
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip(id + "\n" + polyhaven.Credit)
	}
	imgui.SameLine()
	if n, ok := item["name"]; ok && n.Str != "" && n.Str != id {
		return n.Str + ": " + id
	}
	return id
}

// genAssetListBrowse is the Browse… button after an asset list's items.
func genAssetListBrowse(s *procgen.Stage, path genParamPath, def *procgen.ParamDef) {
	if def.AssetList == "" {
		return
	}
	imgui.SameLine()
	if imgui.Button("Browse...##phbrowse") {
		OpenPolyHavenBrowserFor(def.AssetList, s.ID, path)
	}
	editorMenuTooltip("Open the Poly Haven browser on " + def.AssetList + "; its Add to stage appends here.\n" + polyhaven.Credit)
}
