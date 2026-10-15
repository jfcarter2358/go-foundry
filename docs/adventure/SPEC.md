# The `adventure` level — spec

The user asked (2026-10-14):

> I want to recreate something as similar to the UE5 Nanite tech demo. I have attached
> screenshots from various moments in the .screenshots directory, use these as reference to
> the quality and level of detail in the assets. You should use assets from Poly Haven
> wherever possible and flex the power of our Nanite implementation. I want you to build a
> medium sized 3D adventure-style game level where the (third person) player character
> starts in a slot canyon where you can see the sand and the layers of rock with light
> streaming in, then goes into a cave which looks like the one in the screenshots, finally
> exiting the cave into a high part of the mountains with ancient ruins stretching down the
> mountain. Remember, performance is paramount. Shoot for 60FPS and if and only if that is
> not possible then 30FPS is acceptable. If you need to create assets or download them from
> Poly Haven or make any changes to the engine then do so. Implement this for me in our
> Foundry OneDrive project as the level named `adventure`.

Reference shots: `.screenshots/*.png` in the repo root (UE5 "Lumen in the Land of Nanite" /
"Valley of the Ancient" style). Look at them. What they show:

- **Slot canyon** (113107, 113549): a narrow passage, a dark-shadowed floor of rippled sand
  and rock rubble, sunlit patches where light falls through the gap; warm layered sandstone
  walls with ledges, undercuts and vertical joints; dense small rock and sparse dry grass.
- **Cave** (113107 background, 113117): hanging rock drapery from the roof, a bright
  opening; a floor of piled, broken, angular sandstone blocks at every scale (the "Nanite
  rock pile"), fine sand settled in the gaps.
- **Mountain & ruins** (113157, 113138): from a high ledge, a vista down a canyon-cut
  mountainside covered in carved stepped temple towers, terraces and walls that grow out of
  the cliffs, a hazy sunlit desert valley and sharp far peaks beyond; ropes/ladders/prayer
  flags on the cliffs.

## Phases

Each phase writes `docs/adventure/PHASE_<x>.md`: what was built, how it was verified,
what is left. A phase leaves the tree building (`go build ./...`, `go vet` on touched
packages) and its new tests passing.

| Phase | What |
| --- | --- |
| A | **Third-person player** (engine): `player: camera:` mode `third_person` (boom behind/over the shoulder, mouse orbits, pulled in on collision, smooth), a visible **body** model drawn in the world pass with shadows, facing its movement direction, locomotion clips (idle/walk/run/jump/fall) chosen from its motion; viewmodel off in third person. Editor Player panel + YAML docs. Works in `foundry play` (shots show the body). |
| B | **Light shafts + dark interiors** (render): a shadowed in-scattering march (VSM) so sunbeams stream through the slot and the cave mouth; level/environment setting to drive it; an `ambient` volume (or equivalent) that darkens the sky ambient inside enclosed spaces when GI is off (the user's graphics.yaml has `gi_quality: off`). Cheap: budget ≤ 1.5 ms at 2560×1080 on the 1070. |
| C | **Authored terrain + cave stage** (procgen): terrain `stamps` (authored landforms at positions: mountain massif, plateau, ledge/platform, ramp, so the layout is designed, not left to noise) and a new `cave` stage: an SDF tunnel/chamber system along a 3D spline (radius/height per point), sandstone strata, roof drapery, broken floor, meshed in sections like wall_sheet (dual contouring, simplify, AO incl. a large-reach sky-visibility term into vertex AO), cooked to Nanite with mesh collision, that **cuts the terrain** where it passes through (no tile triangles inside the tunnel) so the mouths are open. Draw distances on its placements. |
| D | **Ruins stage** (procgen): ancient sandstone architecture as SDF → Nanite: stepped temple towers with cornices/carved friezes/domes, terrace retaining walls following the slope, stairways, walls, columns/arches, collapse and weathering, rubble. Textured with Poly Haven masonry textures (e.g. `large_sandstone_blocks`, `sandstone_blocks_08`, `rabdentse_ruins_wall`, `red_sandstone_wall`, `old_sandstone_02`) via the existing material path; mesh collision on walkable parts; draw distances. Laid out along an authored path down a slope. |
| E | **The level**: `resources/generators/adventure.fgen` + `levels/adventure.yaml` in the OneDrive project; slot canyon → cave → summit ledge → ruins down the mountain, with the far backdrop; Poly Haven rocks/plants/props (no biomes); the third-person player (body: a `human`-skeleton model, e.g. mechanic/marine); light shafts; chunks per area; a reason to walk to the bottom (a goal object). Iterate on `foundry play` shots against the references. |
| F | **Performance** on the user's GTX 1070 (native Windows bench, 2560×1080, user graphics.yaml): 60 fps (≤16.6 ms GPU and CPU) at poses through all three areas, moving. Only if impossible, 30 fps, with the reason written down. |

## Fixed facts and conventions

- Repo: `/home/jfcarter2358/personal/code/main/foundry`, branch `feature/jc/brush-editing`.
  **Do not commit**, never `git add -A`. Another session (music/score editor) shares this
  working tree: touch shared files additively and don't revert changes you didn't make.
- OneDrive project: `/mnt/c/Users/jcart/OneDrive/Documents/Foundry` (resources/ under it).
  C: has ~49 GB free; keep the level's generated output well under ~2 GB.
- Docs to read first: `docs/guide/33-generators.md` (stages, CLI, perf), `docs/canyon/PHASE_7.md`,
  `PHASE_8.md`, `PERF.md`, `docs/regions/SPEC.md`, `docs/nanite/` as needed.
- Tools (don't write scratch harnesses): `foundry gen run/new/list`, `foundry check`,
  `foundry sim`, `foundry play run` (headless real GL screenshots, command script, `//` comments),
  `foundry render`, `foundry profile-load`, `foundry bench` / `FOUNDRY_BENCH` (native perf:
  see `docs/canyon/PERF.md` Method for the Windows cross-build and `.bat` trick).
  WSLg (Mesa d3d12) frame times are NOT the card's; judge perf only natively.
- Poly Haven: prefer its CC0 scans/textures over generated assets (scaled down is fine); use
  `polyhaven_rocks` / `polyhaven_plants` / `polyhaven_textures` stages; never the
  vegetation stage, `kind: biome` volumes or .fbio biomes. Credit stays visible as today.
- No explanatory HUD hint text ("press E to…"); players figure it out.
- Generators run inside the editor: new generation is procgen StageKinds driven by `.fgen`,
  not Python scripts. Bump a stage's `Version` when its output changes.
- New render paths are switchable (env var / setting) with the old path as fallback.
- Resource limits (WSL, 32 GB): one heavy job (big `go test ./...`, GL suites, big cooks) at
  a time; `GOFLAGS=-p=6`, `GOMEMLIMIT=6GiB`; give long commands a timeout; bound procgen
  with `-workers`/`cook_memory`. GL tests: `FOUNDRY_GL_TEST=1`, `GODEBUG=cgocheck=0`.
- Known pre-existing failures (not yours): TestNaniteStreamStreamsInFromTheRoots,
  TestAPlaySessionDrawsTheRoomItIsStandingIn, TestNaniteFoliageDrawsTheClassicNearField,
  KeepsTheForestsLookToTheHorizon.
