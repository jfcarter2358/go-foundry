package editor

// editor_score_page.go — the Score tab's page view (View > Page View): the
// score laid out on paper as it will print, page under page, and the
// layout's dialogs — Page Setup (paper, margins, staff size, system gap,
// titles, names, house style, a part's own settings) and Export Selection
// as Image (PNG or SVG of the selected bars). The PDFs of the score and
// parts are the Export dialog's (editor_score_export.go).
//
// Each system on a page is the engraver's own display list, so everything
// the continuous view does on music works here through the same functions,
// given the system's geometry: hit tests, the note-input ghost, clicks that
// write or select, the selection, the caret, the play head. A strip along
// the top picks the full score or a part (each laid out with its own
// breaks, multi-bar rests and settings), shows the page count, and opens
// the setup. Ctrl+wheel zooms about the pointer; the play head is followed
// page by page.
//
// As in the continuous view one InvisibleButton covers only the visible
// pages area (not the strip or the scrollbars), so nothing drawn over the
// pages needs a click of its own.

import (
	"fmt"
	"math"
	"strings"

	"foundry/internal/imguibackend"
	"foundry/internal/score"
	"foundry/internal/score/engrave"
	"foundry/internal/score/scoredraw"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

const (
	scorePageStripH = float32(32)
	scorePageGap    = float32(28) // between pages, pixels
	scorePagePad    = float32(24) // around the pages
)

var (
	scoreColDesk   = scoreRGB(0x4a, 0x4e, 0x56, 1)
	scoreColShadow = scoreRGB(0, 0, 0, 0.35)
)

// scorePageGeom places the pages on screen.
type scorePageGeom struct {
	left   float32 // pages' left edge on screen
	top    float32 // the first page's top on screen
	zoom   float32
	pw, ph float32 // a page's size, pixels
	visMin imgui.Vec2
	visMax imgui.Vec2
}

// scorePageGeomAt is the pages' geometry in a pane at wp of a size,
// scrolled by sx, sy: the pages centred when they are narrower than it.
func scorePageGeomAt(b *engrave.Book, wp, size imgui.Vec2, sx, sy float32) scorePageGeom {
	z := scorePageZoom
	pw, ph := float32(b.Width)*z, float32(b.Height)*z
	visW := size.X - scoreScrollW
	left := wp.X + scorePagePad - sx
	if pw+2*scorePagePad < visW {
		left = wp.X + (visW-pw)/2
	}
	return scorePageGeom{left: left, top: wp.Y + scorePagePad - sy, zoom: z, pw: pw, ph: ph,
		visMin: wp, visMax: imgui.Vec2{X: wp.X + size.X - scoreScrollW, Y: wp.Y + size.Y - scoreScrollW}}
}

// pageOrigin is page i's top left on screen.
func (g scorePageGeom) pageOrigin(i int) imgui.Vec2 {
	return imgui.Vec2{X: g.left, Y: g.top + float32(i)*(g.ph+scorePageGap)}
}

// systemGeom is a system's view geometry: what the continuous view's
// functions take.
func (g scorePageGeom) systemGeom(i int, sys *engrave.System) scoreViewGeom {
	o := g.pageOrigin(i)
	return scoreViewGeom{origin: imgui.Vec2{X: o.X + float32(sys.At.X)*g.zoom, Y: o.Y + float32(sys.At.Y)*g.zoom}, zoom: g.zoom, visMin: g.visMin, visMax: g.visMax}
}

// view is the visible desk as a view geometry, for what is drawn over it
// rather than on a system (the pinned word field).
func (g scorePageGeom) view() scoreViewGeom {
	return scoreViewGeom{zoom: g.zoom, visMin: g.visMin, visMax: g.visMax}
}

// pageAt is the page under a screen point and the point on it, or -1.
func (g scorePageGeom) pageAt(b *engrave.Book, p imgui.Vec2) (int, engrave.Point) {
	if g.ph <= 0 {
		return -1, engrave.Point{}
	}
	i := int(math.Floor(float64((p.Y - g.top) / (g.ph + scorePageGap))))
	if i < 0 || i >= len(b.Pages) {
		return -1, engrave.Point{}
	}
	o := g.pageOrigin(i)
	q := engrave.Point{X: float64((p.X - o.X) / g.zoom), Y: float64((p.Y - o.Y) / g.zoom)}
	if q.X < 0 || q.Y < 0 || q.X > b.Width || q.Y > b.Height {
		return -1, engrave.Point{}
	}
	return i, q
}

// scorePageTarget is the system under the pointer and its geometry.
type scorePageTarget struct {
	page int
	sys  *engrave.System
	g    scoreViewGeom
	at   engrave.Point // in the system's list
}

func (g scorePageGeom) target(b *engrave.Book, p imgui.Vec2) (scorePageTarget, bool) {
	i, q := g.pageAt(b, p)
	if i < 0 {
		return scorePageTarget{}, false
	}
	sys := b.Pages[i].SystemAt(q)
	if sys == nil {
		return scorePageTarget{}, false
	}
	sg := g.systemGeom(i, sys)
	return scorePageTarget{page: i, sys: sys, g: sg, at: sg.toLayout(p)}, true
}

// scorePagePane draws the page view and takes the pointer.
func scorePagePane(pos, size imgui.Vec2) {
	e := scEd
	b := scorePageBook()
	io := imgui.CurrentIO()
	ctrl := io.KeyCtrlPressed()
	scorePageStrip(pos, size.X, b)
	pos.Y += scorePageStripH
	size.Y -= scorePageStripH

	z := scorePageZoom
	pw, ph := float32(b.Width)*z, float32(b.Height)*z
	contentW := pw + 2*scorePagePad
	contentH := 2*scorePagePad + float32(len(b.Pages))*(ph+scorePageGap) - scorePageGap
	imgui.SetNextWindowPos(pos)
	imgui.SetNextWindowSize(size)
	imgui.SetNextWindowContentSize(imgui.Vec2{X: contentW, Y: contentH})
	flags := trkPaneFlags | imgui.WindowFlagsHorizontalScrollbar
	if ctrl {
		flags |= imgui.WindowFlagsNoScrollWithMouse
	}
	imgui.PushStyleVarVec2(imgui.StyleVarWindowPadding, imgui.Vec2{})
	imgui.PushStyleColor(imgui.StyleColorWindowBg, imgui.Vec4{X: 0.29, Y: 0.306, Z: 0.337, W: 1})
	imgui.BeginV("##scorepages", nil, flags)
	imgui.PopStyleColor()
	imgui.PopStyleVar()

	wp := imgui.WindowPos()
	sx, sy := imgui.ScrollX(), imgui.ScrollY()
	scorePageScroll = imgui.Vec2{X: sx, Y: sy}
	g := scorePageGeomAt(b, wp, size, sx, sy)
	imgui.SetCursorPos(imgui.Vec2{X: sx, Y: sy})
	imgui.InvisibleButton("##scpagecanvas", imgui.Vec2{X: max(g.visMax.X-g.visMin.X, 1), Y: max(g.visMax.Y-g.visMin.Y, 1)})
	hovered := imgui.IsItemHovered()
	clicked := imgui.IsItemClicked()

	dl := imgui.WindowDrawList()
	dl.PushClipRect(g.visMin, g.visMax)
	dl.AddRectFilled(g.visMin, g.visMax, scoreColDesk)
	for i, pg := range b.Pages {
		o := g.pageOrigin(i)
		if o.Y > g.visMax.Y || o.Y+ph < g.visMin.Y {
			continue
		}
		dl.AddRectFilled(imgui.Vec2{X: o.X + 4, Y: o.Y + 5}, imgui.Vec2{X: o.X + pw + 4, Y: o.Y + ph + 5}, scoreColShadow)
		dl.AddRectFilled(o, imgui.Vec2{X: o.X + pw, Y: o.Y + ph}, scoreColPaper)
		pageG := scoreViewGeom{origin: o, zoom: z, visMin: g.visMin, visMax: g.visMax}
		for _, sys := range pg.Systems {
			sg := g.systemGeom(i, sys)
			scoreDrawSelection(dl, sys.List, sg)
			scoreRend.Draw(dl, sys.List, scoredraw.View{Origin: sg.origin, Scale: z, Clip: sg.clip(), Ink: scoreColInk, Colour: scoreItemColour, Faint: 0.3})
			scoreDrawCaret(dl, sys.List, sg)
			scoreDrawPlayhead(dl, sys.List, sg)
		}
		scoreRend.Draw(dl, &engrave.List{Items: pg.Items}, scoredraw.View{Origin: o, Scale: z, Clip: pageG.clip(), Ink: scoreColInk})
	}
	mouse := imgui.MousePos()
	var target scoreTarget
	targetOK := false
	pt, inSys := scorePageTarget{}, false
	if hovered {
		pt, inSys = g.target(b, mouse)
		if inSys {
			target, targetOK = scorePointerTarget(pt.sys.List, e.Score, e.Voice, e.Dur, pt.at)
			if e.Input && targetOK {
				scoreDrawGhost(dl, pt.sys.List, pt.g, target)
			}
		}
	}
	dl.PopClipRect()
	if hovered && e.Input && targetOK {
		scoreDrawGhostTip(dl, pt.g, target)
	}
	scorePageFollow(b, g)

	if _, wheel := io.MouseWheel(); hovered && ctrl && wheel != 0 {
		i, at := g.pageAt(b, mouse)
		scorePageZoom = clampF32(scorePageZoom*float32(math.Pow(1.15, float64(wheel))), 1.5, 30)
		if i >= 0 {
			nz := scorePageZoom
			nph := float32(b.Height) * nz
			y := scorePagePad + float32(i)*(nph+scorePageGap) + float32(at.Y)*nz
			imgui.SetScrollY(max(0, y-(mouse.Y-wp.Y)))
			imgui.SetScrollX(max(0, scorePagePad+float32(at.X)*nz-(mouse.X-wp.X)))
		}
	}
	if clicked && inSys {
		scoreClick(pt.sys.List, pt.at, target, targetOK, io)
	} else if clicked {
		e.Sel = scoreSel{}
	}
	if e.Words != nil {
		// The word field on the system its place is in, or at the foot
		// of the view while that is out of sight.
		drawn := false
	field:
		for i, pg := range b.Pages {
			for _, sys := range pg.Systems {
				if scoreDrawWordField(sys.List, g.systemGeom(i, sys)) {
					drawn = true
					break field
				}
			}
		}
		if !drawn {
			scoreDrawPinnedWordField(g.view())
		}
	}
	if e.Reveal {
		e.Reveal = false
		scorePageReveal(b, g, wp, size)
	}
	imgui.End()
}

// scorePageStrip is the strip over the pages: the part shown, the page
// count, the setup and the zoom.
func scorePageStrip(pos imgui.Vec2, w float32, b *engrave.Book) {
	e := scEd
	imgui.SetNextWindowPos(pos)
	imgui.SetNextWindowSize(imgui.Vec2{X: w, Y: scorePageStripH})
	imgui.PushStyleVarVec2(imgui.StyleVarWindowPadding, imgui.Vec2{X: 8, Y: 4})
	imgui.PushStyleColor(imgui.StyleColorWindowBg, imgui.Vec4{X: 0.098, Y: 0.106, Z: 0.125, W: 1})
	imgui.BeginV("##scorepagestrip", nil, trkPaneFlags|imgui.WindowFlagsNoScrollbar|imgui.WindowFlagsNoScrollWithMouse)
	imgui.PopStyleColor()
	imgui.PopStyleVar()
	part := e.pagePart()
	shown := "Full score"
	if part >= 0 {
		shown = "Part: " + e.lineName(part)
	}
	imgui.SetNextItemWidth(220)
	if imguibackend.BeginCombo("##scpagepart", shown) {
		if imguibackend.SelectableV("Full score##scpp-1", part < 0, 0, imgui.Vec2{}) {
			scorePagePart = -1
		}
		for i := range e.Score.Lines {
			if imguibackend.SelectableV(fmt.Sprintf("Part: %s##scpp%d", e.lineName(i), i), part == i, 0, imgui.Vec2{}) {
				scorePagePart = i
			}
		}
		imguibackend.EndCombo()
	}
	editorMenuTooltip("The full score, or one line's part: its own breaks, multi-bar rests and settings")
	imgui.SameLine()
	// The paper the pages are on: a part's own when it has one.
	paper := e.Score.Layout.Paper
	if part >= 0 {
		lay, _ := e.Score.Layout.ForPart(e.Score.Lines[part].Name)
		paper = lay.Paper
	}
	paperW, paperH := paper.PaperSize()
	editorTextMuted(fmt.Sprintf("%d page(s)  ·  %s %.0fx%.0f mm  ·  staff %.1f mm", len(b.Pages), scorePaperLabel(paper.Name), paperW, paperH, b.Space*4))
	if b.Reduced {
		imgui.SameLine()
		editorTextMuted("(reduced: a system is taller than the page)")
		editorMenuTooltip("A system of every staff does not fit the page at the Page Setup's staff size, so the pages are set at a smaller one. Choose a smaller staff size, larger paper or smaller margins to set it yourself.")
	}
	imgui.SameLine()
	if imgui.Button("Page Setup...##scpgsetup") {
		scoreRunCmd(scPageSetup)
	}
	editorMenuTooltip("Paper, margins, staff size, titles, names, breaks" + scoreKeysSuffix(scPageSetup))
	imgui.SameLine()
	if imgui.Button("Export PDF...##scpgpdf") {
		scoreRunCmd(scExportPDF)
	}
	editorMenuTooltip("The score and its parts as PDFs" + scoreKeysSuffix(scExportPDF))
	imgui.SameLine()
	if imgui.Button("-##scpgzo") {
		scorePageZoom = clampF32(scorePageZoom/1.25, 1.5, 30)
	}
	imgui.SameLine()
	editorTextMuted(fmt.Sprintf("%.0f%%", scorePageZoom/5*100))
	imgui.SameLine()
	if imgui.Button("+##scpgzi") {
		scorePageZoom = clampF32(scorePageZoom*1.25, 1.5, 30)
	}
	imgui.End()
}

// scorePaperLabel is a paper name as the UI shows it.
func scorePaperLabel(name string) string {
	switch name {
	case "", "letter":
		return "Letter"
	case "a4", "a3":
		return strings.ToUpper(name)
	case "custom":
		return "Custom"
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

// scorePageScrollTo scrolls so a system's top is in sight.
func scorePageScrollTo(b *engrave.Book, g scorePageGeom, pi int, sys *engrave.System, wp imgui.Vec2) {
	o := g.pageOrigin(pi)
	top := o.Y + float32(sys.At.Y+sys.List.Origin.Y-6)*g.zoom
	bottom := o.Y + float32(sys.At.Y+sys.List.Height)*g.zoom
	if top < g.visMin.Y || bottom > g.visMax.Y {
		imgui.SetScrollY(max(0, imgui.ScrollY()+top-(g.visMin.Y+20)))
	}
}

// scorePageReveal scrolls the selection's system into sight.
func scorePageReveal(b *engrave.Book, g scorePageGeom, wp, size imgui.Vec2) {
	a, _, ok := scEd.selBars()
	if !ok {
		return
	}
	pg, sys := b.Find(a)
	if sys == nil {
		return
	}
	scorePageScrollTo(b, g, pg.Number-1, sys, wp)
}

// scorePageScroll is the page view's scroll as last drawn (what following
// the play head moves; the tests read it).
var scorePageScroll imgui.Vec2

// scorePageFollow keeps the play head's system in sight while it plays.
func scorePageFollow(b *engrave.Book, g scorePageGeom) {
	if !scorePlaying() || !scEd.Transport.Follow {
		return
	}
	src := scoreHeadPerf()
	if src.perf == nil {
		return
	}
	bar, _, _ := scoreTickPos(src.perf, src.tick)
	pg, sys := b.Find(bar)
	if sys == nil {
		return
	}
	scorePageScrollTo(b, g, pg.Number-1, sys, g.visMin)
}

// ---------- page setup ----------

var scoreShowPageSetup bool

// scoreLayoutSlider is a layout slider: one undo step per drag (set
// changes the layout under its own key), a tenth of a unit apart.
func scoreLayoutSlider(label string, v float64, lo, hi float32, format string, set func(float64)) {
	f := float32(v)
	imgui.SetNextItemWidth(200)
	if imgui.SliderFloatV(label, &f, lo, hi, format, 0) {
		set(math.Round(float64(f)*10) / 10)
	}
	if imgui.IsItemDeactivated() {
		scEd.endLayoutGesture()
	}
}

// scorePageSetupDialog is Layout > Page Setup.
func scorePageSetupDialog() {
	imguibackend.WithModalPopup("Page Setup##scpagesetup", &scoreShowPageSetup, func() {
		e := scEd
		lay := &e.Score.Layout
		imgui.Text("Titles")
		for _, f := range []struct{ label, key string }{{"Title", "title"}, {"Subtitle", "subtitle"}, {"Composer", "composer"}} {
			v := map[string]string{"title": e.Score.Title, "subtitle": e.Score.Subtitle, "composer": e.Score.Composer}[f.key]
			imgui.SetNextItemWidth(320)
			if imgui.InputText(f.label+"##scps"+f.key, &v) {
				e.setText(f.key, v)
			}
			if imgui.IsItemDeactivated() {
				e.endLayoutGesture()
			}
		}
		tp := lay.TitlePage
		if imgui.Checkbox("Title page##scpstp", &tp) {
			e.setLayout("", func(l *score.Layout) { l.TitlePage = tp })
			e.endLayoutGesture()
		}
		editorMenuTooltip("The title, subtitle and composer on a page of their own before the music")
		imgui.SameLine()
		pn := !lay.NoPageNumbers
		if imgui.Checkbox("Page numbers##scpspn", &pn) {
			e.setLayout("", func(l *score.Layout) { l.NoPageNumbers = !pn })
			e.endLayoutGesture()
		}
		imgui.Separator()
		imgui.Text("Paper")
		cur := lay.Paper.Name
		if cur == "" {
			cur = score.DefaultPaper
		}
		imgui.SetNextItemWidth(160)
		if imguibackend.BeginCombo("Size##scpspaper", scorePaperLabel(cur)) {
			for _, n := range score.PaperNames {
				if imguibackend.SelectableV(scorePaperLabel(n)+"##scpsp"+n, n == cur, 0, imgui.Vec2{}) {
					e.setLayout("", func(l *score.Layout) {
						if n == "custom" && l.Paper.Width == 0 {
							l.Paper.Width, l.Paper.Height = l.Paper.PaperSize()
							if l.Paper.Landscape {
								l.Paper.Width, l.Paper.Height = l.Paper.Height, l.Paper.Width
							}
						}
						l.Paper.Name = n
					})
					e.endLayoutGesture()
				}
			}
			imguibackend.EndCombo()
		}
		imgui.SameLine()
		land := lay.Paper.Landscape
		if imgui.Checkbox("Landscape##scpsland", &land) {
			e.setLayout("", func(l *score.Layout) { l.Paper.Landscape = land })
			e.endLayoutGesture()
		}
		if lay.Paper.Name == "custom" {
			scoreLayoutSlider("Width (mm)##scpsw", lay.Paper.Width, 100, 600, "%.0f", func(v float64) {
				e.setLayout("pw", func(l *score.Layout) { l.Paper.Width = v })
			})
			scoreLayoutSlider("Height (mm)##scpsh", lay.Paper.Height, 100, 800, "%.0f", func(v float64) {
				e.setLayout("ph", func(l *score.Layout) { l.Paper.Height = v })
			})
		}
		m := lay.Paper.MarginsOrDefault()
		for i, side := range []string{"Top", "Bottom", "Left", "Right"} {
			v := []float64{m.Top, m.Bottom, m.Left, m.Right}[i]
			k := i
			scoreLayoutSlider(side+" margin (mm)##scpsm"+side, v, 0, 50, "%.1f", func(v float64) {
				e.setLayout("m"+side, func(l *score.Layout) {
					mm := l.Paper.MarginsOrDefault()
					switch k {
					case 0:
						mm.Top = v
					case 1:
						mm.Bottom = v
					case 2:
						mm.Left = v
					case 3:
						mm.Right = v
					}
					l.Paper.Margins = mm
				})
			})
		}
		imgui.Separator()
		imgui.Text("Staves")
		scoreLayoutSlider("Staff size (mm)##scpsstaff", lay.StaffOrDefault(), float32(score.MinStaff), float32(score.MaxStaff), "%.1f", func(v float64) {
			e.setLayout("staff", func(l *score.Layout) { l.Staff = v })
		})
		editorMenuTooltip("The staff's height, top line to bottom line (Sibelius's 7 mm by default)")
		scoreLayoutSlider("System gap (spaces)##scpsgap", lay.SystemGapOrDefault(), 4, 30, "%.1f", func(v float64) {
			e.setLayout("gap", func(l *score.Layout) { l.SystemGap = v })
		})
		editorMenuTooltip("The least room between one system's bottom line and the next one's top line")
		scoreLayoutSlider("Note spacing##scpsspacing", lay.SpacingOrDefault(), float32(score.MinSpacing), float32(score.MaxSpacing), "%.1f", func(v float64) {
			e.setLayout("spacing", func(l *score.Layout) { l.Spacing = v })
		})
		editorMenuTooltip("How much room each duration asks for: lower fits more bars on a system, higher fewer (1 is the default)")
		imgui.SetNextItemWidth(200)
		if imguibackend.BeginCombo("Instrument names##scpsnames", scoreNameModeLabels[lay.Names]) {
			for i, label := range scoreNameModeLabels {
				if imguibackend.SelectableV(label+fmt.Sprintf("##scpsnm%d", i), int(lay.Names) == i, 0, imgui.Vec2{}) {
					e.setLayout("", func(l *score.Layout) { l.Names = score.NameMode(i) })
					e.endLayoutGesture()
				}
			}
			imguibackend.EndCombo()
		}
		caut := !lay.NoCautionary
		if imgui.Checkbox("Cautionary clefs, keys and times at system ends##scpscaut", &caut) {
			e.setLayout("", func(l *score.Layout) { l.NoCautionary = !caut })
			e.endLayoutGesture()
		}
		court := lay.Courtesy
		if imgui.Checkbox("Courtesy accidentals in the bar after##scpscourt", &court) {
			e.setLayout("", func(l *score.Layout) { l.Courtesy = court })
			e.endLayoutGesture()
		}
		editorMenuTooltip("A note the bar before altered shows its accidental again, in parentheses")
		brk := !lay.NoBrackets
		if imgui.Checkbox("Family brackets##scpsbrk", &brk) {
			e.setLayout("", func(l *score.Layout) { l.NoBrackets = !brk })
			e.endLayoutGesture()
		}
		editorMenuTooltip("A bracket down each family of two lines or more (woodwinds, brass,\npercussion, voices, strings), its barlines run through")
		hide := lay.HideEmpty
		if imgui.Checkbox("Hide empty staves##scpshide", &hide) {
			e.setLayout("", func(l *score.Layout) { l.HideEmpty = hide })
			e.endLayoutGesture()
		}
		editorMenuTooltip("After the first system, a line with no notes in a system's bars is left out of it")
		imgui.Separator()
		imgui.Text("Short names")
		for i, l := range e.Score.Lines {
			v := l.Short
			imgui.SetNextItemWidth(140)
			if imgui.InputTextWithHint(fmt.Sprintf("%s##scpsshort%d", l.Name, i), score.AbbreviateName(l.Name), &v) {
				e.setShortName(i, v)
			}
			if imgui.IsItemDeactivated() {
				e.endLayoutGesture()
			}
		}
		if part := e.pagePart(); part >= 0 {
			imgui.Separator()
			name := e.Score.Lines[part].Name
			imgui.Text("The " + name + " part")
			pl := lay.Part(name)
			var p score.PartLayout
			if pl != nil {
				p = *pl
			}
			staff := p.Staff
			if staff == 0 {
				staff = lay.StaffOrDefault()
			}
			scoreLayoutSlider("Part staff size (mm)##scppstaff", staff, float32(score.MinStaff), float32(score.MaxStaff), "%.1f", func(v float64) {
				e.setPart(part, "staff", func(p *score.PartLayout) { p.Staff = v })
			})
			gap := p.SystemGap
			if gap == 0 {
				gap = lay.SystemGapOrDefault()
			}
			scoreLayoutSlider("Part system gap (spaces)##scppgap", gap, 4, 30, "%.1f", func(v float64) {
				e.setPart(part, "gap", func(p *score.PartLayout) { p.SystemGap = v })
			})
			// The part's own paper (named sizes; a custom size is the
			// file's), or the score's.
			pcur := "As the score"
			if p.Paper.Name != "" {
				pcur = scorePaperLabel(p.Paper.Name)
			}
			imgui.SetNextItemWidth(160)
			if imguibackend.BeginCombo("Part paper##scpppaper", pcur) {
				if imguibackend.SelectableV("As the score##scppp-", p.Paper.Name == "", 0, imgui.Vec2{}) {
					e.setPart(part, "", func(p *score.PartLayout) { p.Paper = score.Paper{} })
					e.endLayoutGesture()
				}
				for _, n := range score.PaperNames[:len(score.PaperNames)-1] {
					if imguibackend.SelectableV(scorePaperLabel(n)+"##scppp"+n, n == p.Paper.Name, 0, imgui.Vec2{}) {
						e.setPart(part, "", func(p *score.PartLayout) {
							p.Paper = score.Paper{Name: n, Landscape: p.Paper.Landscape, Margins: p.Paper.Margins}
						})
						e.endLayoutGesture()
					}
				}
				imguibackend.EndCombo()
			}
			if p.Paper.Name != "" {
				imgui.SameLine()
				pland := p.Paper.Landscape
				if imgui.Checkbox("Landscape##scppland", &pland) {
					e.setPart(part, "", func(p *score.PartLayout) { p.Paper.Landscape = pland })
					e.endLayoutGesture()
				}
			}
			multi := !p.NoMultiRests
			if imgui.Checkbox("Multi-bar rests##scppmulti", &multi) {
				e.setPart(part, "multi", func(p *score.PartLayout) { p.NoMultiRests = !multi })
				e.endLayoutGesture()
			}
			editorTextMuted(fmt.Sprintf("%d break(s) of its own; the score's breaks do not apply to parts.", len(p.Breaks)))
		}
		imgui.Separator()
		editorTextMuted("Breaks: select a bar, then " + scoreKeymapActive().label(scSystemBreak) + " (system) or " +
			scoreKeymapActive().label(scPageBreak) + " (page) breaks after it.")
		if imgui.Button("Done##scpsdone") {
			e.endLayoutGesture()
			imgui.CloseCurrentPopup()
		}
	})
}

// scoreNameModeLabels are score.NameModes as the dialog says them.
var scoreNameModeLabels = []string{"Full, then short", "Full on every system", "Short on every system", "None"}

// ---------- exporting an image of the selection ----------

var (
	scoreShowImage  bool
	scoreImageSVG   bool
	scoreImageScale = float32(12)
	scoreImageFile  string
)

// scoreOpenImage opens the image dialog for the selected bars.
func scoreOpenImage() {
	if _, _, _, err := scEd.imageSpan(); err != nil {
		scEd.Status = scoreErrText(err)
		return
	}
	scoreImageFile = scoreImagePath(scoreImageSVG)
	scoreShowImage = true
}

// scoreImageDialog is File > Export Selection as Image.
func scoreImageDialog() {
	imguibackend.WithModalPopup("Export Selection as Image##scimg", &scoreShowImage, func() {
		e := scEd
		from, to, lines, err := e.imageSpan()
		if err != nil {
			imgui.Text(scoreErrText(err))
		} else {
			editorTextMuted(fmt.Sprintf("Bars %d-%d of %d line(s), as the continuous view engraves them", from, to, len(lines)))
		}
		for i, label := range []string{"PNG", "SVG (vector)"} {
			if i > 0 {
				imgui.SameLine()
			}
			if imgui.RadioButton(label+"##scimgfmt", scoreImageSVG == (i == 1)) && scoreImageSVG != (i == 1) {
				scoreImageSVG = i == 1
				scoreImageFile = scoreImagePath(scoreImageSVG)
			}
		}
		imgui.SetNextItemWidth(200)
		imgui.SliderFloatV("Pixels a staff space##scimgscale", &scoreImageScale, 4, 60, "%.0f", 0)
		imgui.SetNextItemWidth(520)
		imgui.InputText("Path##scimgpath", &scoreImageFile)
		if imgui.Button("Export##scimggo") && err == nil {
			if err := e.exportImage(scoreImageFile, float64(scoreImageScale)); err != nil {
				e.Status = "Export failed: " + err.Error()
			}
			imgui.CloseCurrentPopup()
		}
		imgui.SameLine()
		if imgui.Button("Cancel##scimgcancel") {
			imgui.CloseCurrentPopup()
		}
	})
}
