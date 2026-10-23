package admin

// [bootimus-fork L1] fork-patch: download-resume 的回归测试。
// 与实现同生共死：上游若自行实现断点续传 / 残留清理，本文件一并删除。

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestParseContentRange(t *testing.T) {
	cases := []struct {
		value             string
		start, end, total int64
		ok                bool
	}{
		{value: "bytes 100-199/1000", start: 100, end: 199, total: 1000, ok: true},
		{value: "bytes 0-0/1", start: 0, end: 0, total: 1, ok: true},
		{value: "bytes */1000", total: 1000, ok: true},
		{value: "bytes 100-199/*", start: 100, end: 199, ok: true},
		{value: "", ok: false},
		{value: "items 1-2/3", ok: false},
		{value: "bytes 100/1000", ok: false},
		{value: "bytes 1-2/abc", ok: false},
	}
	for _, c := range cases {
		start, end, total, ok := parseContentRange(c.value)
		if ok != c.ok {
			t.Errorf("parseContentRange(%q) ok = %v, want %v", c.value, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if start != c.start || end != c.end || total != c.total {
			t.Errorf("parseContentRange(%q) = %d,%d,%d; want %d,%d,%d",
				c.value, start, end, total, c.start, c.end, c.total)
		}
	}
}

// rangeServer serves payload and honours Range requests.
func rangeServer(t *testing.T, payload []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("ETag", `"v1"`)
		http.ServeContent(w, r, "test.iso", time.Unix(1700000000, 0), bytes.NewReader(payload))
	}))
}

func newTestHandler(t *testing.T) (*Handler, string) {
	t.Helper()
	dir := t.TempDir()
	return &Handler{isoDir: dir}, dir
}

func writePartial(t *testing.T, dir, name string, data []byte, url string) {
	t.Helper()
	partPath := filepath.Join(dir, name+".part")
	if err := os.WriteFile(partPath, data, 0o644); err != nil {
		t.Fatalf("write part: %v", err)
	}
	writeDownloadMeta(filepath.Join(dir, name), &downloadPartMeta{
		URL:     url,
		Total:   int64(len(data)),
		Updated: time.Now(),
	})
}

func readISO(t *testing.T, dir, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return data
}

func assertNoTemp(t *testing.T, dir, name string) {
	t.Helper()
	for _, p := range []string{name + ".part", name + ".part.json"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err == nil {
			t.Errorf("%s left behind", p)
		}
	}
}

func TestDownloadISOPlain(t *testing.T) {
	payload := bytes.Repeat([]byte("a"), 200*1024)
	srv := rangeServer(t, payload)
	defer srv.Close()

	h, dir := newTestHandler(t)
	h.downloadISO(srv.URL+"/plain.iso", "plain.iso", filepath.Join(dir, "plain.iso"), "", false)

	if got := readISO(t, dir, "plain.iso"); !bytes.Equal(got, payload) {
		t.Fatalf("downloaded %d bytes, want %d", len(got), len(payload))
	}
	assertNoTemp(t, dir, "plain.iso")
}

func TestDownloadISOResumesFromPartial(t *testing.T) {
	payload := bytes.Repeat([]byte("b"), 300*1024)
	var seenRange, seenStatus string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenRange = r.Header.Get("Range")
		rec := httptest.NewRecorder()
		http.ServeContent(rec, r, "test.iso", time.Unix(1700000000, 0), bytes.NewReader(payload))
		seenStatus = strconv.Itoa(rec.Code)
		relay(rec, w)
	}))
	defer srv.Close()

	h, dir := newTestHandler(t)
	name := "resume.iso"
	writePartial(t, dir, name, payload[:120*1024], srv.URL+"/"+name)

	h.downloadISO(srv.URL+"/"+name, name, filepath.Join(dir, name), "", false)

	if seenRange != "bytes=122880-" {
		t.Errorf("Range header = %q, want %q (partial was not resumed)", seenRange, "bytes=122880-")
	}
	if seenStatus != "206" {
		t.Errorf("status = %s, want 206", seenStatus)
	}

	got := readISO(t, dir, name)
	if !bytes.Equal(got, payload) {
		t.Fatalf("resumed file has %d bytes, want %d (duplicated or truncated data)", len(got), len(payload))
	}
	assertNoTemp(t, dir, name)
}

func TestDownloadISORestartsWhenSourceIgnoresRange(t *testing.T) {
	payload := bytes.Repeat([]byte("c"), 100*1024)
	// Always answers 200 with the whole body, even when a Range is requested.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		w.Write(payload)
	}))
	defer srv.Close()

	h, dir := newTestHandler(t)
	name := "norange.iso"
	writePartial(t, dir, name, payload[:40*1024], srv.URL+"/"+name)

	h.downloadISO(srv.URL+"/"+name, name, filepath.Join(dir, name), "", false)

	got := readISO(t, dir, name)
	if !bytes.Equal(got, payload) {
		t.Fatalf("file has %d bytes, want %d (stale partial was not discarded)", len(got), len(payload))
	}
	assertNoTemp(t, dir, name)
}

