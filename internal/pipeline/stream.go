package pipeline

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nsgo-dev/nsgo-media/internal/ffmpeg"
)

// FFmpeg is built without network protocols, and its HLS and DASH
// demuxers open only URLs of a protocol it has. So an adaptive stream's
// URLs reach FFmpeg as paths under mapPrefix, which it takes for files and
// resolves relative references against like URLs, and its io_open hands
// every one back to Go: /nsgo-media/https/example.com/live/index.m3u8 is
// https://example.com/live/index.m3u8. Manifests are rewritten so that
// every absolute or root-relative reference in them is such a path.
const mapPrefix = "/nsgo-media/"

// Limits of what an adaptive stream reads into memory.
const (
	maxManifestSize  = 16 << 20
	maxSegmentBuffer = 2 << 20 // a segment read ahead of the demuxer
	sampleBytes      = 1 << 20 // the most a bandwidth sample spans
)

// mapURL is the path FFmpeg sees for an absolute HTTP(S) URL. It works on
// the string, so DASH templates such as $Number%05d$ pass unchanged.
func mapURL(u string) string {
	scheme, rest, ok := strings.Cut(u, "://")
	if !ok {
		return u
	}
	return mapPrefix + strings.ToLower(scheme) + "/" + rest
}

// unmapURL is the URL behind a path mapURL made.
func unmapURL(p string) (string, bool) {
	rest, ok := strings.CutPrefix(p, mapPrefix)
	if !ok {
		return "", false
	}
	scheme, rest, ok := strings.Cut(rest, "/")
	if !ok || scheme != "http" && scheme != "https" || rest == "" {
		return "", false
	}
	return scheme + "://" + rest, true
}

// manifestKind tells what a response holds from its URL and media type:
// "hls", "dash", or empty for media.
func manifestKind(url, contentType string) string {
	ct := strings.ToLower(contentType)
	switch {
	case strings.Contains(ct, "mpegurl"):
		return "hls"
	case strings.Contains(ct, "dash+xml"):
		return "dash"
	}
	u, _, _ := strings.Cut(url, "?")
	switch strings.ToLower(path.Ext(u)) {
	case ".m3u8", ".m3u":
		return "hls"
	case ".mpd":
		return "dash"
	}
	return ""
}

// demuxerOptions are the HLS and DASH demuxers' options for mapped URLs:
// the paths' extensions are the URLs', which need not be media's.
var demuxerOptions = map[string]map[string]string{
	"hls":  {"allowed_extensions": "ALL", "allowed_segment_extensions": "ALL", "extension_picky": "0"},
	"dash": {"allowed_extensions": "ALL"},
}

// adaptiveStream opens an HLS or DASH stream's resources for FFmpeg and
// measures how fast its segments arrive.
type adaptiveStream struct {
	ctx    context.Context
	kind   string      // "hls" or "dash"
	header http.Header // what every request sends
	// live is set once a manifest shows a stream without an end.
	live atomic.Bool
	bw   bandwidth
}

func newAdaptiveStream(ctx context.Context, kind string, header http.Header) *adaptiveStream {
	return &adaptiveStream{ctx: ctx, kind: kind, header: header.Clone()}
}

