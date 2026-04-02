# Foundry Engine — Implementation Plan

## Goal

Refactor the engine to match `DESIGN.md`. The world structure should be:
**Chunk → Sector → Wall / Portal / Floor / Ceiling**, plus an **Entity** system and **Collision Detection**. Scripting and animations are out of scope for v1.

---

## Current File Inventory

| File | Purpose | Status |
|------|---------|--------|
| `main.go` | SDL2 window, OpenGL init, game loop | Working but draws only walls |
| `camera.go` | Camera struct, `LoadCamera()`, `MoveCamera()` | Working |
| `graphics.go` | `LoadProgram()`, `CompileShader()` | Working |
| `input.go` | Keyboard/mouse → camera | Working |
| `wall.go` | `Geometry` struct + `Draw()`/`Load()` | **BUGS — see Phase 0** |
| `sector.go` | `Sector` struct + `Load()` | Partially done, bugs |
| `level.go` | Level loading, YAML parse | **Broken** — old CSV format |
| `portal.go` | `Portal` struct | Stub only |
| `point.go` | `Point` struct with X, Y | Fine |
| `texture.go` | `LoadTexture()` | Working |

Dependencies (from `go.mod`):
- `github.com/go-gl/gl/v3.3-core/gl`
- `github.com/go-gl/mathgl/mgl32`
- `github.com/veandco/go-sdl2/sdl`
- `gopkg.in/yaml.v2`

---

## Phase 0 — Fix Bugs (do this first)

### `wall.go` — `Geometry.Load()` (line 50)

The local variable `verts` is built correctly from `indices` but **never assigned back** to `g.Vertices`. Fix:

```go
// After the loop that builds verts, before gl.BufferData:
g.Vertices = verts
gl.BufferData(gl.ARRAY_BUFFER, len(g.Vertices)*4, gl.Ptr(g.Vertices), gl.STATIC_DRAW)
```

### `wall.go` — `Geometry.Draw()` (lines 63–76)

The receiver is `g Geometry` but every line inside uses the undeclared variable `w`. Replace all `w.` with `g.`:

```go
func (g Geometry) Draw() {
    g.Model = mgl32.Translate3D(g.Position.X(), g.Position.Y(), g.Position.Z())
    gl.UseProgram(Program)
    gl.UniformMatrix4fv(g.ModelUniform, 1, false, &g.Model[0])
    gl.BindVertexArray(g.Vao)
    gl.ActiveTexture(gl.TEXTURE0)
    gl.BindTexture(gl.TEXTURE_2D, Textures[g.Texture])
    gl.DrawArrays(gl.TRIANGLES, 0, int32(len(g.Vertices)/5))
}
```

Note: `int32(len(g.Vertices)/5)` replaces the hard-coded `6*2*3`.

Also: since geometry will be stored in **world space** (see Phase 3), the model matrix should always be `mgl32.Ident4()`. Change the first line of `Draw()` to:

```go
g.Model = mgl32.Ident4()
```

And remove `Position`, `Yaw` from the `Geometry` struct if desired (they become unused).

### `sector.go` — `Load()` (lines 29–31)

`Vec2.Add()` and `Vec2.Mul()` return new values; they don't mutate. Fix:

```go
for _, pt := range points {
    s.Center = s.Center.Add(pt)
}
s.Center = s.Center.Mul(1.0 / float32(len(points)))
```

### `graphics.go` — Windows line endings

Add `strings.ReplaceAll` before trimming to handle `\r\n` in shader files:

```go
source := strings.ReplaceAll(strings.TrimRight(string(vertexShaderBytes), "\n"), "\r\n", "\n")
```

Apply the same to the fragment shader source.

---

## Phase 1 — Data Structure Refactor

Rewrite the following files to establish the new type hierarchy. Do NOT implement rendering yet — just the structs and their `Load()` signatures.

### `wall.go` — new `Wall` type

Keep `Geometry` as-is (the GPU resource handle). Add a `Wall` wrapper:

```go
type Wall struct {
    StartPoint mgl32.Vec2
    EndPoint   mgl32.Vec2
    Texture    string
    Collision  bool
    Geometry   Geometry
}
```

### `portal.go` — replace stub

```go
package main

import "github.com/go-gl/mathgl/mgl32"

type Portal struct {
    StartPoint mgl32.Vec2
    EndPoint   mgl32.Vec2
    ToSector   string
}
```

### `sector.go` — complete rewrite

