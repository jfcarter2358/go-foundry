# Foundry Scripting Language
## About
Foundry scripts (flang) function around a central event loop and message box setup. This means that functions can either send a message to an entity/sector/chunk/wall/floor/ceiling/volumes/lights which that item then acts upon. A breakdown of the syntax is as follows:

## Types
- int (integer)
- float (floating point number, must include a decimal (e.g. 0.123, 9.456))
- str (string, must be enclosed in single quotes)
- bool (boolean, `true` or `false`)
- dict (map of string keys to values of any type, format is {"key": val, ...})
- list (list of values, any type, format is [val, val, val, ...])
- atom (literal string to act as essentially an enum, consist of only lowercase letters and underscores, referened with colons on either side, for example the atom `damage` would be referenced as `:damage:`)
- tuple (static-length collection of items. defined by wrapping the values in parenthesis not right behind a function name, `(element1, element2, ...)`)
- vec (three-tuple of floats with built-in vector operations. Defined using `<x, y, z>`)

## Function definitions

function_name(arg1 type, arg2 type, ...) -> (type1, type2, ...)
    <function code goes here>
end

## Pattern matching

flang uses pattern matching to determine which function to call in the same manner as Erlang. For example, to implement the fibonnaci sequence, you would do the following:

```
def fib(n int) -> int
    return n + fib(n - 1)
end
def fib(0) -> int
    return 1
end
```

In the event that multiple definitions of the same function signature are present with no whitespace between them, this can be collapsed to

```
def fib(n int) -> int
    return n + fib(n - 1)
fib(0)
    return 1
end
```

The strictest case is what ends up being matched, for example in the above example if `n` is `0` then `fib(0)` is matched over `fib(n int)`

Additionally, atoms can be used as arguments to match and do not require a type signature. For example, to listen to the `interact` event, the function would look like

```
def interaction(:interact:, eid int) -> ...
    ...
end
```

## Events

Events are emitted via a message box attached to each object. The following event atoms are supported OOTB:

- `enter` -- element entered the bounding box
- `exit` -- element exited the bounding box
- `collide` -- collision between two objects that is resolved so they no longer collide
- `interact` -- interaction key was held while within the bounding box

Users can additionally define custom atoms to be used for event listening. For example, if we want to implement a `damage` function, we would define a function like

```
def do_damage(:damage:, eid int, amount int) -> null
    self.health -= amount
end
```

which can then be triggered via

```
def something(...) -> ...
    @<some id> <- (:damage:, @self.id, 20) -- This passes the arguments to the object with that ID which then matches against the defined functions for that object and fires if anything matches
end
```

Keywords to know about, `@self` is a reference to the object that the script is attached to

Additionally, you can reference an object with an idvia the `@<id>` format, you can also reference all objects with a specific tag via the `#<tag>` format, for example to send a message to all objects with the `foobar` tag you would use `#foobar <- (...)`

All events MUST have an atom as their first argument to determine the event type as well as an entity ID that triggered the event as their second argument

## Entity attributes

Entities carry user-defined **attributes** — see DESIGN.md — and the four types
they can have are deliberately the four scalar types above: `int`, `float`,
`bool`, `str`. An attribute never has to be converted on its way into a script,
and there is no type an author can write in a level that the language will not
understand. The compound types (`dict`, `list`, `atom`) are things scripts build
at runtime rather than things a level authors.

An entity's IDs and tags are what the addressing above resolves through: `@id`
finds the one entity with that ID, `#tag` finds every entity carrying that tag.
Both are authored — the ID on the instance, the tags on the definition it came
from, plus any the instance adds.

## What scripts do not have to do

Solidity is a property of an entity type, not something a script arranges: an
entity's definition carries `collision: {world, entities, gravity, movable}`
flags and the engine honours them every frame (see DESIGN.md). A script that
wants a crate to stop at a wall does not write the stopping — it sets the flag
once on the type, or reads `on_ground` to know the crate has landed.

What is still the script's own is *reacting*: the `enter`, `exit` and `collide`
events above are what turn a resolved overlap into game behaviour.

To access an attribute of an entity, the syntax `@id.attr.<attribute name>`

Attributes can be changed from within scripts via assignment, e.g. `@id.attr.foo = "bar"`

Tags can also be used to bulk-change attributes for objects, e.g. `#tag.attr.foo = "bar"` sets the `foo` attribute to `bar` for all entities with the tag `tag`

## Properties

Objects also have intrinsic properties assigned to them (e.g. wall visibility, entity collision enabled, etc.) that can be accessed/changed with `@id.prop.<property name>`

Properties can be changed from within scripts via assignment, e.g. `@id.prop.foo = "bar"`

Tags can also be used to bulk-change properties for objects, e.g. `#tag.prop.foo = "bar"` sets the `foo` property to `bar` for all entities with the tag `tag`

## Importing

It may be useful to separate scripts at times and organize them into folders. To import a file into another, use `import(<relative path to other file>)`

## Other

Strings can be concatenated via addition, so `"foo" + "bar"` gives `foobar`

Additionally, standard math and string manipulation functions are to be built into the language.

## Sectors, ceiling, floors, and walls

If a sector triggers an event it passes its ID as normal. Once passed, its child items (e.g. ceiling, floor, walls) can be accessed with

```
@<sector ID>.ceil -- ceiling
@<sector ID>.floor -- floor
@<sector ID>.wall.@<wall index> -- specific wall
```