// manifest reads a manifest's body and rewrites its references against
// the URL it came from.
func (a *adaptiveStream) manifest(body io.Reader, from string) (*memSource, error) {
	data, err := io.ReadAll(io.LimitReader(body, maxManifestSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxManifestSize {
		return nil, errors.New("http: manifest too large")
	}
	var live bool
	if a.kind == "hls" {
		data, live = rewriteHLS(data, from)
	} else {
		data, live, err = rewriteDASH(data, from)
		if err != nil {
			return nil, err
		}
	}
	if live {
		a.live.Store(true)
	}
	return &memSource{Reader: bytes.NewReader(data)}, nil
}

// open is the demuxer's io_open: a manifest it reloads, a segment, an
// initialization section or a key.
func (a *adaptiveStream) open(name string, opts map[string]string) (ffmpeg.Source, error) {
	var key, iv []byte
	if rest, ok := strings.CutPrefix(name, "crypto"); ok && len(rest) > 0 && (rest[0] == ':' || rest[0] == '+') {
		name = rest[1:]
		var err error
		if key, err = hex.DecodeString(opts["key"]); err != nil || len(key) != 16 {
			return nil, errors.New("hls: bad AES-128 key")
		}
		if iv, err = hex.DecodeString(opts["iv"]); err != nil || len(iv) != 16 {
			return nil, errors.New("hls: bad AES-128 IV")
		}
	}
	u, ok := unmapURL(name)
	if !ok {
		return nil, fmt.Errorf("%s: cannot open %q", a.kind, name)
	}
	off, _ := strconv.ParseInt(opts["offset"], 10, 64)
	end, _ := strconv.ParseInt(opts["end_offset"], 10, 64)
	seg, err := newSegmentSource(a.ctx, u, a.header, max(off, 0), end, &a.bw)
	if err != nil {
		return nil, err
	}
	if manifestKind(seg.finalURL, seg.contentType) != "" {
		defer seg.Close()
		return a.manifest(seg, seg.finalURL)
	}
	if key != nil {
		return newDecryptSource(seg, key, iv)
	}
	return seg, nil
}

// memSource is a manifest, held in memory.
type memSource struct{ *bytes.Reader }

func (m *memSource) Size() int64 { return m.Reader.Size() }

// resolve is ref against base, as a mapped path when it is HTTP(S).
func resolve(base, ref string) string {
	if strings.HasPrefix(ref, "data:") || strings.HasPrefix(ref, mapPrefix) {
		return ref
	}
	if hasScheme(ref) {
		return mapURL(ref)
	}
	return mapURL(resolveURL(base, ref))
}

func hasScheme(ref string) bool {
	l := strings.ToLower(ref)
	return strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://")
}

// rewriteHLS maps every URI of an HLS playlist, its lines and its tags'
// URI attributes, resolved against from. live reports a media playlist
// with no end.
func rewriteHLS(data []byte, from string) (out []byte, live bool) {
	var b bytes.Buffer
	media, ended := false, false
	for _, line := range strings.SplitAfter(string(data), "\n") {
		body := strings.TrimRight(line, "\r\n")
		eol := line[len(body):]
		t := strings.TrimSpace(body)
		switch {
		case t == "":
		case strings.HasPrefix(t, "#"):
			switch {
			case strings.HasPrefix(t, "#EXTINF"):
				media = true
			case strings.HasPrefix(t, "#EXT-X-ENDLIST"):
				ended = true
			}
			body = rewriteURIAttr(body, from)
		default:
			body = resolve(from, t)
		}
		b.WriteString(body)
		b.WriteString(eol)
	}
	return b.Bytes(), media && !ended
}

// rewriteURIAttr maps a tag's URI="..." attribute.
func rewriteURIAttr(tag, from string) string {
	i := strings.Index(tag, `URI="`)
	if i < 0 {
		return tag
	}
	start := i + len(`URI="`)
	n := strings.IndexByte(tag[start:], '"')
	if n < 0 {
		return tag
	}
	return tag[:start] + resolve(from, tag[start:start+n]) + tag[start+n:]
}

// bandwidth estimates the network's rate from the segments downloaded.
type bandwidth struct {
	mu      sync.Mutex
	bps     float64
	samples int
}

// add takes a segment of n bytes that took d to arrive.
func (b *bandwidth) add(n int64, d time.Duration) {
	if n < 16<<10 || d <= 0 {
		return // too small to tell the rate from the latency
	}
	rate := float64(n*8) / d.Seconds()
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.samples == 0 {
		b.bps = rate
	} else {
		b.bps = 0.7*b.bps + 0.3*rate
	}
	b.samples++
}

// estimate is the rate in bits per second, 0 before a sample.
func (b *bandwidth) estimate() float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.bps
}

// segmentSource downloads one resource of an adaptive stream ahead of the
// demuxer, into at most maxSegmentBuffer bytes, timing the download while
// it is not waiting for room.
type segmentSource struct {
	url         string
	finalURL    string
	contentType string
	size        int64 // the whole resource's, -1 when unknown
	bw          *bandwidth
	cancel      context.CancelFunc
	done        chan struct{}

	mu   sync.Mutex
	cond *sync.Cond
	buf  []byte // downloaded and not yet read
	pos  int64  // where the next Read reads
	off  int64  // where the download started
	err  error  // io.EOF once the download ended
	shut bool
}

// newSegmentSource starts a GET of url from off, up to end when above 0,
// and returns once the response's headers arrived.
func newSegmentSource(ctx context.Context, url string, header http.Header, off, end int64, bw *bandwidth) (*segmentSource, error) {
	ctx, cancel := context.WithCancel(ctx)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		cancel()
		return nil, err
	}
	for k, v := range header {
		req.Header[k] = v
	}
	if h := header.Get("Host"); h != "" {
		req.Host = h
	}
	if end > off {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", off, end-1))
	} else if off > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", off))
	}
	started := time.Now()
	resp, err := httpClient.Do(req)
	if err != nil {
		cancel()
		return nil, err
	}
	s := &segmentSource{url: url, finalURL: resp.Request.URL.String(), contentType: resp.Header.Get("Content-Type"),
		size: -1, bw: bw, cancel: cancel, done: make(chan struct{}), pos: off, off: off}
	s.cond = sync.NewCond(&s.mu)
	switch resp.StatusCode {
	case http.StatusPartialContent:
		if cr := resp.Header.Get("Content-Range"); cr != "" {
			if i := strings.LastIndexByte(cr, '/'); i >= 0 {
				s.size, _ = strconv.ParseInt(cr[i+1:], 10, 64)
			}
		}
	case http.StatusOK:
		s.size = resp.ContentLength
		if off > 0 {
			// The server sent the whole resource: skip to the range.
			if _, err := io.CopyN(io.Discard, resp.Body, off); err != nil {
				resp.Body.Close()
				cancel()
				return nil, err
			}
		}
	default:
		resp.Body.Close()
		cancel()
		return nil, fmt.Errorf("http: %s: %s", url, resp.Status)
	}
	if s.size <= 0 {
		s.size = -1
	}
	limit := int64(-1)
	if end > off {
		limit = end - off
	}
	go s.download(resp.Body, limit, started)
	return s, nil
}