```go
package main

import "github.com/go-gl/mathgl/mgl32"

type Floor struct {
    Enabled  bool
    Height   float32
    Texture  string
    Terrain  bool
    Geometry Geometry
}

type Ceiling struct {
    Enabled bool
    Height  float32
    Texture string
    Geometry Geometry
}

type Sector struct {
    ID       string
    Points   []mgl32.Vec2  // ordered polygon vertices in XZ plane
    Walls    []Wall
    Portals  []Portal
    Floor    Floor
    Ceiling  Ceiling
    MinBound mgl32.Vec2    // XZ AABB for culling
    MaxBound mgl32.Vec2
}
```

### `chunk.go` — new file

```go
package main

import "github.com/go-gl/mathgl/mgl32"

type Chunk struct {
    ID       string
    Sectors  []Sector
    MinBound mgl32.Vec3
    MaxBound mgl32.Vec3
}
```

### `entity.go` — new file

```go
package main

import "github.com/go-gl/mathgl/mgl32"

type BoundingType int

const (
    BoundingAABB     BoundingType = iota
    BoundingSphere
    BoundingCylinder
)

type Entity struct {
    ID            string
    Position      mgl32.Vec3
    Velocity      mgl32.Vec3
    ModelPath     string
    TexturePath   string
    BoundingKind  BoundingType
    BoundHalfX    float32
    BoundHalfY    float32
    BoundHalfZ    float32
    Geometry      Geometry
    Visible       bool
    CurrentSector string
    CurrentChunk  string
}
```

### `level.go` — new `Level` struct

```go
type Level struct {
    Chunks   []Chunk
    Entities []Entity
}
```

---

## Phase 2 — YAML Format + Parser

### New `resources/levels/test.yaml` format

The existing `test.yaml` uses `points` and `sectors` at the top level without `chunks`. Migrate it to wrap the content in a `chunks` array, add ceiling definitions, and add a player entity. Example:

```yaml
chunks:
  - id: main_floor
    points:
      A: { x: -2, y: 5 }
      B: { x: 3,  y: 1 }
      C: { x: 5,  y: 1 }
      D: { x: 6,  y: 2 }
      E: { x: 7,  y: 2 }
      F: { x: 8,  y: 1 }
      G: { x: 8,  y: -1 }
      H: { x: 7,  y: -2 }
      I: { x: 6,  y: -2 }
      J: { x: 5,  y: -1 }
      K: { x: 3,  y: -1 }
      L: { x: -2, y: -5 }
    sectors:
      - id: A
        floor:   { enabled: true,  height: 0.0,  texture: square, terrain: false }
        ceiling: { enabled: true,  height: 3.0,  texture: square }
        edges:
          - { from: A, to: B, kind: wall,   texture: square }
          - { from: K, to: L, kind: wall,   texture: square }
          - { from: L, to: A, kind: wall,   texture: square }
          - { from: B, to: K, kind: portal, to_sector: B }
      - id: B
        floor:   { enabled: true,  height: -0.25, texture: square, terrain: false }
        ceiling: { enabled: true,  height: 3.0,   texture: square }
        edges:
          - { from: B, to: C, kind: wall,   texture: square }
          - { from: C, to: D, kind: wall,   texture: square }
          - { from: D, to: E, kind: wall,   texture: square }
          - { from: E, to: F, kind: wall,   texture: square }
          - { from: F, to: G, kind: wall,   texture: square }
          - { from: G, to: H, kind: wall,   texture: square }
          - { from: H, to: I, kind: wall,   texture: square }
          - { from: I, to: J, kind: wall,   texture: square }
          - { from: J, to: K, kind: wall,   texture: square }
          - { from: B, to: K, kind: portal, to_sector: A }
      - id: C
        floor:   { enabled: true,  height: 0.25, texture: square, terrain: false }
        ceiling: { enabled: true,  height: 3.0,  texture: square }
        edges:
          - { from: A, to: B, kind: wall,   texture: square }
          - { from: B, to: K, kind: portal, to_sector: B }
          - { from: K, to: L, kind: wall,   texture: square }
          - { from: L, to: A, kind: wall,   texture: square }

entities:
  - id: player
    position: { x: 0.0, y: 1.0, z: 0.0 }
    model: ""
    texture: ""
    bounding: aabb
    bound_half_x: 0.3
    bound_half_y: 0.9
    bound_half_z: 0.3
```

**Coordinate convention**: sector `points` `{x, y}` map to world `(X, Z)`. Y-up is the OpenGL convention used throughout.

### Raw YAML structs (add to `level.go`)

These are only used during unmarshaling:

