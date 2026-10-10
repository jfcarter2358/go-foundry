package world

// entity_diagnose.go — why an entity did not do what a script asked it to.
//
// `play_anim` reports success as a bool, and a bool is a terrible thing to be
// handed when the answer is no. There are four distinct
// ways for `play_anim(@button, "button", "button_press")` to come back false —
// no attachment by that name, an attachment that cannot animate at all, an
// animation the entity never declared, and a declared animation pointing at a
// clip the .fmdl does not have — and they have four different fixes, none of
// which is guessable from `false`.
//
// So the checks live here, once, in a form that returns *words*. Two callers
// need exactly the same answer and would otherwise each reimplement half of it:
//
//   - the verbs in script_actions.go, which log the reason when a call does
//     nothing, so a silent no-op becomes a line in the log window;
//   - the script linter (script_lint.go), which runs the same checks against
//     literal arguments before the script is ever run, so the problem is visible
//     in the editor rather than discovered by walking into the volume.
//
// Every function returns "" for "this would work", so a caller reads as
// `if why := …; why != "" { warn(why) }`.

import (
	"fmt"
	"foundry/internal/strutil"
	"sort"
	"strconv"
	"strings"
)

// AnimationProblem explains why `play_anim(e, model, animation)` would play
// nothing, or "" when it would play.
//
// An empty model means "every attachment", which succeeds if any one of them
// plays — so the problem is only reported when they all fail, and it names the
// reason each gave rather than a summary nobody can act on.
func (e *Entity) AnimationProblem(model, animation string) string {
	if e == nil {
		return "there is no such entity"
	}
	if animation == "" {
		return "no animation was named"
	}
	if model != "" {
		m := e.Model(model)
		if m == nil {
			return fmt.Sprintf("entity %q has no model attachment called %q; %s",
				e.ID, model, hasAttachments(e))
		}
		return m.AnimationProblem(animation)
	}

	if len(e.Models) == 0 {
		return fmt.Sprintf("entity %q has no model attachments, so there is nothing to animate", e.ID)
	}
	var why []string
	for i := range e.Models {
		reason := e.Models[i].AnimationProblem(animation)
		if reason == "" {
			return ""
		}
		why = append(why, reason)
	}
	if len(why) == 1 {
		return why[0]
	}
	return fmt.Sprintf("no attachment of entity %q plays %q: %s",
		e.ID, animation, strings.Join(why, "; "))
}

// AnimationProblem explains why this attachment would not play the named
// animation, or "" when it would.
//
// The order of the checks is the order the fixes come in, which is why "cannot
// animate at all" is asked before "has no animation by that name": telling
// someone to declare an animation on a model with no armature would send them
// to the wrong window.
func (m *EntityModel) AnimationProblem(animation string) string {
	if m == nil {
		return "there is no such attachment"
	}
	if m.Anim == nil {
		why := m.AnimBlocked
		if why == "" {
			// Load has not run, so nothing knows more than this.
			why = "the model has not been loaded, or has no armature"
		}
		return fmt.Sprintf("attachment %q cannot animate: %s", m.Name, why)
	}

	clips := m.Anim.ClipNames()
	a := m.Animation(animation)
	if a == nil {
		// The common one, and the one worth being long-winded about: a clip
		// authored in the model editor is not yet an animation the entity has.
		// The two lists are both shown because which one the name is in says
		// which of the two mistakes was made.
		return fmt.Sprintf(
			"attachment %q has no animation called %q — a clip in the .fmdl is not an animation until the entity declares it "+
				"(entity panel → the attachment → Animations → Add Animation). It declares %s; its model's clips are %s",
			m.Name, animation, namesOrNone(animationNames(m)), namesOrNone(clips))
	}
	if m.Anim.Doc == nil || m.Anim.Doc.ClipIndexByName(a.EffectiveClip()) < 0 {
		return fmt.Sprintf(
			"attachment %q maps animation %q to clip %q, which its model does not have; its clips are %s",
			m.Name, animation, a.EffectiveClip(), namesOrNone(clips))
	}
	return ""
}

// TextureProblem explains why writing an entity's or an attachment's `texture`
// property would reskin nothing, or "" when it would.
func (e *Entity) TextureProblem(model string) string {
	if e == nil {
		return "there is no such entity"
	}
	if model != "" {
		if e.Model(model) == nil {
			return fmt.Sprintf("entity %q has no model attachment called %q; %s",
				e.ID, model, hasAttachments(e))
		}
		return ""
	}
	if len(e.Models) == 0 {
		return fmt.Sprintf("entity %q has no model attachments, so there is nothing to reskin", e.ID)
	}
	return ""
}

