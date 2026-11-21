package pipeline

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oneo-me/nsgo-media/internal/ffmpeg"
)

// streamServer serves testdata over HTTP, as a CDN serves HLS and DASH.
func streamServer(t *testing.T, extra map[string]http.HandlerFunc) *httptest.Server {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "testdata"))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.Dir(root)))
	for path, h := range extra {
		mux.HandleFunc(path, h)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// frameWidth runs p until a frame shows and gives its width.
func frameWidth(t *testing.T, p *Pipeline, want int) {
	t.Helper()
	run(t, p, 10*time.Second, func(st Status) bool {
		return st.Frame != nil && st.Frame.Image.Rect.Dx() == want
	})
}

func TestMapURL(t *testing.T) {
	for _, u := range []string{
		"https://example.com/live/index.m3u8?token=a%2Fb",
		"http://127.0.0.1:8080/dash/chunk-$RepresentationID$-$Number%05d$.m4s",
	} {
		m := mapURL(u)
		if !strings.HasPrefix(m, mapPrefix) {
			t.Fatalf("mapURL(%q) = %q", u, m)
		}
		if back, ok := unmapURL(m); !ok || back != u {
			t.Errorf("unmapURL(%q) = %q, %v; want %q", m, back, ok, u)
		}
	}
	if _, ok := unmapURL("/etc/passwd"); ok {
		t.Error("a path outside the prefix unmapped")
	}
	if _, ok := unmapURL(mapPrefix + "file/etc/passwd"); ok {
		t.Error("a file URL unmapped")
	}
}

func TestRewriteHLS(t *testing.T) {
	in := "#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=\"../keys/k.bin\",IV=0x01\n#EXTINF:1,\nseg0.ts\n#EXTINF:1,\r\nhttps://cdn.example.com/seg1.ts\r\n#EXTINF:1,\n/root/seg2.ts\n"
	out, live := rewriteHLS([]byte(in), "https://example.com/a/b/index.m3u8")
	if !live {
		t.Error("a playlist with no ENDLIST is not live")
	}
	for _, want := range []string{
		`URI="/nsgo-media/https/example.com/a/keys/k.bin"`,
		"\n/nsgo-media/https/example.com/a/b/seg0.ts\n",
		"\n/nsgo-media/https/cdn.example.com/seg1.ts\r\n",
		"\n/nsgo-media/https/example.com/root/seg2.ts\n",
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("rewritten playlist lacks %q:\n%s", want, out)
		}
	}
	if _, live := rewriteHLS([]byte(in+"#EXT-X-ENDLIST\n"), "https://example.com/i.m3u8"); live {
		t.Error("an ended playlist is live")
	}
}

func TestRewriteDASH(t *testing.T) {
	in := `<?xml version="1.0"?>
<MPD xmlns="urn:mpeg:dash:schema:mpd:2011" type="dynamic">
  <BaseURL>https://cdn.example.com/v/</BaseURL>
  <Period>
    <BaseURL>p1/</BaseURL>
    <AdaptationSet>
      <SegmentTemplate media="chunk-$Number%05d$.m4s?a=1&amp;b=2" initialization="/init/$RepresentationID$.mp4"/>
      <Representation id="1" bandwidth="1000"/>
    </AdaptationSet>
  </Period>
</MPD>`
	out, live, err := rewriteDASH([]byte(in), "https://example.com/m/manifest.mpd")
	if err != nil {
		t.Fatal(err)
	}
	if !live {
		t.Error("a dynamic manifest is not live")
	}
	for _, want := range []string{
		"<BaseURL>/nsgo-media/https/cdn.example.com/v/</BaseURL>",
		"<BaseURL>/nsgo-media/https/cdn.example.com/v/p1/</BaseURL>",
		`media="chunk-$Number%05d$.m4s?a=1&amp;b=2"`,
		`initialization="/nsgo-media/https/cdn.example.com/init/$RepresentationID$.mp4"`,
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("rewritten manifest lacks %q:\n%s", want, out)
		}
	}
}

