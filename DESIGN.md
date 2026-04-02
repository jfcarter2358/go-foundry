# Go-Foundry

## Engine Design

The go-foundry engine takes a doom/quake approach to a game engine. The world consists of the following structure:

objects:
  - name: chunk
    description: Chunks are containers for a group of sectors. They have a bounding box enclosing the entire thing which is used to dictate if an entity should calculate any sort of collision checks against the items inside of that chunk
    children:
      - name: sector
        description: A sector is a 2d map of connected convex polygons. The edges of the polygons are either walls, or portals (which are essentially empty walls, not displayed and no collision checks. They connect two sectors together). The sector can have a ceiling and a floor (though they are optional) and the ceiling point positions can be modified to create sloped walls and such. Each wall, floor, and ceiling of the sector can have different textures assigned to them
        children:
          - name: wall
            description: A wall connects a ceiling to a floor along the edge of a sector. Walls can be textured and optionally (though by default yes) be included in collision checks
          - name: portal
            description: Portals do not have collision or any sort of display and are used to connect sectors together
          - name: ceiling
            description: The ceiling is a convex polygon created by following the edges of the sector along the top which can be optionally displayed and optionally checked for collision
          - name: floor
            description: The floor is a convex polygon created by following the edges of the sector along the bottom which can be optionally displayed and optionally checked for collision. Additionally, floors can have the `terrain` attribute set to `true` which means that they now have a web of triangles inside of them that can have their points manipulated to create varied terrain within the sector.
  - name: entity
    description: Entities are objects that are dynamic in the scene, so the player, enemies, boxes, etc.. These are able to have scripts attached to them (scripting language to be defined later) as well as have their own bounding boxes which are used to determine collision checks. They contain a model and texture as well as the ability to move. Additionally, they can play animations on their models

## Level Composition

Levels are composed of chunks. These chunks can be thought of as floors of a house. They are ultimately "one level" each (though stairs and ramps can be included, you just cannot have a sector that goes over top of another). Chunks can then be combined together to create the larger level, so you might stack the "second floor" chunk on top of the "first floor" chunk to create a two-story house

Chunks are defined by their floor and ceiling sector maps, which are a 2d map defining the bottom (or top) sector definitions which consist of convex polygons connected. Where they connect can be a wall or a portal, portals are empty walls with no collision that serve to connect sectors. The height of the sector maps' points can be changed as well as their positions in the horizontal plane can be offset to create things like sloped walls

Sectors have textures and their positions are baked into an optimized BSP-tree for collision checks

## Game Loop

The main game loop will run physics/movement calculations in compute shaders wherever possible to improve performance. Additionally it will host the script event loop once that is eventually implemented.

## Rendering

Rendering should cull as many chunks/sectors/walls/entities as possible _before_ rendering. The goal is to make the engine as performant as possible. Models should use instancing wherever possible.ith 

## Collision Checking

Each entity contains an axis-aligned bounding box/cylinder/sphere that is used for collision checks If something is within that sphere then a more accurate collision check is done against the entities bounding model, which is a 3d model made up of primitives (boxes, spheres, cylinders) that are checked against for more accuracy. The detailed check can be disabled though if desired. First an entity checks what world chunk it is in. Once it knows that, it uses the chunk's collision BSP tree to determine its sector. If the sector has `terrain == true`, then a 'top-down' check is performed to figure out which terrain triangle the entity is in and then a collision check is performed against that triangle and any applicable edges. Additionally, entities colliding with each other first check if their rough bounding entities overlap, and if so then check their more detailed bounding models against each other (if enabled) to determine how they should interact with each other

## Level format

Levels should be exported as YAML files containing the entity, chunk, and sector information. Entities should reference the path to the .obj model files and their textures

## Scripting language

**Currently out of scope for v1, ignore**

## Animations

**Currently out of scope for v1, ignore**
