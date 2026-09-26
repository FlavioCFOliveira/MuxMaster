package middleware

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"testing"
	"time"
)

// oauth2TransportCopied lists the http.Transport fields that
// oauth2TransportFrom copies from the source transport.
var oauth2TransportCopied = map[string]bool{
	"Proxy":                  true,
	"OnProxyConnectResponse": true,
	"DialContext":            true,
	"Dial":                   true,
	"DialTLSContext":         true,
	"DialTLS":                true,
	"TLSClientConfig":        true,
	"TLSHandshakeTimeout":    true,
	"DisableCompression":     true,
	"IdleConnTimeout":        true,
	"ResponseHeaderTimeout":  true,
	"ExpectContinueTimeout":  true,
	"ProxyConnectHeader":     true,
	"GetProxyConnectHeader":  true,
	"MaxResponseHeaderBytes": true,
	"WriteBufferSize":        true,
	"ReadBufferSize":         true,
	"ForceAttemptHTTP2":      true,
	"HTTP2":                  true,
	"Protocols":              true,
}

// oauth2TransportNotCopied lists the http.Transport fields that
// oauth2TransportFrom deliberately does not copy, with the reason documented
// on oauth2TransportFrom.
var oauth2TransportNotCopied = map[string]string{
	"TLSNextProto":        "bound to the source transport; only the documented disable-HTTP/2 signal is honoured",
	"DisableKeepAlives":   "forced to false: keep-alive reuse is what bounds the sockets",
	"MaxIdleConns":        "overridden with oauth2DefaultMaxConnsPerHost",
	"MaxIdleConnsPerHost": "overridden with oauth2DefaultMaxConnsPerHost",
	"MaxConnsPerHost":     "overridden with oauth2DefaultMaxConnsPerHost",
}

// TestOAuth2TransportFrom_EveryFieldClassified fails when the running Go
// version's http.Transport has an exported field that oauth2TransportFrom
// neither copies nor deliberately skips — so a field added by a future Go
// release is reviewed instead of being silently dropped — and when a
// classified field no longer exists.
func TestOAuth2TransportFrom_EveryFieldClassified(t *testing.T) {
	typ := reflect.TypeFor[http.Transport]()
	present := map[string]bool{}
	var unclassified []string
	for i := range typ.NumField() {
		f := typ.Field(i)
		if !f.IsExported() {
			continue
		}
		present[f.Name] = true
		_, skipped := oauth2TransportNotCopied[f.Name]
		if oauth2TransportCopied[f.Name] && skipped {
			t.Errorf("field %s is classified as both copied and not copied", f.Name)
		}
		if !oauth2TransportCopied[f.Name] && !skipped {
			unclassified = append(unclassified, f.Name)
		}
	}
	sort.Strings(unclassified)
	if len(unclassified) > 0 {
		t.Errorf("http.Transport has exported fields that oauth2TransportFrom does not classify: %v — decide whether to copy each one (and update the classification here and the list in oauth2TransportFrom's doc comment)", unclassified)
	}
	for name := range oauth2TransportCopied {
		if !present[name] {
			t.Errorf("classified field %s no longer exists in http.Transport", name)
		}
	}
	for name := range oauth2TransportNotCopied {
		if !present[name] {
			t.Errorf("classified field %s no longer exists in http.Transport", name)
		}
	}
}

// oauth2NonZeroValue returns a non-zero value of type typ for the kinds
// http.Transport's exported fields use.
func oauth2NonZeroValue(t *testing.T, name string, typ reflect.Type) reflect.Value {
	t.Helper()
	switch typ.Kind() {
	case reflect.Func:
		return reflect.MakeFunc(typ, func([]reflect.Value) []reflect.Value {
			out := make([]reflect.Value, typ.NumOut())
			for i := range out {
				out[i] = reflect.Zero(typ.Out(i))
			}
			return out
		})
	case reflect.Pointer:
		return reflect.New(typ.Elem())
	case reflect.Map:
		m := reflect.MakeMap(typ)
		m.SetMapIndex(reflect.New(typ.Key()).Elem(), reflect.New(typ.Elem()).Elem())
		return m
	case reflect.Bool:
		return reflect.ValueOf(true).Convert(typ)
	case reflect.Int, reflect.Int64:
		return reflect.ValueOf(int64(7)).Convert(typ)
	default:
		t.Fatalf("field %s has unsupported kind %s; extend oauth2NonZeroValue", name, typ.Kind())
		return reflect.Value{}
	}
}

