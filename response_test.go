package muxmaster_test

import (
	"encoding/xml"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	muxmaster "github.com/FlavioCFOliveira/MuxMaster"
)

func TestJSON_HappyPath(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := muxmaster.JSON(rec, http.StatusCreated, map[string]int{"a": 1}); err != nil {
		t.Fatalf("JSON err: %v", err)
	}
	if rec.Code != http.StatusCreated {
		t.Errorf("code=%d want 201", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type=%q", ct)
	}
	if got := rec.Body.String(); got != `{"a":1}` {
		t.Errorf("body=%q", got)
	}
}

func TestJSON_DefaultStatusCode(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := muxmaster.JSON(rec, 0, []int{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("code=%d want 200 (default)", rec.Code)
	}
}

func TestJSON_MarshalError(t *testing.T) {
	rec := httptest.NewRecorder()
	// math.NaN cannot be marshalled to JSON.
	err := muxmaster.JSON(rec, http.StatusOK, math.NaN())
	if err == nil {
		t.Error("expected marshal error for NaN")
	}
}

type unmarshallable struct{ Ch chan int }

func TestJSON_UnsupportedType(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := muxmaster.JSON(rec, http.StatusOK, unmarshallable{Ch: make(chan int)}); err == nil {
		t.Error("expected error for chan")
	}
}

// failingWriter triggers Write error path.
type failingWriter struct{ http.ResponseWriter }

func (f *failingWriter) Write(b []byte) (int, error) {
	return 0, errors.New("write failed")
}

func TestJSON_WriteError(t *testing.T) {
	rec := httptest.NewRecorder()
	w := &failingWriter{ResponseWriter: rec}
	if err := muxmaster.JSON(w, http.StatusOK, map[string]int{"a": 1}); err == nil {
		t.Error("expected write error")
	}
}

func TestXML_HappyPath(t *testing.T) {
	type Foo struct {
		XMLName xml.Name `xml:"foo"`
		V       int      `xml:"v"`
	}
	rec := httptest.NewRecorder()
	if err := muxmaster.XML(rec, http.StatusOK, Foo{V: 42}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rec.Body.String(), "<foo><v>42</v></foo>") {
		t.Errorf("body=%q", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/xml; charset=utf-8" {
		t.Errorf("ct=%q", ct)
	}
}

func TestXML_DefaultStatusCode(t *testing.T) {
	type Bar struct{ V int }
	rec := httptest.NewRecorder()
	_ = muxmaster.XML(rec, 0, Bar{V: 1})
	if rec.Code != http.StatusOK {
		t.Errorf("code=%d want 200", rec.Code)
	}
}

func TestXML_MarshalError(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := muxmaster.XML(rec, http.StatusOK, make(chan int)); err == nil {
		t.Error("expected marshal error")
	}
}

func TestXML_WriteError(t *testing.T) {
	type Foo struct{ V int }
	rec := httptest.NewRecorder()
	w := &failingWriter{ResponseWriter: rec}
	if err := muxmaster.XML(w, http.StatusOK, Foo{V: 1}); err == nil {
		t.Error("expected write error")
	}
}

func TestText_HappyPath(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := muxmaster.Text(rec, http.StatusAccepted, "hello"); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusAccepted {
		t.Errorf("code=%d", rec.Code)
	}
	if rec.Body.String() != "hello" {
		t.Errorf("body=%q", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Errorf("ct=%q", ct)
	}
}

func TestText_DefaultStatusCode(t *testing.T) {
	rec := httptest.NewRecorder()
	_ = muxmaster.Text(rec, 0, "ok")
	if rec.Code != http.StatusOK {
		t.Errorf("code=%d want 200", rec.Code)
	}
}

func TestText_Unicode(t *testing.T) {
	rec := httptest.NewRecorder()
	_ = muxmaster.Text(rec, http.StatusOK, "olá — 日本語")
	if !strings.Contains(rec.Body.String(), "日本語") {
		t.Errorf("unicode lost: %q", rec.Body.String())
	}
}

func TestRedirect(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/here", nil)
	muxmaster.Redirect(rec, req, http.StatusFound, "/there")
	if rec.Code != http.StatusFound {
		t.Errorf("code=%d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/there" {
		t.Errorf("Location=%q", loc)
	}
}

func TestNoContent(t *testing.T) {
	rec := httptest.NewRecorder()
	muxmaster.NoContent(rec)
	if rec.Code != http.StatusNoContent {
		t.Errorf("code=%d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body should be empty: %q", rec.Body.String())
	}
}

// Sanity: large JSON payloads stream correctly.
func TestJSON_LargePayload(t *testing.T) {
	big := make([]int, 10000)
	for i := range big {
		big[i] = i
	}
	rec := httptest.NewRecorder()
	if err := muxmaster.JSON(rec, http.StatusOK, big); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(rec.Body.String(), "[0,1,2") {
		t.Errorf("unexpected body prefix")
	}
}
