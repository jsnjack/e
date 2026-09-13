// Package markdownpreview renders Markdown in a lazily created WebKitGTK view.
package markdownpreview

/*
#cgo pkg-config: gtk4
#cgo LDFLAGS: -ldl
#include <gtk/gtk.h>
#include <dlfcn.h>
#include <stdint.h>
#include <stdlib.h>

static void *webkit_lib;
static const char *webkit_error;

typedef GtkWidget *(*web_view_new_fn)(void);
typedef void (*web_view_load_html_fn)(void *, const char *, const char *);

static gboolean ensure_webkit(void) {
  if (webkit_lib) return TRUE;
  webkit_lib = dlopen("libwebkitgtk-6.0.so.4", RTLD_NOW | RTLD_LOCAL);
  if (!webkit_lib) { webkit_error = dlerror(); return FALSE; }
  return TRUE;
}

static GtkWidget *bridge_web_view_new(void) {
  if (!ensure_webkit()) return NULL;
  web_view_new_fn make = (web_view_new_fn)dlsym(webkit_lib, "webkit_web_view_new");
  if (!make) { webkit_error = dlerror(); return NULL; }
  return make();
}

static void bridge_web_view_load_html(GtkWidget *view, const char *html, const char *base_uri) {
  if (!view || !ensure_webkit()) return;
  web_view_load_html_fn load = (web_view_load_html_fn)dlsym(webkit_lib, "webkit_web_view_load_html");
  if (!load) { webkit_error = dlerror(); return; }
  load(view, html, base_uri);
}

static const char *bridge_webkit_error(void) {
  return webkit_error ? webkit_error : "WebKitGTK 6 is unavailable";
}

static GtkWidget *bridge_widget(uintptr_t pointer) {
  return (GtkWidget *)pointer;
}
*/
import "C"

import (
	"bytes"
	"fmt"
	"html"
	"runtime"
	"unsafe"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	gmhtml "github.com/yuin/goldmark/renderer/html"
)

var renderer = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithRendererOptions(gmhtml.WithHardWraps()),
)

// View is a sandboxed Markdown presentation surface.
type View struct{ Widget *gtk.Widget }

// New creates WebKit only when preview is first requested.
func New() (*View, error) {
	pointer := C.bridge_web_view_new()
	if pointer == nil {
		return nil, fmt.Errorf("create Markdown preview: %s", C.GoString(C.bridge_webkit_error()))
	}
	object := coreglib.Take(unsafe.Pointer(pointer))
	widget, ok := object.CastType(gtk.GTypeWidget).(*gtk.Widget)
	if !ok {
		return nil, fmt.Errorf("wrap Markdown preview as GTK widget")
	}
	widget.SetHExpand(true)
	widget.SetVExpand(true)
	return &View{Widget: widget}, nil
}

// Load displays a complete generated document. Remote loads are blocked by its
// CSP; relative local images can resolve through baseURI.
func (v *View) Load(document, baseURI string) {
	cDocument := C.CString(document)
	defer C.free(unsafe.Pointer(cDocument))
	var cBaseURI *C.char
	if baseURI != "" {
		cBaseURI = C.CString(baseURI)
		defer C.free(unsafe.Pointer(cBaseURI))
	}
	C.bridge_web_view_load_html(
		C.bridge_widget(C.uintptr_t(coreglib.BaseObject(v.Widget).Native())),
		cDocument,
		cBaseURI,
	)
	runtime.KeepAlive(v)
}

// Render converts Markdown to a self-contained, GNOME-themed HTML document.
// Goldmark's safe default omits raw HTML rather than executing it.
func Render(source []byte, title string) (string, error) {
	var body bytes.Buffer
	if err := renderer.Convert(source, &body); err != nil {
		return "", fmt.Errorf("render Markdown: %w", err)
	}
	return `<!doctype html><html><head><meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src file: data:; style-src 'unsafe-inline'">
<meta name="color-scheme" content="light dark"><title>` + html.EscapeString(title) + `</title>
<style>
:root { color-scheme: light dark; font: 16px/1.6 system-ui, sans-serif; }
body { box-sizing: border-box; max-width: 880px; margin: 0 auto; padding: 32px 38px 80px; color: #2e3436; background: #fff; }
h1,h2,h3,h4 { line-height: 1.25; margin: 1.5em 0 .55em; } h1 { font-size: 2em; border-bottom: 1px solid #deddda; padding-bottom: .25em; }
a { color: #1c71d8; } img { max-width: 100%; } blockquote { margin-left: 0; padding-left: 1em; border-left: 4px solid #99c1f1; color: #5e5c64; }
code,pre { font-family: ui-monospace, monospace; background: #f6f5f4; border-radius: 6px; } code { padding: .12em .3em; } pre { overflow: auto; padding: 14px; }
table { border-collapse: collapse; } th,td { border: 1px solid #c0bfbc; padding: 6px 10px; } hr { border: 0; border-top: 1px solid #deddda; }
@media (prefers-color-scheme: dark) { body { color: #f6f5f4; background: #242424; } h1 { border-color: #5e5c64; } a { color: #62a0ea; } code,pre { background: #303030; } th,td { border-color: #77767b; } blockquote { color: #c0bfbc; } }
</style></head><body>` + body.String() + `</body></html>`, nil
}