// download reads the body into the buffer. The bandwidth is sampled from
// the request on, every sampleBytes and at the end, so a long byte range
// counts as it arrives, until the download first waits for room: what
// arrives after that the system had buffered meanwhile.
func (s *segmentSource) download(body io.ReadCloser, limit int64, started time.Time) {
	defer close(s.done)
	defer body.Close()
	var r io.Reader = body
	if limit >= 0 {
		r = io.LimitReader(body, limit)
	}
	var n int64 // bytes since the last sample
	waited := false
	sample := func(now time.Time) {
		if !waited {
			s.bw.add(n, now.Sub(started))
		}
		n, started = 0, now
	}
	chunk := make([]byte, 64<<10)
	for {
		k, err := r.Read(chunk)
		s.mu.Lock()
		if k > 0 {
			s.buf = append(s.buf, chunk[:k]...)
			n += int64(k)
			s.cond.Broadcast()
		}
		if err == nil && (n >= sampleBytes || len(s.buf) >= maxSegmentBuffer) {
			sample(time.Now())
		}
		if err == nil && len(s.buf) >= maxSegmentBuffer {
			for len(s.buf) >= maxSegmentBuffer && !s.shut {
				s.cond.Wait()
			}
			waited = true
		}
		if err == nil && s.shut {
			err = context.Canceled
		}
		if err != nil {
			s.err = err
			s.cond.Broadcast()
			s.mu.Unlock()
			if errors.Is(err, io.EOF) {
				sample(time.Now())
			}
			return
		}
		s.mu.Unlock()
	}
}