```go
type RawPoint struct {
    X float32 `yaml:"x"`
    Y float32 `yaml:"y"`
}

type RawFloor struct {
    Enabled bool    `yaml:"enabled"`
    Height  float32 `yaml:"height"`
    Texture string  `yaml:"texture"`
    Terrain bool    `yaml:"terrain"`
}

type RawCeiling struct {
    Enabled bool    `yaml:"enabled"`
    Height  float32 `yaml:"height"`
    Texture string  `yaml:"texture"`
}

type RawEdge struct {
    From      string `yaml:"from"`
    To        string `yaml:"to"`
    Kind      string `yaml:"kind"`       // "wall" or "portal"
    Texture   string `yaml:"texture"`
    Collision *bool  `yaml:"collision"`  // nil = default true
    ToSector  string `yaml:"to_sector"`
}

type RawSector struct {
    ID      string     `yaml:"id"`
    Floor   RawFloor   `yaml:"floor"`
    Ceiling RawCeiling `yaml:"ceiling"`
    Edges   []RawEdge  `yaml:"edges"`
}

type RawChunk struct {
    ID      string              `yaml:"id"`
    Points  map[string]RawPoint `yaml:"points"`
    Sectors []RawSector         `yaml:"sectors"`
}

type RawEntity struct {
    ID          string   `yaml:"id"`
    Position    RawPoint `yaml:"position"` // X and Z; use Y field for initial height
    ModelPath   string   `yaml:"model"`
    TexturePath string   `yaml:"texture"`
    Bounding    string   `yaml:"bounding"`
    BoundHalfX  float32  `yaml:"bound_half_x"`
    BoundHalfY  float32  `yaml:"bound_half_y"`
    BoundHalfZ  float32  `yaml:"bound_half_z"`
}

type RawLevel struct {
    Chunks   []RawChunk  `yaml:"chunks"`
    Entities []RawEntity `yaml:"entities"`
}
```

### `LoadLevel()` rewrite

```go
func LoadLevel(levelPath string) (Level, error) {
    data, err := os.ReadFile(levelPath)
    if err != nil {
        return Level{}, err
    }
    var raw RawLevel
    if err := yaml.Unmarshal(data, &raw); err != nil {
        return Level{}, err
    }
    return buildLevel(raw)
}

func buildLevel(raw RawLevel) (Level, error) {
    var level Level

    for _, rc := range raw.Chunks {
        chunk, err := buildChunk(rc)
        if err != nil {
            return Level{}, err
        }
        level.Chunks = append(level.Chunks, chunk)
    }

    for _, re := range raw.Entities {
        level.Entities = append(level.Entities, buildEntity(re))
    }

    return level, nil
}
```

### `buildChunk()`

```go
func buildChunk(rc RawChunk) (Chunk, error) {
    // resolve named points into mgl32.Vec2 map
    pts := map[string]mgl32.Vec2{}
    for name, rp := range rc.Points {
        pts[name] = mgl32.Vec2{rp.X, rp.Y}
    }

    chunk := Chunk{ID: rc.ID}
    minBound := mgl32.Vec3{math.MaxFloat32, math.MaxFloat32, math.MaxFloat32}
    maxBound := mgl32.Vec3{-math.MaxFloat32, -math.MaxFloat32, -math.MaxFloat32}

    for _, rs := range rc.Sectors {
        sector, err := buildSector(rs, pts)
        if err != nil {
            return Chunk{}, err
        }
        // expand chunk AABB
        minBound[0] = min32(minBound[0], sector.MinBound[0])
        minBound[2] = min32(minBound[2], sector.MinBound[1])
        maxBound[0] = max32(maxBound[0], sector.MaxBound[0])
        maxBound[2] = max32(maxBound[2], sector.MaxBound[1])
        // Y bounds come from floor/ceiling heights
        if sector.Floor.Enabled {
            minBound[1] = min32(minBound[1], sector.Floor.Height)
        }
        if sector.Ceiling.Enabled {
            maxBound[1] = max32(maxBound[1], sector.Ceiling.Height)
        }
        chunk.Sectors = append(chunk.Sectors, sector)
    }

    chunk.MinBound = minBound
    chunk.MaxBound = maxBound
    return chunk, nil
}
```

### `buildSector()`

This is the critical function that creates all GPU geometry:

