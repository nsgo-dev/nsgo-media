---
title: 字幕
description: 选择字幕轨道或添加字幕文件，把文本和位图 cue 画在画面之上。
---

内核解码字幕，以 cue 的形式交给应用。应用用自己的样式绘制它们。

## 轨道和文件

媒体源的字幕轨道在 `p.Info().Tracks` 里，`Kind` 为 `player.Subtitle`。`SelectSubtitle(id)` 选择一条，-1 表示不显示。

`AddSubtitles(path)` 把 UTF-8 或 UTF-16 编码的 SRT、WebVTT 或 ASS 文件作为当前媒体源的一条轨道加入，`Info` 会列出它，`SelectSubtitle` 可以选择它。VobSub 的 `.idx`（旁边要有对应的 `.sub`）会按语言各加一条轨道。文件会立即读取。

```go
if err := p.AddSubtitles("movie.srt"); err != nil {
	// 不是内核能读的字幕文件
}
```

媒体源里的位图轨道只在被选中时解码，所以选择它会从当前位置之前的关键帧重新解码，画面会停顿片刻。

## Cue

`p.Cues()` 是所选轨道当前正在显示的 cue。暂停时它们保持不变，位图轨道更新画面时随之改变，什么都不显示时为空。跳转之后不会留下旧位置的 cue。

一个 `player.Cue` 有 `Start` 和 `End`，文本轨道给出 `Text`，PGS、DVD、DVB 或 VobSub 轨道则给出画布 `Canvas` 上的 `Bitmaps`。ASS 只保留文本，不保留样式。

### 文本

```go
core.Text{ContentFunc: func() string {
	var lines []string
	for _, c := range p.Cues() {
		lines = append(lines, c.Text)
	}
	return strings.Join(lines, "\n")
}}
```

把它放在画面之上，作为 `player.View` 的子节点或排在它之后的视图，并设置 `PointerEvents: core.PointerEventsNone`，让点击穿透到你的控件。

### 位图

每个 `Bitmap` 是一张预乘的 `*image.RGBA`，左上角位于 cue 画布上的 `X`、`Y` 处，画布覆盖整个画面。旋转视频的位图和画布会像画面一样转过来。

要画出位图，先求出视图把画面画在哪里，再求出位图落在画面的哪里：

```go
picture := p.PictureBounds(box, fit) // box：视图的尺寸，fit：它的 Fit
r := cue.Place(bitmap, picture)      // 单位与 picture 相同
```

用 `core.Image` 和 `core.ImageSource{Pixels: bitmap.Image}` 把位图拉伸画到 `r`。演示应用里的 `bitmapSubtitles` 就这样画出每张正在显示的位图。

每次跳转，以及每次需要重新解码的向后逐帧，cue 的 `Generation` 都会增加，所以重新读到的同一个 cue 会有新的位图。PGS cue 的 `End` 在读到同一轨道的下一个 cue 之前为 0。