// ---------- shared phrasing ----------

// attachmentNames lists an entity's attachments, in the order it holds them —
// which is the order the entity panel shows, so a message and the window agree.
func attachmentNames(e *Entity) []string {
	if e == nil {
		return nil
	}
	out := make([]string, 0, len(e.Models))
	for i := range e.Models {
		out = append(out, e.Models[i].Name)
	}
	return out
}

// animationNames lists the animations an attachment declares.
func animationNames(m *EntityModel) []string {
	if m == nil {
		return nil
	}
	out := make([]string, 0, len(m.Animations))
	for _, a := range m.Animations {
		out = append(out, a.Name)
	}
	return out
}

// hasAttachments renders the "it has …" half of a missing-attachment message.
func hasAttachments(e *Entity) string {
	if len(e.Models) == 0 {
		return "it has no attachments at all"
	}
	return "it has " + namesOrNone(attachmentNames(e))
}

// namesOrNone renders a list of names for a message, or "none" when it is empty.
func namesOrNone(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = strconv.Quote(n)
	}
	return strings.Join(quoted, ", ")
}

// NearestName returns the candidate closest to name, or "" when none is close
// enough to be worth suggesting. A wrong guess is worse than no guess, which is
// the same threshold didYouMean (script_host.go) uses for property names.
func NearestName(name string, candidates []string) string {
	best, bestDist := "", 3
	sorted := append([]string(nil), candidates...)
	sort.Strings(sorted) // deterministic when two candidates tie
	for _, cand := range sorted {
		if d := strutil.EditDistance(name, cand); d < bestDist {
			best, bestDist = cand, d
		}
	}
	return best
}

// ---------- attaching ----------

// BoneProblem explains why `attach(obj, e, model, bone)` would bind to nothing,
// or "" when it would bind.
//
// An empty model means "work it out", which is what the three-argument form of
// the verb asks for: an entity with one attachment has an unambiguous answer,
// and one with several does not — so that case is reported here rather than
// guessed at, since picking the wrong turret is worse than saying so.
//
// The armature is checked but the *pose* is not. m.Anim is runtime state that
// the editor's linter has no business requiring, and a bone with no pose falls
// back to the entity's own transform rather than to nothing — a graceful
// degradation, not a failure worth refusing the binding over.
func (e *Entity) BoneProblem(model, bone string) string {
	if e == nil {
		return "there is no such entity"
	}
	if bone == "" {
		return "no bone was named"
	}
	if model == "" {
		switch len(e.Models) {
		case 0:
			return fmt.Sprintf("entity %q has no model attachments, so it has no bones to ride", e.ID)
		case 1:
			model = e.Models[0].Name
		default:
			return fmt.Sprintf("entity %q has several model attachments (%s), so the one holding %q has to be named too",
				e.ID, namesOrNone(attachmentNames(e)), bone)
		}
	}
	m := e.Model(model)
	if m == nil {
		return fmt.Sprintf("entity %q has no model attachment called %q; %s",
			e.ID, model, hasAttachments(e))
	}
	if m.Doc == nil {
		return fmt.Sprintf("attachment %q has no model loaded, so its bones are not known yet", m.Name)
	}
	if len(m.Doc.Bones) == 0 {
		return fmt.Sprintf("attachment %q has no armature, so it has no bones to ride", m.Name)
	}
	if m.Doc.BoneIndexByName(bone) < 0 {
		msg := fmt.Sprintf("attachment %q has no bone called %q; it has %s",
			m.Name, bone, namesOrNone(boneNames(m)))
		if near := NearestName(bone, boneNames(m)); near != "" {
			msg += fmt.Sprintf(" — did you mean %q?", near)
		}
		return msg
	}
	return ""
}

// BoneModel resolves which attachment a bone binding names, filling in the
// entity's only attachment when the caller did not say. It returns "" exactly
// when BoneProblem would have something to report, so the two are always asked
// in that order.
func (e *Entity) BoneModel(model string) string {
	if model != "" {
		return model
	}
	if e != nil && len(e.Models) == 1 {
		return e.Models[0].Name
	}
	return ""
}

// boneNames lists an attachment's bones, in the order the model holds them.
func boneNames(m *EntityModel) []string {
	if m == nil || m.Doc == nil {
		return nil
	}
	out := make([]string, 0, len(m.Doc.Bones))
	for i := range m.Doc.Bones {
		out = append(out, m.Doc.Bones[i].Name)
	}
	return out
}
