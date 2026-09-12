package editor

// editor_cloud_panel.go — the controls for what a cloud looks like, shared by a
// volume's Cloud group and the sky's cloud deck.
//
// One function because world.CloudLook is one struct embedded in both, and an
// author who has tuned a cumulus should find the deck's knobs in the same order
// with the same names. What differs — a volume's softness, the deck's altitude
// — is drawn by each caller around this.

import (
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

// editorCloudLook draws l's controls. id keeps the imgui labels of the two
// callers apart.
func editorCloudLook(id string, l *world.CloudLook) {
	editorSkyColor("Colour##"+id+"cloudcol", &l.Color)

	editorDragFloatStep("Coverage##"+id+"cloudcov", &l.Coverage, 0, 1, "%.2f", 0.01)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("How much of the region is cloud. Low is scattered puffs\nwith sky between them; high is an overcast mass.")
	}
	editorDragFloatStep("Density##"+id+"clouddens", &l.Density, 0, world.CloudMaxDensity, "%.3f", 0.005)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Thickness per world unit at the cloud's densest, the unit a\nfog volume's density is in.")
	}
	editorDragFloatStep("Scale##"+id+"cloudscale", &l.Scale, world.CloudMinScale, 5000, "%.1f", 0.5)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("World units across the largest lumps.")
	}
	editorDragFloatStep("Detail##"+id+"clouddetail", &l.Detail, world.CloudMinScale, 2000, "%.1f", 0.25)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("World units across the small noise that breaks the edges up.")
	}
	editorDragFloatStep("Erosion##"+id+"clouderode", &l.Erosion, 0, 1, "%.2f", 0.01)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("How hard that detail eats the edges. 0 is soft cotton; 1 is ragged.")
	}

	wind := [3]float32{l.Wind.X(), l.Wind.Y(), l.Wind.Z()}
	if editorDragFloat3("Wind##"+id+"cloudwind", &wind, -world.CloudMaxWind, world.CloudMaxWind, "%.2f") {
		l.Wind = mgl32.Vec3{wind[0], wind[1], wind[2]}
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("World units per second the pattern drifts through the cloud.\nIt moves the cloud's shape, not the region it is in.")
	}

	editorDragFloatStep("Absorption##"+id+"cloudabs", &l.Absorption, 0, world.CloudMaxAbsorption, "%.2f", 0.05)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("How much sunlight the cloud's own mass takes out, which is what\nshades its underside. Higher is a heavier, darker cloud.")
	}
	editorDragFloatStep("Ambient##"+id+"cloudamb", &l.Ambient, 0, world.CloudMaxAmbient, "%.2f", 0.05)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("How much sky light fills the shadowed side.")
	}

	cast := l.CastShadows
	if imgui.Checkbox("Casts shadows##"+id+"cloudcast", &cast) {
		EditorPushUndo()
		l.CastShadows = cast
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Darken the ground, scatter and sprites under the cloud, from the\nlevel's first directional light.")
	}
}
