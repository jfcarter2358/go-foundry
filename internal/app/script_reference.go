// Development-build only: the language reference is drawn with imgui into the
// F1 overlay, and a release build has neither. See devtools_dev.go.

//go:build !release

package app

// script_reference.go — the language reference tabs of the F1 overlay.
//
// What a script can say used to be shown in the script editor, folded into the
// strip below the diagnostics pane. That is the wrong place twice over: the
// pane under the buffer is where you look when something is *wrong*, and the
// strip was never tall enough to say more than a comma-separated list of names
// — which answers "does `clamp` exist" and nothing else.
//
// F1 already means "what can I do here" in every window of this application,
// and the overlay is most of the screen. So the reference lives there, in full,
// beside the keybindings; the editor keeps a one-line pointer to it.
//
// Like keybindings.go, this file is the place the tables are *described from*,
// not where they are kept: the builtins come from flang, the events and the
// properties from internal/script. Each has a test in its own package that
// fails when the language grows something this cannot show.

import (
	"fmt"
	"strings"

	"foundry/internal/flang"
	"foundry/internal/script"
	"foundry/internal/uiwin"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

func init() {
	uiwin.HelpPages = []uiwin.HelpPage{
		{Title: "Events", Draw: drawEventReference},
		{Title: "Standard library", Draw: drawStdlibReference},
		{Title: "Properties", Draw: drawPropertyReference},
	}
}

// Reference palette. The same amber the script editor marks issues in, and the
// same blue its keyword class uses, so a name reads the same colour in the
// reference as it does in the buffer.
var (
	refNameColor  = imgui.Vec4{X: 1.00, Y: 0.80, Z: 0.42, W: 1}
	refTypeColor  = imgui.Vec4{X: 0.51, Y: 0.67, Z: 1.00, W: 1}
	refAtomColor  = imgui.Vec4{X: 0.94, Y: 0.44, Z: 0.47, W: 1}
	refMutedColor = imgui.Vec4{X: 0.62, Y: 0.66, Z: 0.72, W: 1}
	refHeadColor  = imgui.Vec4{X: 0.45, Y: 0.80, Z: 1.00, W: 1}
)

// refTableFlags is the shape every table on these pages takes: ruled rows, a
// resizable first column, and the body scrolling under a header that stays.
const refTableFlags = imgui.TableFlagsBorders | imgui.TableFlagsRowBg |
	imgui.TableFlagsResizable | imgui.TableFlagsSizingStretchProp

// refText draws a coloured cell.
func refText(col imgui.Vec4, s string) {
	imgui.PushStyleColor(imgui.StyleColorText, col)
	imgui.Text(s)
	imgui.PopStyleColor()
}

// refWrapped draws a cell that wraps rather than clipping — the "what it does"
// column, which is the one that needs the room.
func refWrapped(s string) {
	imgui.PushTextWrapPos()
	imgui.Text(s)
	imgui.PopTextWrapPos()
}

// refHeading draws a section title inside a page.
func refHeading(s string) {
	imgui.Spacing()
	refText(refHeadColor, s)
	imgui.Separator()
}

// ---------- events ----------

// drawEventReference lists what the engine posts without being asked.
func drawEventReference() {
	refWrapped("The engine posts these on its own. A clause matches one by naming its atom " +
		"as the handler's first parameter; the rest of the parameters are the arguments below.")
	imgui.Spacing()
	refText(refMutedColor, "def on_tick(:tick:, dt float) -> null")
	imgui.Spacing()

	if !imgui.BeginTableV("##eventtable", 4, refTableFlags, imgui.Vec2{}, 0) {
		return
	}
	imgui.TableSetupColumnV("Atom", imgui.TableColumnFlagsWidthStretch, 0.9, 0)
	imgui.TableSetupColumnV("Posted to", imgui.TableColumnFlagsWidthStretch, 1.1, 0)
	imgui.TableSetupColumnV("Arguments", imgui.TableColumnFlagsWidthStretch, 1.4, 0)
	imgui.TableSetupColumnV("When", imgui.TableColumnFlagsWidthStretch, 3.0, 0)
	imgui.TableHeadersRow()

	for _, e := range script.EventDocs() {
		imgui.TableNextRow()
		imgui.TableSetColumnIndex(0)
		refText(refAtomColor, e.Atom)
		imgui.TableSetColumnIndex(1)
		refText(refMutedColor, e.PostedTo)
		imgui.TableSetColumnIndex(2)
		refText(refTypeColor, e.Args)
		imgui.TableSetColumnIndex(3)
		refWrapped(e.When)
	}
	imgui.EndTable()

	refHeading("Custom messages")
	refWrapped("Atoms of your own need no declaration. `#breakable <- (:damage:, @self, 5)` reaches " +
		"every object carrying that tag; one with no clause for `:damage:` ignores it, as does one " +
		"whose clauses are all guarded and every guard declines.")
	imgui.Spacing()
	refText(refMutedColor, "@self <- after(500, :open_done:)         -- to yourself, 500ms of play time from now")
	refText(refMutedColor, "@door <- after(1000, :damage:, @self, 5) -- to someone else, with arguments")
	refText(refMutedColor, "#lamps <- after(250, :flicker:)          -- to every object carrying the tag")
}

// ---------- standard library ----------

// drawStdlibReference lists every library function, by group.
func drawStdlibReference() {
	refWrapped("Every function a script can call without defining it. A script's own definitions " +
		"shadow these, so a game that wants its own `distance` is not fighting the engine for the name.")

	for _, group := range flang.BuiltinGroups() {
		docs := flang.BuiltinDocsIn(group)
		if len(docs) == 0 {
			continue
		}
		refHeading(group)
		if !imgui.BeginTableV("##stdlib_"+group, 3, refTableFlags, imgui.Vec2{}, 0) {
			continue
		}
		imgui.TableSetupColumnV("Call", imgui.TableColumnFlagsWidthStretch, 2.0, 0)
		imgui.TableSetupColumnV("Gives", imgui.TableColumnFlagsWidthStretch, 0.7, 0)
		imgui.TableSetupColumnV("What it does", imgui.TableColumnFlagsWidthStretch, 3.6, 0)
		imgui.TableHeadersRow()
		for _, d := range docs {
			imgui.TableNextRow()
			imgui.TableSetColumnIndex(0)
			refText(refNameColor, d.Sig)
			imgui.TableSetColumnIndex(1)
			refText(refTypeColor, d.Ret)
			imgui.TableSetColumnIndex(2)
			refWrapped(d.Summary)
		}
		imgui.EndTable()
	}

	refHeading("hitscan options")
	refWrapped("`hitscan`'s optional second argument is a dict of these. An unknown key is a typo " +
		"rather than something to ignore, so it is reported with a suggestion — before the script " +
		"runs when the dict is written out literally.")
	if imgui.BeginTableV("##hitscan_opts", 2, refTableFlags, imgui.Vec2{}, 0) {
		imgui.TableSetupColumnV("Option", imgui.TableColumnFlagsWidthStretch, 1.0, 0)
		imgui.TableSetupColumnV("What it does", imgui.TableColumnFlagsWidthStretch, 3.6, 0)
		imgui.TableHeadersRow()
		for _, name := range script.HitscanOptionNames() {
			imgui.TableNextRow()
			imgui.TableSetColumnIndex(0)
			refText(refNameColor, name)
			imgui.TableSetColumnIndex(1)
			refWrapped(script.HitscanOptionSummary(name))
		}
		imgui.EndTable()
	}

	refHeading("Not functions")
	refWrapped("`import(\"shared/helpers.flang\")` and `param name type` are statements the parser " +
		"reads at the top of a file, not calls. A declared param is read back as `?name` — `@name` " +
		"is an object in the level, and the two are separate namespaces. See SCRIPTING.md.")
}

// ---------- properties ----------

// refKindOrder is the order the reference lists object kinds — entities first,
// since that is what most scripts address, then the parts of a sector.
var refKindOrder = []struct {
	Label string
	Kind  flang.Kind_
}{
	{"level", flang.RefLevel},
	{"entity", flang.RefEntity},
	{"model", flang.RefModel},
	{"volume", flang.RefVolume},
	// Sprite, emitter and sound were absent here, so their property tables
	// never reached the F1 reference even though PropertyDocs returns them —
	// a feature nobody could find. Fixed alongside adding sounds rather than
	// after, since the omission is what would have hidden the new kind too.
	// There is now a test that will not let it happen again:
	// TestReferenceListsEveryPropertyKind.
	{"sprite", flang.RefSprite},
	{"emitter", flang.RefEmitter},
	{"sound", flang.RefSound},
	{"brush", flang.RefBrush},
	{"chunk", flang.RefChunk},
	{"light", flang.RefLight},
	// TODO(brush migration, Phase 5): "biome" belongs here and is missing,
	// because biome scatter is being rebuilt as a Volume and has no property
	// table to point at yet. TestReferenceKindsAllHaveProperties fails on a
	// label with nothing behind it, so the row goes back when the properties
	// do.
	{"screen", flang.RefScreen},
	{"widget", flang.RefWidget},
}

// drawPropertyReference lists what `.prop` reaches, per object kind.
func drawPropertyReference() {
	refWrapped("Read and written as `@id.prop.name`, or across a whole tag with `#tag.prop.name = v`. " +
		"A read-only property is reported rather than obeyed when a script assigns to it.")

	for _, k := range refKindOrder {
		docs := script.PropertyDocs(k.Kind)
		if len(docs) == 0 {
			continue
		}
		refHeading(fmt.Sprintf("%s  (%d)", k.Label, len(docs)))
		if !imgui.BeginTableV("##props_"+k.Label, 3, refTableFlags, imgui.Vec2{}, 0) {
			continue
		}
		imgui.TableSetupColumnV("Name", imgui.TableColumnFlagsWidthStretch, 1.4, 0)
		imgui.TableSetupColumnV("Access", imgui.TableColumnFlagsWidthStretch, 0.7, 0)
		imgui.TableSetupColumnV("What it is", imgui.TableColumnFlagsWidthStretch, 4.0, 0)
		imgui.TableHeadersRow()
		for _, d := range docs {
			imgui.TableNextRow()
			imgui.TableSetColumnIndex(0)
			refText(refNameColor, d.Name)
			imgui.TableSetColumnIndex(1)
			access, col := "read-only", refMutedColor
			if d.Writable {
				access, col = "read/write", refTypeColor
			}
			refText(col, access)
			imgui.TableSetColumnIndex(2)
			refWrapped(strings.TrimSpace(d.Doc))
		}
		imgui.EndTable()
	}
}
