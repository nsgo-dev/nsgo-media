# Test fixtures

Small clips the pipeline's tests decode, made with FFmpeg's test sources:

```sh
# av.mkv: 2 s of 160×90 H.264 at 25 fps, AAC in English (48 kHz) and Japanese (44.1 kHz), sub.srt as a subtitle track, two chapters
# rotated.mp4: 1 s of 96×64 H.264 at 30 fps with a display matrix turning it 90° clockwise
# dash-video.mp4, dash-audio.m4a: a fragmented MP4 pair as DASH serves them, 2 s of 160×90 H.264 at 25 fps without B-frames, and av.mkv's English audio
# vfr.mkv: 2 s of 160×90 H.264 at a variable rate, frames 20 to 60 ms apart, B-frames, a keyframe every 12 frames
# hls/: 6 s in two variants, 320×180 and 160×90 H.264 with their own AAC, in 1 s TS segments
# hls-aes/: 3 s of 160×90 in 1 s TS segments encrypted with AES-128 (key.bin)
# hls-fmp4/: 4 s of 160×90 in 1 s fMP4 segments, byte ranges of one file
# dash/: 6 s in two video representations, 320×180 and 160×90, and one audio, in 1 s segments
ffmpeg -f lavfi -i testsrc2=size=320x180:rate=25:duration=6 -f lavfi -i sine=frequency=440:sample_rate=48000:duration=6 -filter_complex "[0:v]split=2[a][b];[b]scale=160:90[s]" -map "[a]" -map "[s]" -map 1:a -map 1:a -c:v libx264 -pix_fmt yuv420p -g 25 -keyint_min 25 -sc_threshold 0 -b:v:0 120k -b:v:1 40k -c:a aac -b:a 32k -ac 1 -f hls -hls_time 1 -hls_playlist_type vod -hls_segment_filename 'hls/v%v_%d.ts' -master_pl_name master.m3u8 -var_stream_map "v:0,a:0 v:1,a:1" 'hls/v%v.m3u8'
printf 'key.bin\nhls-aes/key.bin\n00112233445566778899aabbccddeeff\n' > keyinfo  # key.bin holds 0123456789abcdef
ffmpeg -f lavfi -i testsrc2=size=160x90:rate=25:duration=3 -f lavfi -i sine=frequency=440:sample_rate=48000:duration=3 -c:v libx264 -pix_fmt yuv420p -g 25 -sc_threshold 0 -b:v 40k -c:a aac -b:a 32k -ac 1 -f hls -hls_time 1 -hls_playlist_type vod -hls_key_info_file keyinfo -hls_segment_filename 'hls-aes/s%d.ts' hls-aes/index.m3u8
ffmpeg -f lavfi -i testsrc2=size=160x90:rate=25:duration=4 -f lavfi -i sine=frequency=440:sample_rate=48000:duration=4 -c:v libx264 -pix_fmt yuv420p -g 25 -sc_threshold 0 -b:v 40k -c:a aac -b:a 32k -ac 1 -f hls -hls_time 1 -hls_playlist_type vod -hls_segment_type fmp4 -hls_flags single_file -hls_fmp4_init_filename init.mp4 hls-fmp4/index.m3u8
ffmpeg -f lavfi -i testsrc2=size=320x180:rate=25:duration=6 -f lavfi -i sine=frequency=440:sample_rate=48000:duration=6 -filter_complex "[0:v]split=2[a][b];[b]scale=160:90[s]" -map "[a]" -map "[s]" -map 1:a -c:v libx264 -pix_fmt yuv420p -g 25 -keyint_min 25 -sc_threshold 0 -b:v:0 120k -b:v:1 40k -c:a aac -b:a 32k -ac 1 -f dash -seg_duration 1 -use_template 1 -use_timeline 1 -adaptation_sets "id=0,streams=v id=1,streams=a" -init_seg_name 'init-$RepresentationID$.m4s' -media_seg_name 'chunk-$RepresentationID$-$Number%03d$.m4s' dash/manifest.mpd
ffmpeg -f lavfi -i testsrc2=size=96x64:rate=30:duration=1 -c:v libx264 -pix_fmt yuv420p r.mp4
ffmpeg -display_rotation:v:0 -90 -i r.mp4 -c copy rotated.mp4
ffmpeg -f lavfi -i testsrc2=size=160x90:rate=50:duration=2 -vf "select='not(mod(n\,3))+eq(mod(n\,7)\,1)'" -fps_mode vfr -c:v libx264 -pix_fmt yuv420p -g 12 -bf 2 vfr.mkv
ffmpeg -f lavfi -i testsrc2=size=160x90:rate=25:duration=2 -c:v libx264 -pix_fmt yuv420p -bf 0 -g 25 -movflags empty_moov+default_base_moof+frag_keyframe dash-video.mp4
ffmpeg -i av.mkv -map 0:a:0 -map_chapters -1 -c copy -frag_duration 1000000 -movflags empty_moov+default_base_moof dash-audio.m4a
```