func TestChooseVariant(t *testing.T) {
	vs := []Track{{ID: 1, Bandwidth: 100_000}, {ID: 2, Bandwidth: 500_000}, {ID: 3, Bandwidth: 2_000_000}}
	for _, c := range []struct {
		bps  float64
		want int
	}{{0, 1}, {300_000, 1}, {700_000, 2}, {10_000_000, 3}} {
		if got := chooseVariant(vs, c.bps).ID; got != c.want {
			t.Errorf("at %.0f bit/s chose %d, want %d", c.bps, got, c.want)
		}
	}
}

func TestDecryptSource(t *testing.T) {
	key, iv := []byte("0123456789abcdef"), []byte("fedcba9876543210")
	plain := bytes.Repeat([]byte("nsgo-media "), 3000) // not whole blocks
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	padded := append(append([]byte(nil), plain...), bytes.Repeat([]byte{byte(pad)}, pad)...)
	block, _ := aes.NewCipher(key)
	enc := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(enc, padded)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(enc) }))
	defer srv.Close()
	seg, err := newSegmentSource(context.Background(), srv.URL+"/s.ts", nil, 0, 0, new(bandwidth))
	if err != nil {
		t.Fatal(err)
	}
	d, err := newDecryptSource(seg, key, iv)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	got, err := io.ReadAll(d)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("decrypted %d bytes, want the %d encrypted", len(got), len(plain))
	}
}

func TestHLSOnDemand(t *testing.T) {
	srv := streamServer(t, nil)
	var ev events
	p := open(t, srv.URL+"/hls/master.m3u8", &ev)
	info := p.info
	if info.Format != "hls" || info.Live || !info.Seekable {
		t.Errorf("format %q, live %v, seekable %v", info.Format, info.Live, info.Seekable)
	}
	if info.Duration < 5900*time.Millisecond || info.Duration > 6100*time.Millisecond {
		t.Errorf("duration %v, want 6 s", info.Duration)
	}
	vs := info.Variants()
	if len(vs) != 2 || vs[0].Width != 160 || vs[1].Width != 320 || vs[0].Bandwidth >= vs[1].Bandwidth {
		t.Fatalf("variants %+v", vs)
	}
	if !p.AutoVariant() {
		t.Error("the variant does not follow the bandwidth")
	}

	p.Seek(4*time.Second, true)
	run(t, p, 10*time.Second, func(Status) bool { return shownPTS(p) >= 0 })
	if d := shownPTS(p) - 4*time.Second; d < -40*time.Millisecond || d > 40*time.Millisecond {
		t.Errorf("an exact seek to 4 s shows %v", shownPTS(p))
	}
	p.SetPaused(false)
	run(t, p, 10*time.Second, func(st Status) bool { return st.Ended })
	if ev.has(EventError) {
		t.Errorf("events %+v", ev.list)
	}
}

func TestHLSSelectVariant(t *testing.T) {
	srv := streamServer(t, nil)
	p := open(t, srv.URL+"/hls/master.m3u8", nil)
	vs := p.info.Variants()
	p.SetPaused(false)
	for _, v := range []Track{vs[0], vs[1], vs[0]} {
		before := p.Position()
		if err := p.SelectVariant(v.ID); err != nil {
			t.Fatal(err)
		}
		if p.AutoVariant() || p.VideoTrack() != v.ID {
			t.Fatalf("auto %v, video %d after choosing %d", p.AutoVariant(), p.VideoTrack(), v.ID)
		}
		frameWidth(t, p, v.Width)
		if pos := p.Position(); pos < before-100*time.Millisecond {
			t.Errorf("changing the variant went back from %v to %v", before, pos)
		}
		if a := p.track(p.AudioTrack()); a.Bandwidth != v.Bandwidth {
			t.Errorf("the audio is of variant %d, the video of %d", a.Bandwidth, v.Bandwidth)
		}
		at := p.Position()
		run(t, p, 5*time.Second, func(st Status) bool { return st.Position > at+300*time.Millisecond || st.Ended })
	}
	if err := p.SelectVariant(-1); err != nil || !p.AutoVariant() {
		t.Errorf("back to auto: %v, auto %v", err, p.AutoVariant())
	}
	if err := p.SelectVariant(p.AudioTrack()); err == nil {
		t.Error("an audio track was taken for a variant")
	}
}

