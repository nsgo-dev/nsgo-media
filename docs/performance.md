# Performance

Measured playback of the pipeline, and where the time goes. Read this before changing the presenter, the frame conversion, the views' drawing or anything else on the per-frame path.

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

The pipeline's frames in memory then go through NSGo's pixel buffers: the engine copies each new version and the platform uploads it. In the demo on macOS, a 4K60 HEVC clip in a window about 2500×1400 pixels used about 140% CPU in all, against the pipeline's 25%, as each frame is converted to the view's size and then copied twice more. Zero-copy rendering on macOS and iOS is a [later goal](https://github.com/oneo-me/nsgo-media/issues/40).

Until then, macOS uploads HDR pictures to a `gpu` view instead of converting them to RGBA. Writing a 4K frame's P010 planes (24 MiB) takes about 1.2 ms of the engine's goroutine, as NSGo copies them for its backend, which copies them again into a staging buffer the GPU copies from. In the demo, 1024×672 points on a MacBook Pro (M1 Max), a 4K60 HEVC 10-bit HDR10 clip with VideoToolbox used 200–203% CPU over 20 s after 6 s of playing before, and 64–72% after, about 1.1–1.2 GiB resident in both (2026-11-25).

On Windows and Linux, hardware pictures stay on the GPU and a `gpu` view draws them, with no conversion or copy through memory. A player filling a 1920×1080-point window played the clips above, looped, in a process of its own; the CPU is over 20 s after 6 s of playing, the memory the most private bytes (Windows) or resident set (Linux) in that time, before and after (2026-11-25):

| Machine | Clip | Before | After |
| --- | --- | --- | --- |
| Windows 11 PC (Core Ultra 9 285K, RTX 5080), D3D11VA | 1080p60 H.264 | 59% CPU, 533 MiB | 9%, 402 MiB |
| | 4K60 H.264 | 104%, 1187 MiB | 16%, 1037 MiB |
| | 4K60 HEVC | 97%, 1193 MiB | 9%, 1054 MiB |
| | 4K60 HEVC 10-bit | 115%, 1783 MiB | 9%, 1628 MiB |
| Ryzen 9 6900HX, Radeon 680M, Omarchy, VAAPI | 1080p60 H.264 | 55%, 495 MiB | 5%, 251 MiB |
| | 4K60 H.264 | 87%, 1405 MiB | 5%, 486 MiB |
| | 4K60 HEVC | 104%, 1537 MiB | 6%, 407 MiB |
| | 4K60 HEVC 10-bit | 134%, 2757 MiB | 6%, 401 MiB |

Most of the Windows process's private bytes are the graphics driver's in both. Each frame on the GPU holds its decoder surface, so a hardware decoder's pool keeps 7 more; on Windows each frame also has a texture of its own, which D3D11VA's slice is copied into on the GPU, as the views cannot import one slice of its texture array.

## Presentation

Frames show a little before their time (`lead`, half a frame at most 8 ms). When a 60 Hz display meets 60 fps video in phase, jitter would make two frames due in one display frame and none in the next; each such skip moves the lead by 3 ms around a frame until the phases part. Before this, 1080p60 dropped 4–10% of frames at 10% CPU.

## Low-latency encoding (`codec`)

`TestMeasureEncodeLatency` in `codec` encodes 60 pre-generated 1920×1080 NV12 frames (after 10 warm-up frames) at 60 fps and 12 Mbps with no B-frames and low-delay settings (2026-11-25):

| Machine | Encoder | Mean | p50 | p95 | Min – Max |
| --- | --- | --- | --- | --- | --- |
| MacBook (Apple M1 Max), macOS | `h264_videotoolbox` (hw) | 6.29 ms | 6.28 ms | 6.43 ms | 6.02 – 6.44 ms |
| Windows 11 PC (Core Ultra 9 285K, RTX 5080) | `h264_nvenc` (hw) | 1.82 ms | 1.73 ms | 2.34 ms | 0.52 – 3.82 ms |
| Ryzen 9 6900HX, Radeon 680M, Omarchy | `h264_vaapi` (hw) | 6.52 ms | 6.36 ms | 7.84 ms | 4.52 – 9.80 ms |
