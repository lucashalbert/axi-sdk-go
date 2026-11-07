package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

// rewriteTransport sends every request, whatever its host, to the test server.
type rewriteTransport struct{ target *url.URL }

func (r rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme, req.URL.Host = r.target.Scheme, r.target.Host
	return http.DefaultTransport.RoundTrip(req)
}

func tarGz(t *testing.T, name string, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	must(t, tw.WriteHeader(&tar.Header{Name: "README.md", Mode: 0o644, Size: 2, Typeflag: tar.TypeReg}))
	_, err := tw.Write([]byte("hi"))
	must(t, err)
	must(t, tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}))
	_, err = tw.Write(body)
	must(t, err)
	must(t, tw.Close())
	must(t, gz.Close())
	return buf.Bytes()
}

func zipOf(t *testing.T, name string, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(name)
	must(t, err)
	_, err = w.Write(body)
	must(t, err)
	must(t, zw.Close())
	return buf.Bytes()
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// fakeGitHub serves a release API plus its assets.
type fakeGitHub struct {
	srv      *httptest.Server
	client   *http.Client
	tag      string
	files    map[string][]byte // asset name -> content
	sums     string            // checksums.txt content; "" means derive from files
	apiCalls atomic.Int32
	apiCode  int // 0 means 200
}

func newFakeGitHub(t *testing.T, tag string, files map[string][]byte) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{tag: tag, files: files}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/tool/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		f.apiCalls.Add(1)
		if f.apiCode != 0 {
			http.Error(w, "nope", f.apiCode)
			return
		}
		assets := ""
		for name := range f.files {
			assets += fmt.Sprintf(`{"name":%q,"browser_download_url":%q},`, name, f.srv.URL+"/dl/"+name)
		}
		assets += fmt.Sprintf(`{"name":"checksums.txt","browser_download_url":%q}`, f.srv.URL+"/dl/checksums.txt")
		_, _ = fmt.Fprintf(w, `{"tag_name":%q,"assets":[%s]}`, f.tag, assets)
	})
	mux.HandleFunc("/dl/", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Path[len("/dl/"):]
		if name == "checksums.txt" {
			if f.sums != "" {
				_, _ = fmt.Fprint(w, f.sums)
				return
			}
			for n, b := range f.files {
				_, _ = fmt.Fprintf(w, "%s  %s\n", sum(b), n)
			}
			return
		}
		b, ok := f.files[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	u, _ := url.Parse(f.srv.URL)
	f.client = &http.Client{Transport: rewriteTransport{u}}
	return f
}

func blockingHandler(block <-chan struct{}) http.Handler {
	return http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-block })
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func jsonResponse(body string) *http.Response {
	return &http.Response{StatusCode: 200, Status: "200 OK", Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
}