func (s *segmentSource) Read(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.buf) == 0 && s.err == nil && !s.shut {
		s.cond.Wait()
	}
	if len(s.buf) == 0 {
		if s.shut {
			return 0, context.Canceled
		}
		return 0, s.err
	}
	n := copy(p, s.buf)
	s.buf = s.buf[n:]
	if len(s.buf) == 0 {
		s.buf = nil
	}
	s.pos += int64(n)
	s.cond.Broadcast()
	return n, nil
}

// Seek only tells the position or skips forward over what downloaded:
// demuxers read segments through.
func (s *segmentSource) Seek(off int64, whence int) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch whence {
	case io.SeekCurrent:
		off += s.pos
	case io.SeekEnd:
		if s.size < 0 {
			return 0, errors.New("http: unknown size")
		}
		off += s.size
	}
	if off == s.pos {
		return off, nil
	}
	if d := off - s.pos; d > 0 && d <= int64(len(s.buf)) {
		s.buf = s.buf[d:]
		s.pos = off
		s.cond.Broadcast()
		return off, nil
	}
	return 0, errors.New("http: a segment seeks only forward")
}

func (s *segmentSource) Size() int64 { return s.size }

// Start is where the byte range the source reads starts.
func (s *segmentSource) Start() int64 { return s.off }

// Close stops the download and waits for it.
func (s *segmentSource) Close() error {
	s.mu.Lock()
	s.shut = true
	s.buf = nil
	s.cond.Broadcast()
	s.mu.Unlock()
	s.cancel()
	<-s.done
	return nil
}

// decryptSource decrypts an AES-128 segment: CBC over the whole resource
// with PKCS#7 padding, as HLS encrypts it.
type decryptSource struct {
	src    *segmentSource
	mode   cipher.BlockMode
	in     []byte // ciphertext not yet decrypted, less than a block
	out    []byte // plaintext not yet read
	held   []byte // the last block decrypted, kept until the padding shows
	pos    int64
	srcEOF bool
}

func newDecryptSource(src *segmentSource, key, iv []byte) (*decryptSource, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		src.Close()
		return nil, err
	}
	return &decryptSource{src: src, mode: cipher.NewCBCDecrypter(block, iv)}, nil
}

func (d *decryptSource) Read(p []byte) (int, error) {
	for len(d.out) == 0 {
		if d.srcEOF {
			return 0, io.EOF
		}
		buf := make([]byte, 64<<10)
		n, err := d.src.Read(buf)
		d.in = append(d.in, buf[:n]...)
		if err != nil && !errors.Is(err, io.EOF) {
			return 0, err
		}
		whole := len(d.in) / aes.BlockSize * aes.BlockSize
		if whole > 0 {
			plain := make([]byte, whole)
			d.mode.CryptBlocks(plain, d.in[:whole])
			d.in = d.in[whole:]
			d.out = append(d.out, d.held...)
			d.out = append(d.out, plain[:whole-aes.BlockSize]...)
			d.held = plain[whole-aes.BlockSize:]
		}
		if errors.Is(err, io.EOF) {
			d.srcEOF = true
			if len(d.in) != 0 {
				return 0, errors.New("hls: an AES-128 segment is not whole blocks")
			}
			if len(d.held) > 0 {
				pad := int(d.held[len(d.held)-1])
				if pad == 0 || pad > aes.BlockSize {
					return 0, errors.New("hls: bad AES-128 padding")
				}
				d.out = append(d.out, d.held[:aes.BlockSize-pad]...)
				d.held = nil
			}
		}
	}
	n := copy(p, d.out)
	d.out = d.out[n:]
	d.pos += int64(n)
	return n, nil
}