func TestHLSByteRanges(t *testing.T) {
	// fMP4 segments as byte ranges of one file, as Apple's examples are:
	// each range must start where the playlist says.
	srv := streamServer(t, nil)
	var ev events
	p := open(t, srv.URL+"/hls-fmp4/index.m3u8", &ev)
	if d := p.info.Duration; d < 3900*time.Millisecond || d > 4100*time.Millisecond {
		t.Errorf("duration %v, want 4 s", d)
	}
	p.Seek(2500*time.Millisecond, true)
	run(t, p, 10*time.Second, func(Status) bool { return shownPTS(p) >= 0 })
	if d := shownPTS(p) - 2500*time.Millisecond; d < -40*time.Millisecond || d > 40*time.Millisecond {
		t.Errorf("an exact seek to 2.5 s shows %v", shownPTS(p))
	}
	p.SetPaused(false)
	run(t, p, 10*time.Second, func(st Status) bool { return st.Ended })
	if shownPTS(p) < 3800*time.Millisecond || ev.has(EventError) {
		t.Errorf("ended at %v, events %+v", shownPTS(p), ev.list)
	}
}

func TestHLSAES128(t *testing.T) {
	var keys atomic.Int32
	srv := streamServer(t, map[string]http.HandlerFunc{
		"/hls-aes/key.bin": func(w http.ResponseWriter, r *http.Request) {
			keys.Add(1)
			w.Write([]byte("0123456789abcdef"))
		},
	})
	var ev events
	p := open(t, srv.URL+"/hls-aes/index.m3u8", &ev)
	if p.info.Duration < 2900*time.Millisecond {
		t.Errorf("duration %v, want 3 s", p.info.Duration)
	}
	p.SetPaused(false)
	run(t, p, 10*time.Second, func(st Status) bool { return st.Ended })
	if shownPTS(p) < 2800*time.Millisecond {
		t.Errorf("ended at %v: segments did not decrypt", shownPTS(p))
	}
	if keys.Load() == 0 {
		t.Error("the key was not fetched through Go")
	}
	if ev.has(EventError) {
		t.Errorf("events %+v", ev.list)
	}
}