```go
func buildSector(rs RawSector, pts map[string]mgl32.Vec2) (Sector, error) {
    sector := Sector{
        ID: rs.ID,
        Floor: Floor{
            Enabled: rs.Floor.Enabled,
            Height:  rs.Floor.Height,
            Texture: rs.Floor.Texture,
            Terrain: rs.Floor.Terrain,
        },
        Ceiling: Ceiling{
            Enabled: rs.Ceiling.Enabled,
            Height:  rs.Ceiling.Height,
            Texture: rs.Ceiling.Texture,
        },
    }

    floorH := rs.Floor.Height
    ceilH := rs.Ceiling.Height

    // Collect ordered sector polygon points from edges
    // (edge order in YAML defines the polygon traversal order)
    seenPoints := map[string]bool{}
    for _, edge := range rs.Edges {
        if !seenPoints[edge.From] {
            seenPoints[edge.From] = true
            p := pts[edge.From]
            sector.Points = append(sector.Points, p)
        }
    }

    // Compute sector XZ AABB
    sector.MinBound = sector.Points[0]
    sector.MaxBound = sector.Points[0]
    for _, p := range sector.Points[1:] {
        if p[0] < sector.MinBound[0] { sector.MinBound[0] = p[0] }
        if p[1] < sector.MinBound[1] { sector.MinBound[1] = p[1] }
        if p[0] > sector.MaxBound[0] { sector.MaxBound[0] = p[0] }
        if p[1] > sector.MaxBound[1] { sector.MaxBound[1] = p[1] }
    }

    // Build walls and portals from edges
    for _, edge := range rs.Edges {
        start := pts[edge.From]
        end := pts[edge.To]

        if edge.Kind == "portal" {
            sector.Portals = append(sector.Portals, Portal{
                StartPoint: start,
                EndPoint:   end,
                ToSector:   edge.ToSector,
            })
            continue
        }

        // Wall: build a quad (two triangles) from floor to ceiling
        // Vertices in world space — model matrix stays identity
        // Layout (viewed from inside the sector):
        //  A(start,ceil) --- B(end,ceil)
        //       |         /       |
        //  D(start,floor) --- C(end,floor)
        //
        // Triangle 1: A, B, C
        // Triangle 2: A, C, D
        ax, az := start[0], start[1]
        bx, bz := end[0], end[1]

        wallVerts := [][]float32{
            {ax, ceilH, az,  0.0, 0.0}, // A
            {bx, ceilH, bz,  1.0, 0.0}, // B
            {bx, floorH, bz, 1.0, 1.0}, // C
            {ax, floorH, az, 0.0, 1.0}, // D
        }

        collision := true
        if edge.Collision != nil {
            collision = *edge.Collision
        }

        wall := Wall{
            StartPoint: start,
            EndPoint:   end,
            Texture:    edge.Texture,
            Collision:  collision,
        }
        if err := wall.Geometry.Load(wallVerts, []int{0, 1, 2, 0, 2, 3}, edge.Texture, mgl32.Vec3{}); err != nil {
            return Sector{}, err
        }
        sector.Walls = append(sector.Walls, wall)
    }

    // Build floor geometry (triangle fan from vertex 0)
    if rs.Floor.Enabled {
        floorVerts := buildPolygonGeometry(sector.Points, floorH, false)
        if err := sector.Floor.Geometry.Load(floorVerts, identityIndices(len(floorVerts)), rs.Floor.Texture, mgl32.Vec3{}); err != nil {
            return Sector{}, err
        }
    }

    // Build ceiling geometry (triangle fan, flipped winding)
    if rs.Ceiling.Enabled {
        ceilVerts := buildPolygonGeometry(sector.Points, ceilH, true)
        if err := sector.Ceiling.Geometry.Load(ceilVerts, identityIndices(len(ceilVerts)), rs.Ceiling.Texture, mgl32.Vec3{}); err != nil {
            return Sector{}, err
        }
    }

    return sector, nil
}
```

### Helper functions

```go
// buildPolygonGeometry tessellates a convex polygon as a triangle fan.
// Points are in XZ plane at height y.
// flip reverses winding for ceilings (so normals face down into the room).
func buildPolygonGeometry(points []mgl32.Vec2, y float32, flip bool) [][]float32 {
    var verts [][]float32
    n := len(points)
    for i := 1; i < n-1; i++ {
        a := points[0]
        b := points[i]
        c := points[i+1]
        if flip {
            b, c = c, b
        }
        verts = append(verts,
            []float32{a[0], y, a[1], a[0], a[1]},
            []float32{b[0], y, b[1], b[0], b[1]},
            []float32{c[0], y, c[1], c[0], c[1]},
        )
    }
    return verts
}

// identityIndices returns [0, 1, 2, 3, ...] for pre-expanded triangle lists.
func identityIndices(n int) []int {
    idx := make([]int, n)
    for i := range idx {
        idx[i] = i
    }
    return idx
}
```

> **Note on `Geometry.Load()` signature change**: The current signature is `Load(vertices [][]float32, indices []int, texture string, position mgl32.Vec3)`. Since geometry is now in world space, `position` is always `mgl32.Vec3{}`. The function already exists and works; just use it as-is.

### Texture loading

In `main.go`, replace the hardcoded `textureNames` map with a dynamic collection. After `buildLevel()` resolves all textures, call `LoadTexture()` for each unique name. Add a helper to `level.go`:

