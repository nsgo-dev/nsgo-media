---
title: Subtitles
description: Choose a subtitle track or add a file, and draw its text and bitmap cues over the picture.
---

The core decodes subtitles and hands them to the app as cues. The app draws them, in its own style.

## Tracks and files

The source's subtitle tracks are in `p.Info().Tracks`, with `Kind` set to `player.Subtitle`. `SelectSubtitle(id)` chooses one, or none for -1.

`AddSubtitles(path)` adds an SRT, WebVTT or ASS file, in UTF-8 or UTF-16, as a track of the open source, which `Info` lists and `SelectSubtitle` selects. A VobSub `.idx`, with its `.sub` beside it, adds a track per language. It reads the file at once.

```go
if err := p.AddSubtitles("movie.srt"); err != nil {
	// not a subtitle file the core reads
}
```

A bitmap track of the source decodes only while it is chosen, so choosing one decodes again from the keyframe before the position and the picture holds for a moment.

## Cues

`p.Cues()` is the chosen track's cues showing now. They hold while paused, change as a bitmap track updates its picture, and are empty when nothing shows. After a seek no cue of the old position stays.

A `player.Cue` has its `Start` and `End`, and either `Text`, for a text track, or `Bitmaps` on a `Canvas`, for a PGS, DVD, DVB or VobSub track. ASS styling beyond the text is not kept.

### Text

```go
core.Text{ContentFunc: func() string {
	var lines []string
	for _, c := range p.Cues() {
		lines = append(lines, c.Text)
	}
	return strings.Join(lines, "\n")
}}
```

Place it above the picture, as a child of the `player.View` or a view after it, with `PointerEvents: core.PointerEventsNone` so it lets taps through to your controls.

### Bitmaps

Each `Bitmap` is a premultiplied `*image.RGBA` with its top left corner at `X`, `Y` on the cue's canvas, which covers the picture. A rotated video's bitmaps and canvas are turned as its picture is.

To draw one, find where the view draws the picture, then where the bitmap falls on it:

```go
picture := p.PictureBounds(box, fit) // box: the view's size, fit: its Fit
r := cue.Place(bitmap, picture)      // in the same units as picture
```

Draw the bitmap as a `core.Image` with `core.ImageSource{Pixels: bitmap.Image}`, stretched to `r`. The demo app's `bitmapSubtitles` does this for every bitmap showing.

A cue's `Generation` rises with each seek and each step backward that decodes again, so the same cue read again has new bitmaps. A PGS cue's `End` is 0 until the next cue of its track is read.
