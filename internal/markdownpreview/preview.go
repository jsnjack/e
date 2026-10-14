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
typedef void (*web_view_evaluate_javascript_fn)(void *, const char *, gssize, const char *, const char *, void *, void *, void *);

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

static void bridge_web_view_evaluate_javascript(GtkWidget *view, const char *script) {
  if (!view || !ensure_webkit()) return;
  web_view_evaluate_javascript_fn evaluate = (web_view_evaluate_javascript_fn)dlsym(webkit_lib, "webkit_web_view_evaluate_javascript");
  if (!evaluate) { webkit_error = dlerror(); return; }
  evaluate(view, script, -1, NULL, NULL, NULL, NULL, NULL);
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
	"encoding/json"
	"fmt"
	"html"
	"regexp"
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

var linkDestination = regexp.MustCompile(` href="([^"]*)"`)

// loadFinished is WEBKIT_LOAD_FINISHED from WebKitLoadEvent.
const loadFinished = 3

// View is a sandboxed Markdown presentation surface. It loads the page shell
// once per base URI and then replaces the body in place, which keeps the
// scroll position and avoids a blank frame on every edit.
type View struct {
	Widget *gtk.Widget

	// hasShell reports that the last full load was the preview shell.
	hasShell bool
	// baseURI is the base of the last full load of the shell.
	baseURI string
	// loaded is false from a full load until WebKit reports it finished.
	loaded bool
	// shellPage is the page that the last full load embedded in the shell.
	shellPage Page
	// latest is the most recent page passed to Show.
	latest Page
}

// Page is rendered preview content: a sanitized body fragment plus the script
// that installs it into an already loaded shell.
type Page struct {
	title  string
	body   string
	script string
}

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
	view := &View{Widget: widget}
	coreglib.BaseObject(widget).Connect("load-changed", func(event int) {
		if event == loadFinished {
			view.loadFinished()
		}
	})
	return view, nil
}

// Show displays page. It replaces the body of the loaded shell in place when
// the base URI is unchanged, and does a full load otherwise.
func (v *View) Show(page Page, baseURI string) {
	v.latest = page
	if !v.hasShell || baseURI != v.baseURI {
		v.loadShell(page, baseURI)
		return
	}
	if v.loaded {
		v.evaluate(page.script)
	}
	// While a full load is in flight, loadFinished installs v.latest.
}

// Load displays a complete generated document. Remote loads are blocked by its
// CSP; relative local images can resolve through baseURI. A later Show does a
// full load, because the document may not contain the preview shell.
func (v *View) Load(document, baseURI string) {
	v.hasShell = false
	v.loaded = false
	v.load(document, baseURI)
}

func (v *View) loadShell(page Page, baseURI string) {
	v.hasShell = true
	v.baseURI = baseURI
	v.loaded = false
	v.shellPage = page
	v.load(shell(page.title, page.body), baseURI)
}

// loadFinished runs for every WEBKIT_LOAD_FINISHED. A cancelled load also
// reports it, possibly before the replacing load completes; installing the
// latest body again on the next report keeps the final state correct.
func (v *View) loadFinished() {
	if !v.hasShell {
		return
	}
	v.loaded = true
	if v.latest != v.shellPage {
		v.evaluate(v.latest.script)
	}
}

func (v *View) load(document, baseURI string) {
	cDocument := C.CString(document)
	defer C.free(unsafe.Pointer(cDocument))
	var cBaseURI *C.char
	if baseURI != "" {
		cBaseURI = C.CString(baseURI)
		defer C.free(unsafe.Pointer(cBaseURI))
	}
	C.bridge_web_view_load_html(v.native(), cDocument, cBaseURI)
	runtime.KeepAlive(v)
}

func (v *View) evaluate(script string) {
	if script == "" {
		return
	}
	cScript := C.CString(script)
	defer C.free(unsafe.Pointer(cScript))
	C.bridge_web_view_evaluate_javascript(v.native(), cScript)
	runtime.KeepAlive(v)
}

func (v *View) native() *C.GtkWidget {
	return C.bridge_widget(C.uintptr_t(coreglib.BaseObject(v.Widget).Native()))
}

// RenderPage converts Markdown to a page for Show. It does all slow work, so
// callers can run it off the GTK thread.
func RenderPage(source []byte, title string) (Page, error) {
	body, err := renderBody(source)
	if err != nil {
		return Page{}, err
	}
	script, err := updateScript(title, body)
	if err != nil {
		return Page{}, err
	}
	return Page{title: title, body: body, script: script}, nil
}

// Render converts Markdown to a self-contained, GNOME-themed HTML document.
// Goldmark's safe default omits raw HTML rather than executing it.
func Render(source []byte, title string) (string, error) {
	body, err := renderBody(source)
	if err != nil {
		return "", err
	}
	return shell(title, body), nil
}

// renderBody converts Markdown to a sanitized body fragment. Goldmark runs
// without the unsafe option, so raw HTML is omitted, and links lose href.
func renderBody(source []byte) (string, error) {
	var body bytes.Buffer
	if err := renderer.Convert(source, &body); err != nil {
		return "", fmt.Errorf("render Markdown: %w", err)
	}
	return linkDestination.ReplaceAllString(body.String(), ` title="$1"`), nil
}

// updateScript builds JavaScript that installs body and title into a loaded
// shell. Both are JSON string literals, so no input can end the literal or the
// statement; Go's encoder also escapes <, >, &, U+2028 and U+2029.
func updateScript(title, body string) (string, error) {
	encodedBody, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("encode Markdown preview body: %w", err)
	}
	encodedTitle, err := json.Marshal(title)
	if err != nil {
		return "", fmt.Errorf("encode Markdown preview title: %w", err)
	}
	return "document.title = " + string(encodedTitle) + ";\ndocument.body.innerHTML = " + string(encodedBody) + ";\n", nil
}

// shell wraps a sanitized body fragment in the preview document and its CSP.
func shell(title, body string) string {
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
</style></head><body>` + body + `</body></html>`
}