```go
func (l *Level) CollectTextures() []string {
    seen := map[string]bool{}
    for _, chunk := range l.Chunks {
        for _, sector := range chunk.Sectors {
            if sector.Floor.Texture != "" { seen[sector.Floor.Texture] = true }
            if sector.Ceiling.Texture != "" { seen[sector.Ceiling.Texture] = true }
            for _, wall := range sector.Walls { seen[wall.Texture] = true }
        }
    }
    for _, e := range l.Entities {
        if e.TexturePath != "" { seen[e.TexturePath] = true }
    }
    var out []string
    for k := range seen {
        out = append(out, k)
    }
    return out
}
```

In `main.go`, replace the texture loading loop with:

```go
// textureFiles maps logical name → path relative to executablePath
var textureFiles = map[string]string{
    "square": "textures/square.png",
    // add more as needed
}

for _, name := range level.CollectTextures() {
    path, ok := textureFiles[name]
    if !ok {
        panic(fmt.Sprintf("no path registered for texture %q", name))
    }
    if err := LoadTexture(name, fmt.Sprintf("%s/%s", executablePath, path)); err != nil {
        panic(err)
    }
}
```

---

## Phase 3 — Rendering Pipeline

### `main.go` draw loop

Replace the flat `for _, wall := range level.Walls` loop with the hierarchical traversal:

```go
for _, chunk := range level.Chunks {
    for i := range chunk.Sectors {
        s := &chunk.Sectors[i]
        if s.Floor.Enabled {
            s.Floor.Geometry.Draw()
        }
        if s.Ceiling.Enabled {
            s.Ceiling.Geometry.Draw()
        }
        for j := range s.Walls {
            s.Walls[j].Geometry.Draw()
        }
        // portals: no draw call
    }
}
```

Remove the now-unused global `var Walls = []Wall{}` from `main.go`.

### `Geometry.Draw()` final form

After Phase 0 fixes and the model-matrix change, the final `Draw()` should be:

```go
func (g Geometry) Draw() {
    if len(g.Vertices) == 0 {
        return
    }
    model := mgl32.Ident4()
    gl.UseProgram(Program)
    gl.UniformMatrix4fv(g.ModelUniform, 1, false, &model[0])
    gl.BindVertexArray(g.Vao)
    gl.ActiveTexture(gl.TEXTURE0)
    gl.BindTexture(gl.TEXTURE_2D, Textures[g.Texture])
    gl.DrawArrays(gl.TRIANGLES, 0, int32(len(g.Vertices)/5))
}
```

---

## Phase 4 — Entity System + OBJ Loading + Camera Attachment

### New file `obj.go`

```go
package main

import (
    "bufio"
    "fmt"
    "os"
    "strconv"
    "strings"
)

// LoadOBJ parses a Wavefront .obj file and returns a flat vertex buffer
// in [X,Y,Z,U,V, ...] format, fully expanded (no index buffer).
func LoadOBJ(path string) ([]float32, error) {
    f, err := os.Open(path)
    if err != nil {
        return nil, err
    }
    defer f.Close()

    var positions [][3]float32
    var uvs [][2]float32
    var out []float32

    scanner := bufio.NewScanner(f)
    for scanner.Scan() {
        line := strings.TrimSpace(scanner.Text())
        if line == "" || strings.HasPrefix(line, "#") {
            continue
        }
        parts := strings.Fields(line)
        switch parts[0] {
        case "v":
            x, _ := strconv.ParseFloat(parts[1], 32)
            y, _ := strconv.ParseFloat(parts[2], 32)
            z, _ := strconv.ParseFloat(parts[3], 32)
            positions = append(positions, [3]float32{float32(x), float32(y), float32(z)})
        case "vt":
            u, _ := strconv.ParseFloat(parts[1], 32)
            v, _ := strconv.ParseFloat(parts[2], 32)
            uvs = append(uvs, [2]float32{float32(u), float32(v)})
        case "f":
            // triangulate: fan from first vertex
            faceVerts := parts[1:]
            for i := 1; i < len(faceVerts)-1; i++ {
                for _, fv := range []string{faceVerts[0], faceVerts[i], faceVerts[i+1]} {
                    tokens := strings.Split(fv, "/")
                    pi, _ := strconv.Atoi(tokens[0])
                    pi--
                    var ui int
                    if len(tokens) > 1 && tokens[1] != "" {
                        ui, _ = strconv.Atoi(tokens[1])
                        ui--
                    }
                    p := positions[pi]
                    var u, v float32
                    if ui >= 0 && ui < len(uvs) {
                        u, v = uvs[ui][0], uvs[ui][1]
                    }
                    out = append(out, p[0], p[1], p[2], u, v)
                }
            }
        }
    }
    if err := scanner.Err(); err != nil {
        return nil, fmt.Errorf("LoadOBJ %s: %w", path, err)
    }
    return out, nil
}
```

