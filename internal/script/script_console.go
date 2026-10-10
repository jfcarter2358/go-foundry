package script

// script_console.go — the read-only window the developer console needs onto a
// level's names.
//
// internal/devui asks three questions that the script runtime already answers
// for `@name`, `#tag` and `distance`: what does this name address, what does
// this tag address, and where is it. Every one of those answers has a detail in
// it that is easy to get wrong somewhere else — a rider's position is in its
// frame's coordinates, a tag reaches nine kinds of object, a chunk's brush is
// addressed by a pair — so they are exported from here rather than reimplemented
// by the console.
//
// All four bind the runtime to lvl for the duration of the call and put back
// whatever was there, exactly as RunScriptValue does, because the answers come
// from the live host.

import (
	"foundry/internal/flang"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// withLevel runs fn with the runtime pointed at lvl, and restores what it was
// pointed at before.
func withLevel[T any](lvl *world.Level, fn func(rt *ScriptRuntime) T) T {
	rt := Scripts
	prevLvl, prevCache := rt.lvl, rt.cache
	rt.lvl = lvl
	rt.cache = buildRefCache(lvl)
	if rt.host == nil {
		rt.host = &levelHost{rt: rt}
	}
	defer func() {
		rt.lvl, rt.cache = prevLvl, prevCache
	}()
	return fn(rt)
}

// LevelTags is every tag anything in lvl carries, sorted and without repeats.
func LevelTags(lvl *world.Level) []string {
	return withLevel(lvl, func(rt *ScriptRuntime) []string { return rt.cache.allTags() })
}

// TaggedRefs is what `#tag` addresses in lvl, in the order `find` yields it.
func TaggedRefs(lvl *world.Level, tag string) []flang.RefVal {
	return withLevel(lvl, func(rt *ScriptRuntime) []flang.RefVal { return rt.cache.refsWithTag(tag) })
}

// ResolveRef is what `@name` addresses in lvl, or false for a name the level
// does not have.
func ResolveRef(lvl *world.Level, name string) (flang.RefVal, bool) {
	type result struct {
		r  flang.RefVal
		ok bool
	}
	got := withLevel(lvl, func(rt *ScriptRuntime) result {
		r, ok := rt.host.Resolve(name)
		return result{r, ok}
	})
	return got.r, got.ok
}

// LevelNames is every ID `@name` can address in lvl, sorted and without
// repeats. What the console's tab completion offers.
func LevelNames(lvl *world.Level) []string {
	return withLevel(lvl, func(rt *ScriptRuntime) []string {
		c := rt.cache
		seen := map[string]bool{}
		for _, k := range []ixKind{
			ixEntity, ixVolume, ixSprite, ixEmitter, ixSound, ixEffect, ixChunk, ixDirLight, ixPtLight,
		} {
			for k := range c.ix(k).m {
				seen[k] = true
			}
		}
		for k := range c.brush {
			seen[k] = true
		}
		out := make([]string, 0, len(seen))
		for k := range seen {
			out = append(out, k)
		}
		sortStringsAsc(out)
		return out
	})
}

// RefPosition is where a ref is in the world — carried out of whatever frame it
// rides, so that two of these can be subtracted.
func RefPosition(lvl *world.Level, r flang.RefVal) (mgl32.Vec3, bool) {
	type result struct {
		p  mgl32.Vec3
		ok bool
	}
	got := withLevel(lvl, func(rt *ScriptRuntime) result {
		p, ok := rt.host.refPosition(r)
		return result{p, ok}
	})
	return got.p, got.ok
}
