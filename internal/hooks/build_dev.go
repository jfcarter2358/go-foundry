//go:build !release

package hooks

// build_dev.go / build_release.go — which kind of binary this is, as a
// constant the whole engine can read.
//
// internal/app already knows: devAvailable in devtools_dev.go is the same fact,
// and it is what decides whether the editor exists at all. This is that fact
// moved somewhere the layers below app can see it, because one of them — the
// script runtime, through `@level.prop.dev_build` — has to answer it to a
// game's own UI.
//
// A constant and not a variable installed at startup, so that a release build
// folds `if hooks.DevBuild` away entirely and nothing has to be initialised in
// the right order for the answer to be right. Every binary built without the
// `release` tag is a development one, the command-line tools included.
//
// What it is FOR, on the game's side: a setting that would be cheating or
// confusing in a shipped game but is how a level gets tested — switching the
// difficulty mid-mission, say. The game's settings screen offers it while
// `@level.prop.dev_build`, and a release build simply does not show it.
const DevBuild = true
