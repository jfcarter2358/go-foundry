# Poly Haven assets

[Poly Haven](https://polyhaven.com) publishes photo-scanned rocks, cliffs,
plants and ground textures under CC0: free for any use, commercial included,
with no attribution required. Foundry can search their library, download what
you pick and import it as engine assets — cooked Nanite meshes with their
textures and normal maps — from the editor, from the command line, or inside
a generator run. The procedural canyon's generators use these scans by default
wherever one suits, and generate only what Poly Haven cannot supply.

Assets from Poly Haven are credited on screen wherever the editor shows them
(the browser's footer, the Generator's asset lists). Foundry is not affiliated
with or endorsed by Poly Haven.

## The browser

Open it with the **Poly Haven** button in the editor strip.

- **Models / Textures** switch the catalog. The search box matches IDs, names,
  tags and categories (every word); the category menu narrows it; sort by
  downloads, name, date or triangle count. Thumbnails load as you scroll.
- Click an asset for its **details**: name, ID, authors, categories, tags,
  triangles, real-world size, date, licence, and whether this project has
  imported it.
- Pick a **resolution** (2k is plenty for most pieces; 1k for small scatter),
  optionally a **max texture px** to scale the textures further, and for a
  model:
  - **Plant** — cutout leaves, no collision, no back (on by default for plants,
    trees, ground cover and flowers);
  - **Back** — for cliff pieces: `auto` estimates which side is the flat back
    (what `cliff_wall` presses into the wall); `none`, `-z`, `+z`, `-x`, `+x`
    or a heading in degrees say it yourself;
  - **Max triangles** decimates before the cook (0: as scanned); **Lite
    triangles** adds a light copy for scattering by the hundred.
- **Download & Import** fetches the files (once — they are cached, so a second
  import or another project downloads nothing) and imports them, with a
  progress bar, a log and **Cancel**.
- **Add to stage** appends the asset to a Generator stage's asset list: the list
  whose **Browse…** opened the browser, or else the selected stage's first list
  of that type (`polyhaven_rocks`, `polyhaven_plants`, `polyhaven_textures`).

## What an import makes

A model `<id>`:

| File | What |
| --- | --- |
| `models/polyhaven/<id>/<id>.gltf` (+ its `.bin`) | the glTF as downloaded, its images pointed at the engine textures below |
| `models/polyhaven/<id>/<id>[_t<n>].fnm` | the cooked Nanite mesh (and decimated or `_lite` copies); place it as scenery |
| `models/polyhaven/<id>/<id>_<part>.fnm` | one per piece when the file holds several side by side |
| `models/polyhaven/<id>/polyhaven.yaml` | provenance: authors, licence, page, every file's URL and md5, the date, what was made |
| `textures/ph_<id>[_<material>].png` | the albedo (with the cutout alpha merged in for leaves) |
| `textures/ph_<id>…_normal.png` | its normal map, picked up automatically |
| `textures/ph_<id>…_arm.png`, `_height.png` | ambient occlusion / roughness / metal, and the displacement, for materials |

A texture `<id>`: `textures/ph_<id>.png`, `_normal`, `_arm`, `_rough`, `_ao`,
`_height`, and `textures/polyhaven/<id>/polyhaven.yaml`.

The mesh is put in the **placement frame** the generators use: its lowest point
on y = 0 and its footprint centred, so a placement at a ground point sits on
it; a cliff piece with a back has it facing −Z on z = 0. Scans are in metres;
an import whose size disagrees with Poly Haven's listed dimensions says so.
Collision: hulls for pieces up to 4 m, the mesh for larger ones, none for
plants.

Place an imported mesh like any cooked mesh: a `scenery:` entry naming the
`.fnm`, or **Explorer → Add**. **Window → Unused Assets** never offers the
imports' files.

## From the command line

```
foundry polyhaven search [-t models|textures] [-c category] [-tags a,b] [-sort downloads] [words...]
foundry polyhaven info <id>
foundry polyhaven import [-res 2k] [-max-size N] [-max-triangles N] [-lite N] [-back auto] [-plant] <id>...
foundry polyhaven import -textures [-res 2k] <id>...
foundry polyhaven cache
```

`-offline` (or `FOUNDRY_POLYHAVEN_OFFLINE=1`) uses only what is already
downloaded. The cache is `~/.cache/foundry/polyhaven` (`FOUNDRY_POLYHAVEN_CACHE`
moves it): catalog and file listings kept a day, downloaded files kept by md5.

## In a generator

The `polyhaven_rocks`, `polyhaven_plants` and `polyhaven_textures` stages
([Generators](33-generators.md)) import their assets inside the run and hand
them on: rocks to `scatter` and `cliff_wall`, plants to `scatter` rules,
texture layers to the material stages. Their asset lists show thumbnails and
open the browser with **Browse…**.

The **Desert canyon** template ([Generators](33-generators.md#make-a-desert-canyon-in-five-minutes))
uses, at 2k: the textures `red_sand`, `cracked_red_ground`,
`dry_river_pebbles` and `cliff_side`; the cliff scans `namaqualand_cliff_01`
and `_02`, `rock_face_01`, `mountainside` and `coastal_cliff_02` (decimated to
400k triangles on import); the boulders `namaqualand_boulder_02` to `_05`; the
stone sets `namaqualand_stones_01` and `namaqualand_rocks_01`; and the plants
`quiver_tree_01`, `quiver_tree_02`, `dead_quiver_trunk`, `wild_rooibos_bush`
and `shrub_04` — about 230 MB of downloads, cached once for every project.
(`rock_face_02` was tried and left out: its glTF is about half the size Poly
Haven lists, and stood beside the others it reads as a pebble.)

## Texture memory

Scanned sets are 2k–4k a map, which is a lot of video memory uncompressed
(a 2k texture with mips is 21 MB). Two graphics settings help, both off by
default:

- **Texture compression** (`texture_compression` in graphics.yaml,
  `@level.prop.texture_compression`, or `FOUNDRY_TEXTURE_COMPRESSION=1`): world
  textures of 256 px and up are uploaded BC7 (albedo) and BC5 (normal maps),
  a quarter of the memory. The first load encodes them (about 0.2 s per 2k map)
  and keeps the result in `~/.cache/foundry/texcache`, so later loads are as
  fast as before.
- **Skip mips** (`texture_skip_mips`, 0–3, or `FOUNDRY_TEXTURE_SKIP_MIPS`): drop
  that many of the largest mip levels of textures 512 px and up — each one
  quarters their memory.

F3 shows the textures' memory (`Textures: N MB (… BC7, … BC5)`).

A generated terrain's splat layers are packed into two atlases of their own.
They are built only for materials a loaded level wears (not at startup for
every material), and with texture compression on they are BC7 too: a
four-layer 2k set takes 48 MB instead of 193 MB. The desert canyon level holds
about 1 GB of textures uncompressed; turn compression on for it.