// TestOAuth2TransportFrom_CopiesClassifiedFields sets every exported field of
// a source transport to a non-zero value and checks that oauth2TransportFrom
// carries every copied field over (reference-typed configuration is cloned,
// not aliased) and applies the documented value to every other field.
func TestOAuth2TransportFrom_CopiesClassifiedFields(t *testing.T) {
	src := &http.Transport{}
	sv := reflect.ValueOf(src).Elem()
	typ := sv.Type()
	for i := range typ.NumField() {
		f := typ.Field(i)
		if !f.IsExported() || f.Name == "TLSNextProto" {
			continue // TLSNextProto is covered by TestOAuth2TransportFrom_TLSNextProto.
		}
		sv.Field(i).Set(oauth2NonZeroValue(t, f.Name, f.Type))
	}

	dst := oauth2TransportFrom(src)
	dv := reflect.ValueOf(dst).Elem()
	for name := range oauth2TransportCopied {
		s, d := sv.FieldByName(name), dv.FieldByName(name)
		if d.IsZero() {
			t.Errorf("%s was not copied", name)
			continue
		}
		switch s.Kind() {
		case reflect.Func:
			if s.Pointer() != d.Pointer() {
				t.Errorf("%s: copied function differs from the source", name)
			}
		case reflect.Pointer, reflect.Map:
			if s.Pointer() == d.Pointer() {
				t.Errorf("%s: aliased to the source instead of cloned", name)
			}
		default:
			if !reflect.DeepEqual(s.Interface(), d.Interface()) {
				t.Errorf("%s = %v, want %v", name, d.Interface(), s.Interface())
			}
		}
	}

	if dst.DisableKeepAlives {
		t.Error("DisableKeepAlives was copied; it must stay false")
	}
	for _, got := range []struct {
		name string
		v    int
	}{
		{"MaxIdleConns", dst.MaxIdleConns},
		{"MaxIdleConnsPerHost", dst.MaxIdleConnsPerHost},
		{"MaxConnsPerHost", dst.MaxConnsPerHost},
	} {
		if got.v != oauth2DefaultMaxConnsPerHost {
			t.Errorf("%s = %d, want %d", got.name, got.v, oauth2DefaultMaxConnsPerHost)
		}
	}
}

// TestOAuth2TransportFrom_DoesNotInitialiseSource checks, at unit level, the
// property behind the rmp #300 follow-up: building the transport must not
// run the source's lazy HTTP/2 initialisation, which would populate the
// source's TLSClientConfig and TLSNextProto.
func TestOAuth2TransportFrom_DoesNotInitialiseSource(t *testing.T) {
	src := &http.Transport{ForceAttemptHTTP2: true}
	_ = oauth2TransportFrom(src)
	if src.TLSClientConfig != nil || src.TLSNextProto != nil || src.HTTP2 != nil {
		t.Fatalf("source transport was initialised: TLSClientConfig=%v TLSNextProto=%v HTTP2=%v",
			src.TLSClientConfig, src.TLSNextProto, src.HTTP2)
	}
}

// TestOAuth2TransportFrom_TLSNextProto covers the one field copied by
// interpretation rather than by value.
func TestOAuth2TransportFrom_TLSNextProto(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()
	srvTLS := srv.Client().Transport.(*http.Transport).TLSClientConfig

	protoVia := func(t *testing.T, tr *http.Transport) string {
		t.Helper()
		defer tr.CloseIdleConnections()
		resp, err := (&http.Client{Transport: tr, Timeout: 10 * time.Second}).Get(srv.URL)
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		_ = resp.Body.Close()
		return resp.Proto
	}
	newSrc := func() *http.Transport {
		return &http.Transport{
			ForceAttemptHTTP2: true,
			TLSClientConfig:   &tls.Config{RootCAs: srvTLS.RootCAs, MinVersion: tls.VersionTLS12},
		}
	}

	t.Run("source with initialised HTTP/2 keeps HTTP/2", func(t *testing.T) {
		src := newSrc()
		_ = src.Clone() // runs src's HTTP/2 initialisation: TLSNextProto gets its "h2" stub
		if src.TLSNextProto["h2"] == nil {
			t.Fatal("precondition: source TLSNextProto has no h2 entry after initialisation")
		}
		dst := oauth2TransportFrom(src)
		if dst.TLSNextProto != nil {
			t.Fatalf("TLSNextProto copied from an HTTP/2-initialised source: %v", dst.TLSNextProto)
		}
		if got := protoVia(t, dst); got != "HTTP/2.0" {
			t.Fatalf("proto = %s, want HTTP/2.0", got)
		}
	})

	t.Run("empty non-nil map disables HTTP/2", func(t *testing.T) {
		src := newSrc()
		src.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
		dst := oauth2TransportFrom(src)
		if dst.TLSNextProto == nil || len(dst.TLSNextProto) != 0 {
			t.Fatalf("TLSNextProto = %v, want empty non-nil map", dst.TLSNextProto)
		}
		if got := protoVia(t, dst); got != "HTTP/1.1" {
			t.Fatalf("proto = %s, want HTTP/1.1", got)
		}
	})

	t.Run("nil map leaves HTTP/2 enabled", func(t *testing.T) {
		dst := oauth2TransportFrom(newSrc())
		if dst.TLSNextProto != nil {
			t.Fatalf("TLSNextProto = %v, want nil", dst.TLSNextProto)
		}
		if got := protoVia(t, dst); got != "HTTP/2.0" {
			t.Fatalf("proto = %s, want HTTP/2.0", got)
		}
	})
}