### `entity.go` — add `Load()` method

```go
func (e *Entity) Load(executablePath string) error {
    if e.ModelPath == "" {
        return nil // invisible entity (e.g. player in first-person)
    }
    verts, err := LoadOBJ(fmt.Sprintf("%s/%s", executablePath, e.ModelPath))
    if err != nil {
        return err
    }
    // Wrap into [][]float32 for Geometry.Load
    wrapped := make([][]float32, len(verts)/5)
    for i := range wrapped {
        wrapped[i] = verts[i*5 : i*5+5]
    }
    return e.Geometry.Load(wrapped, identityIndices(len(wrapped)), e.TexturePath, mgl32.Vec3{})
}
```

### `buildEntity()` helper in `level.go`

```go
func buildEntity(re RawEntity) Entity {
    var bt BoundingType
    switch re.Bounding {
    case "sphere":
        bt = BoundingSphere
    case "cylinder":
        bt = BoundingCylinder
    default:
        bt = BoundingAABB
    }
    return Entity{
        ID:          re.ID,
        Position:    mgl32.Vec3{re.Position.X, 0.0, re.Position.Y}, // Y from yaml = Z in world
        ModelPath:   re.ModelPath,
        TexturePath: re.TexturePath,
        BoundingKind: bt,
        BoundHalfX:  re.BoundHalfX,
        BoundHalfY:  re.BoundHalfY,
        BoundHalfZ:  re.BoundHalfZ,
        Visible:     re.ModelPath != "",
    }
}
```

### Camera attachment in `main.go`

After `LoadLevel()`, find the player and use its position as the camera start:

```go
var Player *Entity
for i := range level.Entities {
    if level.Entities[i].ID == "player" {
        Player = &level.Entities[i]
        break
    }
}

// Load entity GPU resources
executablePath := filepath.Dir(executable) // already computed above
for i := range level.Entities {
    if err := level.Entities[i].Load(executablePath); err != nil {
        panic(err)
    }
}

// Initialize camera at player position
startPos := mgl32.Vec3{0, 1.0, 0} // fallback
if Player != nil {
    startPos = Player.Position.Add(mgl32.Vec3{0, 0.9, 0}) // eye height
}
LoadCamera(startPos, 0.0, float32(math.Pi/2.0))
```

In `HandleInput()`, after `MoveCamera()`, sync the player position back:

```go
if Player != nil {
    Player.Position = Cam.Position.Sub(mgl32.Vec3{0, 0.9, 0})
}
```

Entity rendering in the draw loop (after world geometry):

```go
for i := range level.Entities {
    e := &level.Entities[i]
    if e.Visible {
        e.Geometry.Draw()
    }
}
```

---

## Phase 5 — Collision Detection

Create a new file `collision.go`.

### Sector lookup

```go
// FindChunk returns the first chunk whose 3D AABB contains pos, or nil.
func FindChunk(chunks []Chunk, pos mgl32.Vec3) *Chunk {
    for i := range chunks {
        c := &chunks[i]
        if pos[0] >= c.MinBound[0] && pos[0] <= c.MaxBound[0] &&
            pos[1] >= c.MinBound[1] && pos[1] <= c.MaxBound[1] &&
            pos[2] >= c.MinBound[2] && pos[2] <= c.MaxBound[2] {
            return c
        }
    }
    return nil
}

// FindSector returns the first sector whose polygon contains xz, or nil.
func FindSector(chunk *Chunk, xz mgl32.Vec2) *Sector {
    if chunk == nil {
        return nil
    }
    for i := range chunk.Sectors {
        if PointInPolygon(chunk.Sectors[i].Points, xz) {
            return &chunk.Sectors[i]
        }
    }
    return nil
}

// PointInPolygon uses ray-casting to test if p is inside polygon.
func PointInPolygon(poly []mgl32.Vec2, p mgl32.Vec2) bool {
    inside := false
    n := len(poly)
    j := n - 1
    for i := 0; i < n; i++ {
        xi, yi := poly[i][0], poly[i][1]
        xj, yj := poly[j][0], poly[j][1]
        if ((yi > p[1]) != (yj > p[1])) &&
            (p[0] < (xj-xi)*(p[1]-yi)/(yj-yi)+xi) {
            inside = !inside
        }
        j = i
    }
    return inside
}
```

### Wall collision (slide response)

