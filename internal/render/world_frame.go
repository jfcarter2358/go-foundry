package render

// world_frame.go — the drawing half of one frame: everything from the world's
// per-frame uniforms to the last sprite over the last cloud.
//
// It used to live inline in app.Run, which was fine for as long as the app was
// the only thing that drew. It is not: `foundry render world` and a headless
// play session (internal/harness) need the picture the game would have drawn,
// and the moment a second caller copied this loop out it would start drifting
// from it — the passes are in the order they are for reasons spelled out pass
// by pass below, and every reordering of them has been a bug. This is the same
// split internal/sim is on the simulation side: one copy, two callers.
//
// WHAT IS HERE is the 3D world: the shadow passes, the opaque world, the
// entities and scenery, the editor's own markers, the scatter, the decals, the
// sky, the sorted transparent pass, the volumetrics, the sprites and the
// first-person viewmodel.
//
// WHAT IS NOT is anything with a window in it: the clock, the input, the swap,
// the drains (glyph pages, texture edits, deferred script work), the runtime
// UI and imgui. Those stay in internal/app, and a headless caller does the two
// or three of them it needs for itself.

import (
	"log"
	"sort"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// TransparentDraw holds a deferred draw call for a transparent geometry object.
// Collected during the opaque pass and drawn back-to-front afterward.
type TransparentDraw struct {
	Geom *gfx.Geometry
	// Batch, when set, is drawn instead of Geom: a batched brush's ranges of
	// one chunk batch (render/chunk_batch.go), which carries its own model.
	Batch       *BatchDraw
	Model       mgl32.Mat4
	Distance    float32
	DoubleSided bool
}

// draw issues the deferred draw.
func (td *TransparentDraw) draw() {
	switch {
	case td.Batch != nil:
		td.Batch.Draw()
	case td.Model == mgl32.Ident4():
		td.Geom.Draw()
	default:
		td.Geom.DrawWithTransform(td.Model)
	}
}

// WorldDraw is what a frame's draw needs to know that is not in the level: the
// clock the shaders animate against, and what the caller is — a game, the
// editor, a benchmark or a headless shot.
//
// Everything has a working zero value except Now, which is a game being drawn
// at its first instant: no editor, no wireframe, every pass drawn.
type WorldDraw struct {
	// Now is the frame's clock in seconds. The water, the material scroll, the
	// biome wind and the clouds are all integrated against it, and all four
	// must be latched from the same value or a shadow slides off the thing
	// casting it.
	Now float64
	// Editor is the editor owning the view. It adds the authoring-only
	// drawings (volume cages, the spawn marker, markers for objects with no
	// mesh) and takes away the viewmodel: what is on screen in the editor is
	// the level, not a game being played from inside a body with hands.
	Editor bool
	// Wireframe is the 3D editor's wireframe shading. The filled passes
	// (scatter, decals, sky, volumetrics, sprites) are skipped rather than
	// outlined — a wireframe field of grass is a solid mask, which is the
	// thing the mode exists to get rid of.
	Wireframe bool
	// Skip reports a pass a benchmark was asked to leave out, by the names
	// ProfileBegin uses. nil draws everything.
	Skip func(pass string) bool
	// Target is the framebuffer and viewport the frame draws into. Declared,
	// the passes read it (and the GL state convention in frame_target.go)
	// instead of asking GL, which stalls the CPU on the GPU every time; nil,
	// they ask.
	Target *FrameTarget
	// Markers and BrushGhost draw the editor's own overlays, and are the
	// editor reaching into the frame rather than the other way round: this
	// package is compiled into a release build, which has no editor at all.
	// Both are ignored unless Editor is set.
	Markers    func(lvl *world.Level, cull *Frustum)
	BrushGhost func(draw func(g *gfx.Geometry, model mgl32.Mat4, doubleSided bool))
}

func (o WorldDraw) skip(pass string) bool {
	return o.Skip != nil && o.Skip(pass)
}

func (o WorldDraw) markers(lvl *world.Level, cull *Frustum) {
	if o.Markers != nil {
		o.Markers(lvl, cull)
	}
}

func (o WorldDraw) brushGhost(draw func(g *gfx.Geometry, model mgl32.Mat4, doubleSided bool)) {
	if o.BrushGhost != nil {
		o.BrushGhost(draw)
	}
}

// DrawWorld draws the level, from the frame's uniforms to the viewmodel.
//
// The caller has placed the camera (gfx.Cam) and set the projection before
// this; nothing here moves either, because a frame that drew from a camera it
// had adjusted itself would be a frame nothing else could reproduce.
func DrawWorld(lvl *world.Level, o WorldDraw) {
	if ForceWireframe {
		o.Wireframe = true
	}
	// The editor's view draws chunk brushes the CPU way: a brush edit
	// rebuilds a group every frame of a drag (chunk_nanite.go).
	naniteChunksActive = !o.Editor
	beginFrameTarget(o.Target)
	defer endFrameTarget()
	// A TAA frame draws into the renderer's own scene target, which stands in
	// for the caller's from here to sceneFrameEnd (scene_target.go, taa.go).
	sceneFrameBegin(o)
	// The "off" anti-aliasing mode's frame: no window samples (antialias.go).
	beginAAModeFrame(o)
	defer endAAModeFrame()
	// The camera's LOD parameters, fixed for every pass of the frame, the
	// shadows' included (nanite_lod.go).
	beginNaniteLODFrame()
	defer endNaniteLODFrame()

	// Per-frame water shader inputs: animation clock and the view position
	// the fresnel/specular terms are measured against.
	gl.UseProgram(gfx.Program)
	gl.Uniform1f(gfx.TimeUniform, float32(o.Now))
	gl.Uniform3f(gfx.CamPosUniform, gfx.Cam.Position.X(), gfx.Cam.Position.Y(), gfx.Cam.Position.Z())
	// The clock a material's own uv_scroll is integrated against. Same
	// clock, kept on the CPU side because the offset it produces has to be
	// known before the draw — see gfx.MaterialScrollTime.
	gfx.MaterialScrollTime = float32(o.Now)

	// Fog, for the same reason and on the same schedule: the distance fog is
	// measured radially from the eye and the volumes are intersected against
	// a ray from it, so there is no "unchanged" frame to skip either way.
	// The other four programs that shade world geometry get theirs from
	// their own per-frame lighting upload — see world.UploadFogTo.
	world.UploadFog(lvl.Fog, lvl.Volumes, lvl)

	{
		// Chunks a script switched on or off last tick are reconciled
		// here, where the GL context is current and no pass is mid-way
		// through reading them (only unload_when_disabled chunks have
		// buffers to build or release). A change means the set of chunks
		// that exist has moved, so the spatial hash and the frames built
		// from it are rebuilt to match.
		if world.SyncChunkGeometry(lvl) {
			if err := world.RebuildIndexes(lvl); err != nil {
				log.Printf("chunk enable: %v", err)
			}
		}
		// And the brush batches with the meshes, before the first pass
		// reads them. Does nothing unless some brush mesh changed.
		SyncChunkBatches(lvl)
		// And the terrain meshes and their foliage, for the same reason: a
		// mined block's sections are uploaded before the shadow pass draws
		// them.
		SyncTerrainMeshes(lvl)

		// TODO(brush migration, Phase 5): the Water-kind biome sector's
		// companion water surface was synced here before anything read it
		// this frame. It comes back with scatter volumes.

		// Latch the biome wind clock before the shadow passes read it, so
		// scatter sways identically in the depth maps and in the color pass and
		// a blade's shadow stays under the blade.
		BiomeWindTime = float32(o.Now)
		// The cloud wind clock, latched for the same reason: the shadow bake
		// below and the volumetric pass after the transparent pass must see
		// one cloud, or its shadow slides off it.
		VolumeTime = o.Now

		// A light riding an entity or a bone moves whenever its carrier
		// does, which is not a change anything else announces — so while
		// any light is attached, the main program's lights go up every
		// frame. Here, after every tick that can move a carrier and
		// before anything is drawn. The other programs already take
		// SceneLights per frame.
		if world.HasAttachedLights(lvl) {
			world.UploadLightsTo(gfx.Program, world.SceneLights(lvl), false)
		}

		// --- Shadow pass: render depth from each shadow-casting light ---
		ProfileBegin("shadows: sun")
		shadowFrame := RenderShadowPass(lvl.Lights, lvl, gfx.Cam.Matrix, gfx.Projection)
		ProfileEnd()
		// The clouds' shadow map, before any program's shadow uniforms go
		// up — UploadShadowUniformsTo is what binds it. See cloud_shadow.go.
		ProfileBegin("shadows: clouds")
		if !o.skip("shadows: clouds") {
			RenderCloudShadowMap(lvl, gfx.Cam.Position)
		}
		ProfileEnd()
		// Live GI (gi.go): the distance field and the probes, before any
		// program's lighting uniforms go up — UploadShadowUniformsTo hands
		// every one of them what this leaves.
		ProfileBegin("gi")
		if !o.skip("gi") {
			UpdateGI(lvl, o)
		}
		ProfileEnd()
		UploadShadowUniforms(lvl.Lights, shadowFrame)
		// Scene lights, so an attached light's cubemap is rendered from
		// where its carrier has taken it rather than from its offset.
		ProfileBegin("shadows: point")
		if !o.skip("shadows: point") {
			RenderPointShadowPass(world.SceneLights(lvl), lvl)
		}
		ProfileEnd()
		// The scene's lights rather than the level's, so the shadow flags
		// at an effect flash's index are written (off) rather than left at
		// whatever a light that once had that slot set them to.
		UploadPointShadowUniforms(world.SceneLights(lvl))

		// --- Render ---
		// What is behind everything: the sky's own horizon, the fog colour
		// when there is fog and no sky, and the engine's slate blue when
		// there is neither. Set per frame rather than at startup because it
		// is level content and the level changes under it. See
		// WorldClearColor.
		ProfileBegin("clear")
		ApplyWorldClearColor(lvl)
		gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
		sceneFrameClear()
		ProfileEnd()

		// The 3D editor's wireframe shading (editor_3d_view.go). Drawing
		// the world as lines is the whole of it: with no filled triangles
		// nothing hides behind anything, which is the same contract the
		// handle picker switches to in this mode. The scatter and
		// displacement passes below are skipped rather than outlined —
		// a wireframe field of grass blades is a solid mask, which is
		// precisely what the mode exists to get rid of.
		editorWire := o.Wireframe
		if editorWire {
			gl.PolygonMode(gl.FRONT_AND_BACK, gl.LINE)
		}

		// Opaque pass: alpha means coverage here, so MSAA can reach the
		// edges a discard would otherwise leave hard — alpha-cutout
		// silhouettes (fragment.glsl's alphaSharpen) and the biome
		// patch's slope contour (ground_frag.glsl). Both are turned back
		// off below, before the transparent pass reads alpha as blend
		// weight again. See antialias.go.
		SetAlphaToCoverage(true)
		gl.UseProgram(gfx.Program)
		gl.Uniform1i(gfx.AlphaSharpenUniform, 1)
		// In a TAA frame there are no samples to cover, and the cutouts
		// dither instead (taa.go).
		gl.Uniform1i(gfx.UniformLoc(gfx.Program, "coverageDither\x00"), coverageDither())
		// A script zooming the camera (@level.prop.camera_fov) lands here,
		// before the frustum and every pass below read Projection — and so
		// does the level's own view distance, which is also the far plane.
		gfx.FarPlane = world.LevelViewDistance(lvl)
		gfx.SyncProjection()
		// And sent to the world program every frame, not only when it
		// changed: what was last sent may be another lens (the viewmodel's
		// restore, a jittered TAA frame's) or nothing at all.
		gfx.UploadProjection(gfx.Projection)
		// A TAA frame's sub-pixel jitter, on everything drawn from here to
		// sceneFrameEnd (taa.go).
		taaBeginJitter(lvl)

		// Build frustum from current projection × camera matrices
		projView := gfx.Projection.Mul4(gfx.Cam.Matrix)
		frustum := ExtractFrustum(projView)

		// Draw world geometry: chunks → sectors → floor/ceiling/walls
		// Two-pass rendering: opaque geometry first, then transparent geometry sorted back-to-front.
		chunksDrawn := 0
		brushesDrawn := 0
		brushDrawCalls := 0
		staticDrawn := 0
		var transparentDraws []TransparentDraw

		// Helper to queue or draw a geometry object based on its alpha.
		identity := mgl32.Ident4()
		drawOrQueue := func(g *gfx.Geometry, model mgl32.Mat4, doubleSided bool) {
			if g.IsTransparent() {
				// The mesh's own origin carried out by model — NOT
				// model * g.Position. A geometry's vertices are stored
				// relative to the position it was loaded with, and every
				// caller's model matrix already contains that same
				// translation (water: chunkModel*Translate(w.Position) with
				// g.Position == w.Position; an entity attachment:
				// world*AttachmentMatrix(), whose innermost term is Translate(m.Offset)
				// and g.Position == m.Offset; the spawn marker likewise).
				// Multiplying it in again put the sort key at twice the
				// offset, so translucent water and attachments sorted
				// against a position nothing occupies. Structural geometry
				// loads at the zero vector, so this is bit-identical there.
				pos := model.Mul4x1(mgl32.Vec4{0, 0, 0, 1})
				dx := pos.X() - gfx.Cam.Position.X()
				dy := pos.Y() - gfx.Cam.Position.Y()
				dz := pos.Z() - gfx.Cam.Position.Z()
				transparentDraws = append(transparentDraws, TransparentDraw{
					Geom:        g,
					Model:       model,
					Distance:    dx*dx + dy*dy + dz*dz,
					DoubleSided: doubleSided,
				})
				return
			}
			if model == identity {
				g.Draw()
			} else {
				g.DrawWithTransform(model)
			}
		}

		// A compiled level's static surfaces. Culled by the compiled
		// visibility set first — every cell the camera's own cannot see is
		// gone before a single bounding box is tested — and then by the
		// frustum, which is what removes the rest of the cell the camera
		// IS in; the survivors are drawn out of their batches, one call per
		// source chunk and material. The chunks those surfaces came from
		// are still walked below, for the frame-owned and switched-off
		// ones; the compiled ones carry Chunk.Compiled and are skipped.
		//
		// A transparent one is queued with its own centre as the sort key,
		// not a model matrix's translation: its vertices are already in
		// world space, so every one of them would sort at the origin.
		// Two-pass occlusion (nanite_cull.go): the GPU-driven cull's first
		// pass, here and in the scenery, draws what was visible last frame;
		// naniteOcclusionPass, after the rest of the opaque world, finishes.
		naniteTwoPass = NaniteOcclusion && !o.skip("nanite: occlusion")
		// The visibility buffer, in a scene-target frame: the GPU cull's
		// draws below write triangle IDs, shaded after its last pass
		// (nanite_vis.go).
		naniteVisBegin()
		naniteChunkPending = naniteChunkPending[:0]
		DebugStats.NaniteClusters, DebugStats.NaniteTriangles = 0, 0
		DebugStats.NaniteFoliage = false
		ProfileBegin("world: compiled")
		if lvl.Static != nil {
			staticDrawScratch = lvl.Static.VisibleSurfaces(gfx.Cam.Position, staticDrawScratch)
			n, calls := DrawStaticSurfaces(lvl, staticDrawScratch, frustum, gfx.Cam.Position,
				func(d BatchDraw, distance float32) {
					transparentDraws = append(transparentDraws, TransparentDraw{
						Batch:       &d,
						Distance:    distance,
						DoubleSided: d.DoubleSided,
					})
				})
			staticDrawn += n
			brushDrawCalls += calls
		}
		ProfileEnd()

		ProfileBegin("world: chunks")
		for i := range lvl.Chunks {
			chunk := &lvl.Chunks[i]
			// A compiled chunk's static surfaces were drawn above, out of
			// the compiled list; the chunk itself is still here for
			// collision, raycasts and scripting (world/static.go). Its
			// DYNAMIC brushes are the exception: the compiler leaves them
			// out of the bake precisely because they move, so they are
			// still live geometry and are drawn the ordinary way below.
			if chunk.Compiled && !chunk.HasDynamicBrushes() {
				continue
			}
			if !chunk.IsEnabled() {
				// Switched off — see world/chunk_enable.go. Its buffers may
				// well still be built; the flag is what decides.
				continue
			}
			if o.Editor {
				// Editor mutations (vertex drag, extrusion/terrain edits,
				// height changes) can move geometry
				// outside the chunk's cached AABB without updating it. Keep
				// bounds honest while editing so culling doesn't hide what
				// was just added.
				chunk.RecomputeBounds()
			}
			chunkMin, chunkMax := chunk.WorldBounds()
			if !AABBInFrustum(frustum, chunkMin, chunkMax) {
				continue
			}
			chunksDrawn++

			// Every chunk's geometry is authored in its own local space and
			// carried into the world (or into an owning frame's space) by
			// ModelMatrix; the identity for an unmoved, unrotated,
			// unscaled chunk is what keeps this one loop.
			chunkModel := chunk.ModelMatrix()

			// Brushes, out of the chunk's batches: culled brush by brush
			// against the frustum by the same box the shadow pass culls
			// them by, the survivors drawn with one call per material, and
			// transparent materials queued per brush for the sorted pass.
			// Culling is the frustum and, for a release build, the
			// compiled PVS; in the editor it is the frustum alone.
			st := DrawChunkBrushes(lvl, chunk, frustum, chunkModel, gfx.Cam.Position,
				func(d BatchDraw, distance float32) {
					transparentDraws = append(transparentDraws, TransparentDraw{
						Batch:       &d,
						Distance:    distance,
						DoubleSided: d.DoubleSided,
					})
				})
			brushesDrawn += st.Brushes
			brushDrawCalls += st.DrawCalls
		}
		ProfileEnd()

		// Chunks' block terrain (terrain.go), in the opaque pass and ahead
		// of the foliage and scatter so what grows lands on its depth.
		ProfileBegin("terrain")
		if !o.skip("terrain") {
			RenderTerrain(lvl, projView, &frustum, shadowFrame)
		}
		ProfileEnd()

		// Placed entities. Unlike the spawn marker below, these are part
		// of the world and draw in gameplay as well as in the editor.
		ProfileBegin("entities")
		if !o.skip("entities") {
			entityDraw := drawOrQueue
			if taaRecording() {
				// TAA's motion vectors need each moving attachment's
				// matrix (taa.go).
				entityDraw = func(g *gfx.Geometry, model mgl32.Mat4, doubleSided bool) {
					taaNoteGeometry(g, model)
					drawOrQueue(g, model, doubleSided)
				}
			}
			RenderEntities(lvl, &frustum, entityDraw)
		}
		ProfileEnd()

		// Placed scenery — the same story, one matrix simpler. A cooked
		// Nanite mesh's placements go through the GPU-driven cull instead
		// (nanite_scenery.go).
		ProfileBegin("scenery")
		RenderScenery(lvl, &frustum, drawOrQueue)
		ProfileEnd()
		ProfileBegin("nanite: scenery")
		RenderNaniteScenery(lvl, frustum, projView)
		ProfileEnd()

		// The GPU-driven cull's second pass: the Hi-Z pyramid from every
		// opaque thing drawn so far, and what the first passes missed. After
		// the entities and scenery, so they hide what is behind them too.
		ProfileBegin("nanite: occlusion")
		naniteOcclusionPass(lvl)
		ProfileEnd()

		// The visibility buffer's material pass: every pixel the cull's
		// draws still own, shaded once, before anything else draws over
		// the opaque world (nanite_vis.go).
		naniteVisShade(lvl, shadowFrame, o.Now)

		// The spawn marker is an authoring aid, not level geometry: it
		// exists to be seen and dragged in the editor and must never show
		// up in the shot the player spawns into.
		if o.Editor && lvl.Spawn != nil {
			// A spawn aboard a vehicle is authored in that vehicle's
			// coordinates, so its marker draws under the same transform its
			// geometry does — otherwise the arrow sits at the world origin
			// while the player will start inside the ship.
			m := lvl.Spawn.ModelMatrix()
			if lvl.Spawn.Frame != "" {
				m = lvl.Frames.Frame(lvl.Spawn.Frame).World.Mul4(m)
			}
			drawOrQueue(&lvl.Spawn.Geometry, m, false)
		}

		// Volumes are regions rather than objects: invisible during play,
		// and drawn here as wireframe cages so an author can see what they
		// are placing. Same rule as the spawn marker.
		//
		// An entity with no mesh yet gets the same treatment, for the same
		// reason: it has to be visible to be worked on, and invisible once
		// the editor is closed.
		if o.Editor {
			RenderVolumes(lvl, &frustum)
			o.markers(lvl, &frustum)
			// The scenery brush's preview. Queued rather than drawn, so a
			// translucent ghost sorts against the world it is being placed
			// into instead of blending over the top of it.
			o.brushGhost(drawOrQueue)
		}

		if !editorWire {
			// --- Terrain foliage (world/foliage.go) ---
			// Solid and depth-written like the scatter, with cutout cards
			// antialiased by the alpha-to-coverage switched on above.
			ProfileBegin("foliage")
			if !o.skip("foliage") {
				RenderFoliage(lvl, projView, &frustum, shadowFrame)
			}
			ProfileEnd()

			// --- Scatter volumes (GPU grass / flowers / rock) ---
			// After opaque geometry but before the transparent pass:
			// blades and rocks are solid and depth-written, so anything
			// translucent still sorts on top of them.
			ProfileBegin("scatter")
			RenderScatterVolumes(lvl, projView, gfx.Cam.Position, shadowFrame)
			ProfileEnd()

			// --- Sprites, cutout only ---
			// Inside the opaque block on purpose: a cutout sprite's alpha
			// is coverage, so the alpha-to-coverage still switched on above
			// antialiases its silhouette and it needs no sorting at all.
			// The alpha and additive modes come after the transparent pass.
			ProfileBegin("sprites: cutout")
			if !o.skip("sprites") {
				RenderSpritesOpaque(lvl, projView, gfx.Cam.Position, &frustum, shadowFrame)
			}
			ProfileEnd()
		}

		// End of the opaque pass — hand alpha back to blending. Done
		// unconditionally rather than inside the transparent block below,
		// so the editor overlays and ImGui the caller draws after this never
		// inherit either setting on a frame with nothing transparent in it.
		SetAlphaToCoverage(false)
		gl.UseProgram(gfx.Program)
		gl.Uniform1i(gfx.AlphaSharpenUniform, 0)
		gl.Uniform1i(gfx.UniformLoc(gfx.Program, "coverageDither\x00"), 0)

		// The sun's virtual shadow map: the pages the opaque world's pixels
		// read, asked for from the depth it left, for a frame or two from
		// now (vsm.go). Before the decals and everything translucent, which
		// read what the opaque world asked for.
		if !editorWire {
			vsmMarkReceivers()
		}

		// --- Decals ---
		// Between the opaque world and the sky, and every part of that is
		// deliberate: a decal blends with the surface under it, so that
		// surface must already be drawn; and it belongs TO that surface
		// rather than floating in front of it, so it goes under the glass and
		// water of the transparent pass rather than sorting against them. See
		// render/decal_render.go for the depth state, which is the rest of
		// the trick. Skipped in wireframe for the reason the sky is.
		if !editorWire {
			ProfileBegin("decals")
			if !o.skip("decals") {
				RenderDecals(lvl, &frustum)
			}
			ProfileEnd()
		}

		// --- Sky ---
		// Exactly here, and the position is the whole optimisation: after
		// every pass that writes depth, so the depth test discards the sky
		// wherever the world already drew and it costs only the pixels
		// nothing covered; and before the transparent pass, which has to
		// blend against it. Skipped in wireframe for the reason the scatter
		// is — a filled sky drawn as lines is neither.
		if !editorWire {
			ProfileBegin("sky")
			RenderSky(lvl)
			ProfileEnd()
		}

		// --- Transparent pass: draw collected transparent geometry back-to-front ---
		ProfileBegin("transparent")
		if len(transparentDraws) > 0 {
			sort.Slice(transparentDraws, func(a, b int) bool {
				return transparentDraws[a].Distance > transparentDraws[b].Distance
			})

			gl.Enable(gl.BLEND)
			gl.BlendFunc(gl.SRC_ALPHA, gl.ONE_MINUS_SRC_ALPHA)
			gl.DepthMask(false)

			for i := range transparentDraws {
				td := &transparentDraws[i]
				if td.DoubleSided {
					// Two-sub-pass for double-sided: back faces first, then front faces
					gl.Enable(gl.CULL_FACE)
					gl.CullFace(gl.FRONT) // draw back faces
					td.draw()
					gl.CullFace(gl.BACK) // draw front faces
					td.draw()
					gl.Disable(gl.CULL_FACE)
				} else {
					td.draw()
				}
			}

			gl.DepthMask(true)
			gl.Disable(gl.BLEND)
		}
		ProfileEnd()

		// --- Volumetrics: cloud volumes, the sky's cloud deck, fireballs ---
		// After the transparent pass, so a cloud behind glass is seen
		// through it; before the sprites, so smoke puffs and sparks draw
		// over a fireball rather than under it. Marched against a copy of
		// the depth the opaque pass left, which nothing since has written.
		// Skipped in wireframe for the reason the sky is. See volumetric.go.
		if !editorWire {
			ProfileBegin("volumetrics")
			if !o.skip("volumetrics") {
				RenderVolumetrics(lvl, projView, gfx.Cam.Position, &frustum, MainVolumeTargets)
			}
			ProfileEnd()
		}

		// --- Sprites, alpha and additive ---
		// After the world's transparent pass rather than inside it: a
		// sprite batch is many quads under one draw, so it cannot join a
		// queue that sorts one Geometry at a time — and additive quads want
		// no sorting at all, which that queue has no way to express.
		if !editorWire {
			ProfileBegin("sprites: blended")
			if !o.skip("sprites") {
				RenderSpritesTransparent(lvl, projView, gfx.Cam.Position, &frustum, shadowFrame)
			}
			ProfileEnd()
		}

		// --- First-person viewmodel ---
		// Last thing drawn in the 3D world, on purpose: it clears the depth
		// buffer so it can never be poked into by nearby geometry (see
		// RenderViewmodel), and nothing after this point in the
		// frame writes to depth again before the next frame's own clear —
		// running any earlier would let whatever came after undo that.
		// Skipped in the editor for the reason the runtime UI is: the
		// view here is the level being built, not a game being played from
		// inside a body that has hands — except while the Player panel's
		// Viewmodel section is open, which is the one moment the editor
		// itself needs to see this (EditorPreviewActive).
		// A TAA frame's motion vectors, from the world's depth before the
		// viewmodel clears it (taa.go).
		taaMotionPass()
		if !o.Editor || EditorPreviewActive {
			ProfileBegin("viewmodel")
			RenderViewmodel()
			// The hands move with the camera: no motion under them.
			taaViewmodelMotion()
			ProfileEnd()
		}

		// GI's drawn debug views (`gi_debug probes|field`), over the world
		// and under the viewmodel's hands.
		if GIDebug != GIDebugOff {
			drawGIDebug()
		}

		// Back to filled triangles before the editor overlays and ImGui the
		// caller draws next, neither of which would survive being outlines.
		if editorWire {
			gl.PolygonMode(gl.FRONT_AND_BACK, gl.FILL)
		}

		// A TAA frame's resolve, into the caller's target (taa.go).
		sceneFrameEnd()

		DebugStats.ChunksDrawn = chunksDrawn
		DebugStats.BrushesDrawn = brushesDrawn
		DebugStats.BrushDrawCalls = brushDrawCalls
		DebugStats.StaticSurfaces = staticSurfaceCount(lvl)
		DebugStats.StaticDrawn = staticDrawn
	}
}

// staticDrawScratch is the list of compiled surfaces to draw this frame, reused
// across frames so the world pass allocates nothing for it. Package-level
// rather than a local because DrawWorld is the only writer and a fresh slice
// every frame is a few thousand ints of garbage per second.
var staticDrawScratch []int

// staticSurfaceCount is how many compiled surfaces the level has at all, for
// the debug overlay to show the drawn count against.
func staticSurfaceCount(lvl *world.Level) int {
	if lvl.Static == nil {
		return 0
	}
	return len(lvl.Static.Surfaces)
}
