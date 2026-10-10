package ai

// aim_assist.go — what a controller player's aim assist may settle on.
//
// The assist itself (app/pad_look.go) is a view matter; which bodies count is
// the controller's, because "hostile" here means exactly what the agents
// already say it means: an agent that hunts the player. Its Targets name the
// player (directly, by ID or by a tag the player carries), or it has picked
// the player as its target now. A friendly squad following the player is
// none of those, and an agent a combat script has switched off (`ai_enabled =
// false`, how a body is dead) is not targetable at all.

import (
	"strings"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// AimTarget is one body aim assist may pull toward.
type AimTarget struct {
	ID string
	// Point is centre mass: where a shot is wanted, and what line of sight is
	// tested against.
	Point mgl32.Vec3
	// Radius is half the body's width, so a wide target catches a wider cone.
	Radius float32
}

// HostileToPlayer reports whether an agent hunts the player.
func HostileToPlayer(e, player *world.Entity) bool {
	if e == nil || e == player || !e.AIDriven() || !e.Visible {
		return false
	}
	if e.AIState != nil && e.AIState.Target == "player" {
		return true
	}
	for _, t := range e.AI.Targets {
		t = strings.TrimSpace(t)
		switch {
		case t == "player" || t == "@player":
			return true
		case strings.HasPrefix(t, "#"):
			if player != nil && player.HasTag(t[1:]) {
				return true
			}
		case player != nil && strings.TrimPrefix(t, "@") == player.ID:
			return true
		}
	}
	return false
}

// AimTargets appends every hostile agent's body to dst.
func AimTargets(lvl *world.Level, player *world.Entity, dst []AimTarget) []AimTarget {
	if lvl == nil {
		return dst
	}
	for i := range lvl.Entities {
		e := &lvl.Entities[i]
		if !HostileToPlayer(e, player) {
			continue
		}
		r := float32(0.35)
		if e.HasBounds() {
			h := e.BoundHalfExtents()
			r = maxf(h.X(), h.Z())
		}
		dst = append(dst, AimTarget{ID: e.ID, Point: bodyPoint(lvl, e, 0.65), Radius: r})
	}
	return dst
}
