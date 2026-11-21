# Test fixtures

Small clips the pipeline's tests decode, made with FFmpeg's test sources:

```sh
# av.mkv: 2 s of 160×90 H.264 at 25 fps, AAC in English (48 kHz) and Japanese (44.1 kHz), sub.srt as a subtitle track, two chapters
# rotated.mp4: 1 s of 96×64 H.264 at 30 fps with a display matrix turning it 90° clockwise
ffmpeg -f lavfi -i testsrc2=size=96x64:rate=30:duration=1 -c:v libx264 -pix_fmt yuv420p r.mp4
ffmpeg -display_rotation:v:0 -90 -i r.mp4 -c copy rotated.mp4
```
