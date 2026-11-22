# Performance

Measured playback of the pipeline, and where the time goes. Read this before changing the presenter, the frame conversion or anything else on the per-frame path.

## Measuring

`TestMeasurePlayback` plays up to 10 s of a clip as a 60 Hz display shows it and reports frames shown and dropped, the decoder and the process's CPU time. It runs only with `NSGO_MEDIA_MEASURE` set; `NSGO_MEDIA_MEASURE_SIZE=1920x1080` converts to a view's size, `NSGO_MEDIA_MEASURE_SOFTWARE=1` turns hardware decoding off.

```sh
NSGO_MEDIA_MEASURE=$PWD/clip.mp4 go test -run Measure -v ./internal/pipeline
```

The clips were FFmpeg's `testsrc2` at 60 fps with AAC sound, H.264 (x264) and HEVC (x265), converted at the source's size, with the libraries `tools/ffmpeg` built (2026-11-22).

| Machine | Decoder | 1080p60 H.264 | 4K60 H.264 | 4K60 HEVC |
| --- | --- | --- | --- | --- |
| MacBook (Apple silicon), macOS | VideoToolbox | 0 dropped, 10% CPU | 1 dropped, 25% | 0 dropped, 23% |
| Ryzen 9 6900HX, Radeon 680M, Omarchy | VAAPI | 0 dropped, 51% | 0 dropped, 166% | 0 dropped, 182% |
| Windows 11 PC | D3D11VA | 0 dropped, 70% | 0 dropped, 111% | 0 dropped, 112% |

CPU is a percentage of one core. Copying hardware frames to memory and converting them to RGBA take most of it; swscale splits the conversion over up to 8 threads. Each frame queued or showing also keeps its decoded picture for snapshots, about 12 MiB for 8-bit 4K; keeping them costs no CPU, as the copy from a hardware frame is made anyway (a 4K60 H.264 clip on the MacBook used 54–58% before and after).

## The app's share

The pipeline's frames then go through NSGo's pixel buffers: the engine copies each new version and the platform uploads it. In the demo on macOS, a 4K60 HEVC clip in a window about 2500×1400 pixels used about 140% CPU in all, against the pipeline's 25%, as each frame is converted to the view's size and then copied twice more. Zero-copy rendering of hardware frames is a [later goal](https://github.com/oneo-me/nsgo-media/issues/15).

## Presentation

Frames show a little before their time (`lead`, half a frame at most 8 ms). When a 60 Hz display meets 60 fps video in phase, jitter would make two frames due in one display frame and none in the next; each such skip moves the lead by 3 ms around a frame until the phases part. Before this, 1080p60 dropped 4–10% of frames at 10% CPU.