```go
// ResolveWallCollision adjusts newPos so the entity does not cross any wall.
// Returns the corrected position.
func ResolveWallCollision(entity *Entity, sector *Sector, newPos mgl32.Vec3) mgl32.Vec3 {
    if sector == nil {
        return newPos
    }
    xz := mgl32.Vec2{newPos[0], newPos[2]}
    oldXZ := mgl32.Vec2{entity.Position[0], entity.Position[2]}

    for _, wall := range sector.Walls {
        if !wall.Collision {
            continue
        }
        hit, t := SegmentIntersect2D(oldXZ, xz, wall.StartPoint, wall.EndPoint)
        if !hit || t < 0 || t > 1 {
            continue
        }
        // Project movement onto wall tangent (slide)
        wallDir := wall.EndPoint.Sub(wall.StartPoint).Normalize()
        move := xz.Sub(oldXZ)
        dot := move.Dot(wallDir)
        xz = oldXZ.Add(wallDir.Mul(dot))
    }

    return mgl32.Vec3{xz[0], newPos[1], xz[1]}
}

// SegmentIntersect2D tests if segment (a1→a2) intersects (b1→b2).
// Returns (intersects, t) where t is the parameter along a1→a2.
func SegmentIntersect2D(a1, a2, b1, b2 mgl32.Vec2) (bool, float32) {
    d := a2.Sub(a1)
    e := b2.Sub(b1)
    denom := d[0]*e[1] - d[1]*e[0]
    if abs32(denom) < 1e-6 {
        return false, 0
    }
    f := b1.Sub(a1)
    t := (f[0]*e[1] - f[1]*e[0]) / denom
    u := (f[0]*d[1] - f[1]*d[0]) / denom
    return u >= 0 && u <= 1, t
}

func abs32(x float32) float32 {
    if x < 0 { return -x }
    return x
}
```

### Floor collision

```go
// ResolveFloorCollision clamps the entity above the sector floor.
func ResolveFloorCollision(entity *Entity, sector *Sector, pos *mgl32.Vec3) {
    if sector == nil || !sector.Floor.Enabled {
        return
    }
    minY := sector.Floor.Height + entity.BoundHalfY
    if pos[1] < minY {
        pos[1] = minY
    }
}
```

### Entity-entity broad phase

```go
func AABBOverlap(a, b *Entity) bool {
    return abs32(a.Position[0]-b.Position[0]) < a.BoundHalfX+b.BoundHalfX &&
        abs32(a.Position[1]-b.Position[1]) < a.BoundHalfY+b.BoundHalfY &&
        abs32(a.Position[2]-b.Position[2]) < a.BoundHalfZ+b.BoundHalfZ
}
```

### Integration in the game loop (`main.go`)

Add a delta-time variable. Update the game loop:

```go
var lastTime uint64 = sdl.GetPerformanceCounter()

for Running {
    now := sdl.GetPerformanceCounter()
    dt := float32(now-lastTime) / float32(sdl.GetPerformanceFrequency())
    lastTime = now

    sdl.PumpEvents()
    KeyStates = sdl.GetKeyboardState()
    HandleInput(KeyStates, dt)  // pass dt so movement scales with time
    // ... event polling ...

    if Player != nil {
        newPos := Player.Position.Add(Player.Velocity.Mul(dt))
        chunk := FindChunk(level.Chunks, newPos)
        sector := FindSector(chunk, mgl32.Vec2{newPos[0], newPos[2]})
        newPos = ResolveWallCollision(Player, sector, newPos)
        ResolveFloorCollision(Player, sector, &newPos)
        Player.Position = newPos
        // keep camera in sync
        Cam.Position = Player.Position.Add(mgl32.Vec3{0, 0.9, 0})
    }

    // ... draw calls ...
}
```

> **Note**: `HandleInput` currently moves the camera directly via `MoveCamera()`. Once collision is in place, refactor it to write a velocity into `Player.Velocity` instead, and remove the direct `Cam.Position` mutation from `MoveCamera()`.

---

## Phase 6 — Frustum Culling

Create a new file `culling.go`.

