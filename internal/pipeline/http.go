package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// httpSource reads an HTTP(S) resource for FFmpeg, as one ranged GET from
// the position read, and another after a seek moves away from it. Every
// request sends header.
type httpSource struct {
	ctx    context.Context
	client *http.Client
	url    string
	header http.Header

	mu      sync.Mutex
	body    io.ReadCloser
	bodyPos int64 // where body reads from
	pos     int64 // where the next Read reads
	size    int64 // -1 when unknown
	rangeOK bool
	opened  bool
}

func newHTTPSource(ctx context.Context, url string, header http.Header) (*httpSource, error) {
	s := &httpSource{ctx: ctx, client: http.DefaultClient, url: url, header: header.Clone(), size: -1}
	if err := s.open(0); err != nil {
		return nil, err
	}
	return s, nil
}

func isURL(src string) bool {
	l := strings.ToLower(src)
	return strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://")
}

// open starts a GET at off.
func (s *httpSource) open(off int64) error {
	if s.body != nil {
		s.body.Close()
		s.body = nil
	}
	req, err := http.NewRequestWithContext(s.ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return err
	}
	for k, v := range s.header {
		req.Header[k] = v
	}
	if h := s.header.Get("Host"); h != "" {
		req.Host = h
	}
	if off > 0 || !s.opened {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", off))
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	switch resp.StatusCode {
	case http.StatusPartialContent:
		s.rangeOK = true
		if cr := resp.Header.Get("Content-Range"); cr != "" {
			if i := strings.LastIndexByte(cr, '/'); i >= 0 {
				if n, err := strconv.ParseInt(cr[i+1:], 10, 64); err == nil {
					s.size = n
				}
			}
		}
	case http.StatusOK:
		if off > 0 {
			resp.Body.Close()
			return errors.New("http: the server cannot seek")
		}
		s.size = resp.ContentLength
	case http.StatusRequestedRangeNotSatisfiable:
		resp.Body.Close()
		s.body, s.bodyPos = io.NopCloser(strings.NewReader("")), off
		s.opened = true
		return nil
	default:
		resp.Body.Close()
		return fmt.Errorf("http: %s", resp.Status)
	}
	s.opened = true
	s.body, s.bodyPos = resp.Body, off
	return nil
}

func (s *httpSource) Read(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.size >= 0 && s.pos >= s.size {
		return 0, io.EOF
	}
	if s.body == nil || s.bodyPos != s.pos {
		// A short skip forward reads through rather than asking again.
		if s.body != nil && s.pos > s.bodyPos && s.pos-s.bodyPos < 256<<10 {
			n, err := io.CopyN(io.Discard, s.body, s.pos-s.bodyPos)
			s.bodyPos += n
			if err != nil {
				s.body.Close()
				s.body = nil
			}
		}
		if s.body == nil || s.bodyPos != s.pos {
			if err := s.open(s.pos); err != nil {
				return 0, err
			}
		}
	}
	n, err := s.body.Read(p)
	s.pos += int64(n)
	s.bodyPos += int64(n)
	if err == io.EOF && n > 0 {
		err = nil
	}
	return n, err
}

func (s *httpSource) Seek(off int64, whence int) (int64, error) {
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
	if off < 0 {
		return 0, errors.New("http: negative position")
	}
	if off != s.pos && !s.rangeOK {
		return 0, errors.New("http: the server cannot seek")
	}
	s.pos = off
	return off, nil
}

func (s *httpSource) Size() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.size
}

func (s *httpSource) Close() {
	s.mu.Lock()
	if s.body != nil {
		s.body.Close()
		s.body = nil
	}
	s.mu.Unlock()
}
