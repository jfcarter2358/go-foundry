# video/

Clips a `video` widget can play, named from a `.fui` without the extension —
`video: intro` finds `intro.avi` or `intro.gif` here.

Two formats, both decoded by the Go standard library so the engine needs no
native video dependency:

**`.avi`** — motion-JPEG frames, with an optional PCM soundtrack that plays on
the music bus. This is the one to use for anything with sound or more than a few
seconds long. From any source file:

```
ffmpeg -i in.mp4 -c:v mjpeg -q:v 3 -c:a pcm_s16le out.avi
```

`-q:v` is quality, 2 (best) to 31. Every frame is a full JPEG, so the files are
large and seeking is instant; scale the picture down to the size it will
actually be drawn at rather than shipping 4K for a 640-pixel widget:

```
ffmpeg -i in.mp4 -vf scale=1280:-2 -r 24 -c:v mjpeg -q:v 4 -c:a pcm_s16le out.avi
```

Anything that is not motion-JPEG is refused at load with the line above in the
message. A soundtrack longer than three minutes is truncated and the clip plays
on — split a longer film into two files.

**`.gif`** — an animated GIF, for a short silent loop: a logo sting, a spinner,
an animated background. Per-frame delays are honoured, and frames are composed
against their disposal methods rather than drawn as raw patches.