```go
package main

import "github.com/go-gl/mathgl/mgl32"

type Frustum struct {
    Planes [6]mgl32.Vec4 // left, right, bottom, top, near, far
}

// ExtractFrustum extracts frustum planes from a combined projection*view matrix
// using the Gribb/Hartmann method.
func ExtractFrustum(m mgl32.Mat4) Frustum {
    var f Frustum
    // Each plane: row combination of the matrix columns
    // OpenGL uses column-major; m[col][row]
    f.Planes[0] = mgl32.Vec4{m[3] + m[0], m[7] + m[4], m[11] + m[8],  m[15] + m[12]} // left
    f.Planes[1] = mgl32.Vec4{m[3] - m[0], m[7] - m[4], m[11] - m[8],  m[15] - m[12]} // right
    f.Planes[2] = mgl32.Vec4{m[3] + m[1], m[7] + m[5], m[11] + m[9],  m[15] + m[13]} // bottom
    f.Planes[3] = mgl32.Vec4{m[3] - m[1], m[7] - m[5], m[11] - m[9],  m[15] - m[13]} // top
    f.Planes[4] = mgl32.Vec4{m[3] + m[2], m[7] + m[6], m[11] + m[10], m[15] + m[14]} // near
    f.Planes[5] = mgl32.Vec4{m[3] - m[2], m[7] - m[6], m[11] - m[10], m[15] - m[14]} // far
    return f
}

// AABBInFrustum returns true if the AABB (min, max) intersects the frustum.
func AABBInFrustum(f Frustum, min, max mgl32.Vec3) bool {
    for _, p := range f.Planes {
        // positive vertex: the AABB corner most in the direction of the plane normal
        px := max[0]
        if p[0] < 0 { px = min[0] }
        py := max[1]
        if p[1] < 0 { py = min[1] }
        pz := max[2]
        if p[2] < 0 { pz = min[2] }
        if p[0]*px+p[1]*py+p[2]*pz+p[3] < 0 {
            return false // entirely outside this plane
        }
    }
    return true
}

// WallFacingCamera returns true if the wall's inward normal faces toward camPos.
func WallFacingCamera(wall Wall, camPos mgl32.Vec3) bool {
    mid := mgl32.Vec2{
        (wall.StartPoint[0] + wall.EndPoint[0]) * 0.5,
        (wall.StartPoint[1] + wall.EndPoint[1]) * 0.5,
    }
    // Outward normal: 90° CW rotation of the edge direction
    dir := wall.EndPoint.Sub(wall.StartPoint)
    normal := mgl32.Vec2{dir[1], -dir[0]}
    toCam := mgl32.Vec2{camPos[0] - mid[0], camPos[2] - mid[1]}
    return normal.Dot(toCam) > 0
}
```

### Updated draw loop with culling

```go
projView := projection.Mul4(Cam.Matrix)
frustum := ExtractFrustum(projView)

for ci := range level.Chunks {
    chunk := &level.Chunks[ci]
    if !AABBInFrustum(frustum, chunk.MinBound, chunk.MaxBound) {
        continue
    }
    for si := range chunk.Sectors {
        s := &chunk.Sectors[si]
        sMin := mgl32.Vec3{s.MinBound[0], s.Floor.Height, s.MinBound[1]}
        sMax := mgl32.Vec3{s.MaxBound[0], s.Ceiling.Height, s.MaxBound[1]}
        if !AABBInFrustum(frustum, sMin, sMax) {
            continue
        }
        if s.Floor.Enabled   { s.Floor.Geometry.Draw() }
        if s.Ceiling.Enabled { s.Ceiling.Geometry.Draw() }
        for wi := range s.Walls {
            w := &s.Walls[wi]
            if WallFacingCamera(*w, Cam.Position) {
                w.Geometry.Draw()
            }
        }
    }
}
```

To access `projection` inside the game loop, store it as a variable declared before the loop (it already is — just make sure it's in scope).

---

## Summary: Files to Create / Modify

| File | Action |
|------|--------|
| `wall.go` | Fix Draw() receiver bug, fix Vertices assignment, dynamic vertex count, world-space model matrix |
| `sector.go` | Complete rewrite with Floor/Ceiling/Sector structs |
| `portal.go` | Replace stub with full Portal struct |
| `level.go` | Complete rewrite: new Level struct, raw YAML structs, LoadLevel, buildLevel/Chunk/Sector/Entity helpers |
| `main.go` | Update draw loop, texture loading, player entity setup, delta time, frustum extraction |
| `graphics.go` | Add `\r\n` stripping |
| `chunk.go` | **New** — Chunk struct |
| `entity.go` | **New** — Entity struct + Load() |
| `obj.go` | **New** — LoadOBJ() |
| `collision.go` | **New** — FindChunk, FindSector, PointInPolygon, ResolveWall/Floor, AABBOverlap |
| `culling.go` | **New** — Frustum, ExtractFrustum, AABBInFrustum, WallFacingCamera |
| `resources/levels/test.yaml` | Migrate to new schema (chunks wrapper, ceiling fields, entities) |

## Implementation Order

1. Phase 0 — bug fixes (gets the engine compiling and drawing the old walls)
2. Phase 1 — data structure refactor (structs only, no new rendering yet)
3. Phase 2 — YAML parser rewrite + update test.yaml
4. Phase 3 — update draw loop; verify floors, ceilings, walls render
5. Phase 4 — entity system + OBJ loading + camera attachment
6. Phase 5 — collision detection
7. Phase 6 — frustum culling

Phases 5 and 6 are independent of each other and can be done in either order after Phase 4.