// Seek only tells the position: decryption runs from the start.
func (d *decryptSource) Seek(off int64, whence int) (int64, error) {
	if whence == io.SeekCurrent {
		off += d.pos
	}
	if whence != io.SeekEnd && off == d.pos {
		return off, nil
	}
	return 0, errors.New("hls: an encrypted segment does not seek")
}

func (d *decryptSource) Size() int64 { return -1 }

func (d *decryptSource) Close() error { return d.src.Close() }

// dashURLAttrs are the attributes of a DASH manifest that hold URLs or
// URL templates.
var dashURLAttrs = regexp.MustCompile(`(\s(?:media|initialization|index|sourceURL)\s*=\s*)("[^"]*"|'[^']*')`)

// rewriteDASH maps a DASH manifest's references: each BaseURL resolved
// against the ones above it and from becomes a mapped absolute path, and
// absolute or root-relative templates and Locations become mapped too.
// Relative templates are left for the demuxer to resolve against the
// mapped BaseURLs. live reports a dynamic manifest.
func rewriteDASH(data []byte, from string) (out []byte, live bool, err error) {
	type edit struct {
		start, end int64
		text       string
	}
	var edits []edit
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	// bases[i] is the base URL of the i-th open element's children.
	bases := []string{from}
	text := "" // the element whose text is being read: BaseURL or Location
	var textStart int64 = -1
	for {
		start := dec.InputOffset()
		tok, terr := dec.Token()
		if terr == io.EOF {
			break
		}
		if terr != nil {
			return nil, false, fmt.Errorf("dash: %w", terr)
		}
		end := dec.InputOffset()
		base := bases[len(bases)-1]
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "MPD" {
				for _, a := range t.Attr {
					live = live || a.Name.Local == "type" && a.Value == "dynamic"
				}
			}
			if t.Name.Local == "BaseURL" || t.Name.Local == "Location" {
				text, textStart = t.Name.Local, end
			}
			raw := string(data[start:end])
			if dashURLAttrs.MatchString(raw) {
				edits = append(edits, edit{start, end, dashURLAttrs.ReplaceAllStringFunc(raw, func(m string) string {
					sub := dashURLAttrs.FindStringSubmatch(m)
					q := sub[2][:1]
					return sub[1] + q + mapRawRef(base, sub[2][1:len(sub[2])-1]) + q
				})})
			}
			bases = append(bases, base)
		case xml.EndElement:
			if t.Name.Local == text && textStart >= 0 {
				raw := strings.TrimSpace(string(data[textStart:start]))
				ref := html.UnescapeString(raw)
				var abs string
				if text == "BaseURL" {
					// A BaseURL sets the base of its parent's other children.
					abs = resolveURL(bases[len(bases)-2], ref)
					bases[len(bases)-2] = abs
				} else {
					abs = resolveURL(from, ref)
				}
				var esc strings.Builder
				xml.EscapeText(&esc, []byte(mapURL(abs)))
				edits = append(edits, edit{textStart, start, esc.String()})
				text, textStart = "", -1
			}
			bases = bases[:len(bases)-1]
		}
	}
	var b bytes.Buffer
	var at int64
	for _, e := range edits {
		b.Write(data[at:e.start])
		b.WriteString(e.text)
		at = e.end
	}
	b.Write(data[at:])
	return b.Bytes(), live, nil
}

// mapRawRef maps an attribute's raw value when it is absolute or
// root-relative, keeping templates and entities as they are.
func mapRawRef(base, raw string) string {
	switch {
	case hasScheme(raw):
		return mapURL(raw)
	case strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "//"):
		if b, err := parseURL(base); err == nil {
			return mapURL(b.Scheme + "://" + b.Host + raw)
		}
	}
	return raw
}

// resolveURL is ref against base, as a URL.
func resolveURL(base, ref string) string {
	b, err := parseURL(base)
	if err != nil {
		return ref
	}
	r, err := parseURL(ref)
	if err != nil {
		return ref
	}
	return b.ResolveReference(r).String()
}

func parseURL(s string) (*url.URL, error) { return url.Parse(s) }