func TestDownloadISORestartsWhenEntityChanged(t *testing.T) {
	payload := bytes.Repeat([]byte("d"), 120*1024)
	srv := rangeServer(t, payload)
	defer srv.Close()

	h, dir := newTestHandler(t)
	name := "changed.iso"
	writePartial(t, dir, name, payload[:50*1024], srv.URL+"/"+name)
	// Pretend the partial came from an older build of the same file.
	metaPath := filepath.Join(dir, name+".part.json")
	if err := os.WriteFile(metaPath, []byte(`{"url":"`+srv.URL+"/"+name+`","etag":"\"old\"","total":122880}`), 0o644); err != nil {
		t.Fatalf("write meta: %v", err)
	}

	h.downloadISO(srv.URL+"/"+name, name, filepath.Join(dir, name), "", false)

	if got := readISO(t, dir, name); !bytes.Equal(got, payload) {
		t.Fatalf("file has %d bytes, want %d (If-Range mismatch not handled)", len(got), len(payload))
	}
	assertNoTemp(t, dir, name)
}

func TestDownloadISODiscardsPartialFromOtherURL(t *testing.T) {
	payload := bytes.Repeat([]byte("e"), 90*1024)
	srv := rangeServer(t, payload)
	defer srv.Close()

	h, dir := newTestHandler(t)
	name := "otherurl.iso"
	writePartial(t, dir, name, bytes.Repeat([]byte("z"), 30*1024), "https://elsewhere.invalid/"+name)

	h.downloadISO(srv.URL+"/"+name, name, filepath.Join(dir, name), "", false)

	if got := readISO(t, dir, name); !bytes.Equal(got, payload) {
		t.Fatalf("file has %d bytes, want %d (partial from another URL was reused)", len(got), len(payload))
	}
	assertNoTemp(t, dir, name)
}

// relay copies a recorded response (including Content-Range) onto the real writer.
func relay(rec *httptest.ResponseRecorder, w http.ResponseWriter) {
	for key, values := range rec.Header() {
		for _, v := range values {
			w.Header().Add(key, v)
		}
	}
	w.WriteHeader(rec.Code)
	w.Write(rec.Body.Bytes())
}

func TestDownloadISOFinishesCompletePartial(t *testing.T) {
	payload := bytes.Repeat([]byte("f"), 64*1024)
	var seenStatus string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := httptest.NewRecorder()
		http.ServeContent(rec, r, "test.iso", time.Unix(1700000000, 0), bytes.NewReader(payload))
		seenStatus = strconv.Itoa(rec.Code)
		relay(rec, w)
	}))
	defer srv.Close()

	h, dir := newTestHandler(t)
	name := "complete.iso"
	// The partial already holds every byte; a Range request gets 416.
	writePartial(t, dir, name, payload, srv.URL+"/"+name)

	h.downloadISO(srv.URL+"/"+name, name, filepath.Join(dir, name), "", false)

	if seenStatus != "416" {
		t.Errorf("status = %s, want 416", seenStatus)
	}
	if got := readISO(t, dir, name); !bytes.Equal(got, payload) {
		t.Fatalf("file has %d bytes, want %d", len(got), len(payload))
	}
	assertNoTemp(t, dir, name)
}

func TestCleanupStaleDownloads(t *testing.T) {
	h, dir := newTestHandler(t)

	stale := filepath.Join(dir, "stale.iso.part")
	if err := os.WriteFile(stale, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-30 * 24 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	writeDownloadMeta(filepath.Join(dir, "stale.iso"), &downloadPartMeta{URL: "https://example.com/stale.iso"})
	if err := os.Chtimes(filepath.Join(dir, "stale.iso.part.json"), old, old); err != nil {
		t.Fatal(err)
	}

	fresh := filepath.Join(dir, "fresh.iso.part")
	if err := os.WriteFile(fresh, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	orphanMeta := filepath.Join(dir, "gone.iso.part.json")
	if err := os.WriteFile(orphanMeta, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	h.cleanupStaleDownloads(7 * 24 * time.Hour)

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale partial was not removed")
	}
	if _, err := os.Stat(stale + ".json"); !os.IsNotExist(err) {
		t.Errorf("stale metadata was not removed")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("fresh partial should be kept for resume: %v", err)
	}
	if _, err := os.Stat(orphanMeta); !os.IsNotExist(err) {
		t.Errorf("metadata without data file was not removed")
	}
}

func TestCleanupKeepsActiveDownload(t *testing.T) {
	h, dir := newTestHandler(t)
	name := "active.iso"
	part := filepath.Join(dir, name+".part")
	if err := os.WriteFile(part, []byte("running"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-30 * 24 * time.Hour)
	if err := os.Chtimes(part, old, old); err != nil {
		t.Fatal(err)
	}

	downloadMgr.TryBegin("https://example.com/"+name, name)
	defer func() {
		if p := downloadMgr.Get(name); p != nil {
			downloadMgr.Error(name, "test finished")
		}
	}()

	h.cleanupStaleDownloads(time.Hour)

	if _, err := os.Stat(part); err != nil {
		t.Errorf("active partial was removed: %v", err)
	}
}

// Guard against the metadata format drifting: it is read back by a different
// code path (restart) than the one that writes it.
func TestDownloadMetaRoundTrip(t *testing.T) {
	_, dir := newTestHandler(t)
	dest := filepath.Join(dir, "round.iso")
	want := &downloadPartMeta{URL: "https://example.com/round.iso", ETag: `"abc"`, Total: 12345}
	writeDownloadMeta(dest, want)

	got := readDownloadMeta(dest)
	if got == nil {
		t.Fatal("metadata not readable")
	}
	if got.URL != want.URL || got.ETag != want.ETag || got.Total != want.Total {
		t.Fatalf("round trip mismatch: %+v", got)
	}

	raw, err := os.ReadFile(downloadMetaPath(dest))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"total":`+strconv.FormatInt(want.Total, 10)) {
		t.Errorf("unexpected metadata payload: %s", raw)
	}
}