func TestHLSLiveWindow(t *testing.T) {
	// A live playlist: a window of 3 one-second segments of hls/v1 that
	// moves on every second, with absolute URLs.
	var start atomic.Int64
	var reloads atomic.Int32
	var srv *httptest.Server
	srv = streamServer(t, map[string]http.HandlerFunc{
		"/live/index.m3u8": func(w http.ResponseWriter, r *http.Request) {
			reloads.Add(1)
			if start.Load() == 0 {
				start.Store(time.Now().UnixNano())
			}
			seq := min(int(time.Since(time.Unix(0, start.Load()))/time.Second), 3)
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			fmt.Fprintf(w, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:1\n#EXT-X-MEDIA-SEQUENCE:%d\n", seq)
			for i := seq; i < seq+3; i++ {
				fmt.Fprintf(w, "#EXTINF:1.0,\n%s/hls/v1_%d.ts\n", srv.URL, i)
			}
		},
	})
	var ev events
	p := open(t, srv.URL+"/live/index.m3u8", &ev)
	info := p.info
	if !info.Live || info.Seekable || info.Duration != 0 {
		t.Errorf("live %v, seekable %v, duration %v", info.Live, info.Seekable, info.Duration)
	}
	p.SetPaused(false)
	run(t, p, 10*time.Second, func(st Status) bool { return st.Position > 1500*time.Millisecond })
	if reloads.Load() < 2 {
		t.Errorf("the playlist loaded %d times: the window was not followed", reloads.Load())
	}
	if ev.has(EventError) {
		t.Errorf("events %+v", ev.list)
	}
}

func TestDASH(t *testing.T) {
	if !ffmpeg.HasDemuxer("dash") {
		t.Skip("the FFmpeg loaded has no DASH demuxer: set NSGO_MEDIA_FFMPEG to libraries tools/ffmpeg built")
	}
	srv := streamServer(t, nil)
	var ev events
	p := open(t, srv.URL+"/dash/manifest.mpd", &ev)
	info := p.info
	if info.Format != "dash" || info.Live || !info.Seekable {
		t.Errorf("format %q, live %v, seekable %v", info.Format, info.Live, info.Seekable)
	}
	vs := info.Variants()
	if len(vs) != 2 || vs[0].Width != 160 || vs[1].Width != 320 {
		t.Fatalf("variants %+v", vs)
	}
	p.Seek(3*time.Second, true)
	run(t, p, 10*time.Second, func(Status) bool { return shownPTS(p) >= 0 })
	if d := shownPTS(p) - 3*time.Second; d < -40*time.Millisecond || d > 40*time.Millisecond {
		t.Errorf("an exact seek to 3 s shows %v", shownPTS(p))
	}
	p.SetPaused(false)
	for _, v := range []Track{vs[0], vs[1]} {
		if err := p.SelectVariant(v.ID); err != nil {
			t.Fatal(err)
		}
		frameWidth(t, p, v.Width)
	}
	run(t, p, 10*time.Second, func(st Status) bool { return st.Ended })
	if ev.has(EventError) {
		t.Errorf("events %+v", ev.list)
	}
}

func TestStreamCloseCancelsRequests(t *testing.T) {
	// A segment that never comes: Close must cancel its request.
	root, err := filepath.Abs(filepath.Join("..", "..", "testdata"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, manifest string
		stuck          func(path string) bool
	}{
		{"hls", "/stall/index.m3u8", func(p string) bool { return strings.HasSuffix(p, "/stuck.ts") }},
		{"dash", "/dash/manifest.mpd", func(p string) bool { return strings.Contains(p, "-004.m4s") }},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.name == "dash" && !ffmpeg.HasDemuxer("dash") {
				t.Skip("the FFmpeg loaded has no DASH demuxer")
			}
			requested := make(chan struct{}, 8)
			canceled := make(chan struct{}, 8)
			files := http.FileServer(http.Dir(root))
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/stall/index.m3u8":
					w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
					io.WriteString(w, "#EXTM3U\n#EXT-X-TARGETDURATION:1\n#EXT-X-PLAYLIST-TYPE:VOD\n"+
						"#EXTINF:1,\n/hls/v1_0.ts\n#EXTINF:1,\n/hls/v1_1.ts\n#EXTINF:1,\n/hls/v1_2.ts\n#EXTINF:1,\nstuck.ts\n#EXT-X-ENDLIST\n")
				case c.stuck(r.URL.Path):
					requested <- struct{}{}
					<-r.Context().Done()
					canceled <- struct{}{}
				default:
					files.ServeHTTP(w, r)
				}
			}))
			defer srv.Close()
			p := New(Options{OpenOutput: silent})
			if _, err := p.Open(srv.URL + c.manifest); err != nil {
				t.Fatal(err)
			}
			p.SetPaused(false)
			deadline := time.After(10 * time.Second)
			for waiting := true; waiting; {
				select {
				case <-requested:
					waiting = false
				case <-deadline:
					p.Close()
					t.Fatal("the stalled segment was never requested")
				default:
					p.Tick()
					time.Sleep(8 * time.Millisecond)
				}
			}
			closed := make(chan struct{})
			go func() {
				p.Close()
				close(closed)
			}()
			select {
			case <-closed:
			case <-time.After(3 * time.Second):
				t.Fatal("Close did not return")
			}
			select {
			case <-canceled:
			case <-time.After(time.Second):
				t.Fatal("Close left the segment's request running")
			}
		})
	}
}

func TestStreamSendsHeaders(t *testing.T) {
	// Every playlist and segment request carries the source's headers.
	root, err := filepath.Abs(filepath.Join("..", "..", "testdata"))
	if err != nil {
		t.Fatal(err)
	}
	var without atomic.Int32
	files := http.FileServer(http.Dir(root))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Referer") != "https://example.com/" {
			without.Add(1)
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		files.ServeHTTP(w, r)
	}))
	defer srv.Close()
	p := New(Options{OpenOutput: silent})
	defer p.Close()
	if _, err := p.OpenSources(Source{Location: srv.URL + "/hls/master.m3u8", Header: http.Header{"Referer": {"https://example.com/"}}}, nil); err != nil {
		t.Fatal(err)
	}
	p.SetPaused(false)
	run(t, p, 10*time.Second, func(st Status) bool { return st.Position > time.Second })
	if n := without.Load(); n > 0 {
		t.Errorf("%d requests went without the headers", n)
	}
}
