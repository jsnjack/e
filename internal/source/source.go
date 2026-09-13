// Package source bridges gotk4 to GtkSourceView 5 and libspelling.
package source

/*
#cgo pkg-config: gtk4
#cgo LDFLAGS: -ldl
#include <gtk/gtk.h>
#include <dlfcn.h>
#include <stdint.h>
#include <stdlib.h>

typedef struct _GtkSourceBuffer GtkSourceBuffer;
typedef struct _GtkSourceLanguage GtkSourceLanguage;
typedef struct _GtkSourceLanguageManager GtkSourceLanguageManager;
typedef struct _SpellingChecker SpellingChecker;
typedef struct _SpellingTextBufferAdapter SpellingTextBufferAdapter;

static void *source_lib;
static void *spelling_lib;
static const char *bridge_error;

typedef void (*source_init_fn)(void);
typedef GtkWidget *(*source_view_new_fn)(void);
typedef GtkSourceLanguageManager *(*language_manager_default_fn)(void);
typedef GtkSourceLanguage *(*guess_language_fn)(GtkSourceLanguageManager *, const char *, const char *);
typedef void (*buffer_set_language_fn)(GtkSourceBuffer *, GtkSourceLanguage *);
typedef void (*buffer_set_bool_fn)(GtkSourceBuffer *, gboolean);
typedef void (*view_set_bool_fn)(void *, gboolean);
typedef void (*view_set_uint_fn)(void *, guint);
typedef void (*view_set_int_fn)(void *, gint);

static void *sym(void *lib, const char *name) {
  void *value = dlsym(lib, name);
  if (!value) bridge_error = dlerror();
  return value;
}

static gboolean ensure_source(void) {
  if (source_lib) return TRUE;
  source_lib = dlopen("libgtksourceview-5.so.0", RTLD_NOW | RTLD_LOCAL);
  if (!source_lib) { bridge_error = dlerror(); return FALSE; }
  source_init_fn init = (source_init_fn)sym(source_lib, "gtk_source_init");
  if (!init) return FALSE;
  init();
  return TRUE;
}

static GtkWidget *bridge_source_view_new(void) {
  if (!ensure_source()) return NULL;
  source_view_new_fn make = (source_view_new_fn)sym(source_lib, "gtk_source_view_new");
  if (!make) return NULL;
  return make();
}

static const char *bridge_last_error(void) {
  return bridge_error ? bridge_error : "native editor library is unavailable";
}

static gboolean bridge_guess_language(GtkTextView *view, const char *filename, const char *content_type) {
  if (!ensure_source()) return FALSE;
  language_manager_default_fn get_manager = (language_manager_default_fn)sym(source_lib, "gtk_source_language_manager_get_default");
  guess_language_fn guess = (guess_language_fn)sym(source_lib, "gtk_source_language_manager_guess_language");
  buffer_set_language_fn set_language = (buffer_set_language_fn)sym(source_lib, "gtk_source_buffer_set_language");
  if (!get_manager || !guess || !set_language) return FALSE;
  GtkSourceLanguage *language = guess(get_manager(), filename, content_type);
  set_language((GtkSourceBuffer *)gtk_text_view_get_buffer(view), language);
  return language != NULL;
}

static void bridge_set_options(GtkTextView *view, gboolean line_numbers, gboolean current_line,
                               gboolean spaces, guint tab_width, gint indent_width) {
  if (!ensure_source()) return;
  view_set_bool_fn set_lines = (view_set_bool_fn)sym(source_lib, "gtk_source_view_set_show_line_numbers");
  view_set_bool_fn set_current = (view_set_bool_fn)sym(source_lib, "gtk_source_view_set_highlight_current_line");
  view_set_bool_fn set_auto = (view_set_bool_fn)sym(source_lib, "gtk_source_view_set_auto_indent");
  view_set_bool_fn set_indent_tab = (view_set_bool_fn)sym(source_lib, "gtk_source_view_set_indent_on_tab");
  view_set_bool_fn set_spaces = (view_set_bool_fn)sym(source_lib, "gtk_source_view_set_insert_spaces_instead_of_tabs");
  view_set_uint_fn set_tab = (view_set_uint_fn)sym(source_lib, "gtk_source_view_set_tab_width");
  view_set_int_fn set_indent = (view_set_int_fn)sym(source_lib, "gtk_source_view_set_indent_width");
  if (set_lines) set_lines(view, line_numbers);
  if (set_current) set_current(view, current_line);
  if (set_auto) set_auto(view, TRUE);
  if (set_indent_tab) set_indent_tab(view, TRUE);
  if (set_spaces) set_spaces(view, spaces);
  if (set_tab) set_tab(view, tab_width);
  if (set_indent) set_indent(view, indent_width);
}

static void bridge_set_highlight(GtkTextView *view, gboolean enabled) {
  if (!ensure_source()) return;
  buffer_set_bool_fn set = (buffer_set_bool_fn)sym(source_lib, "gtk_source_buffer_set_highlight_syntax");
  if (set) set((GtkSourceBuffer *)gtk_text_view_get_buffer(view), enabled);
}

typedef void (*spelling_init_fn)(void);
typedef SpellingChecker *(*checker_default_fn)(void);
typedef SpellingTextBufferAdapter *(*adapter_new_fn)(GtkSourceBuffer *, SpellingChecker *);
typedef GMenuModel *(*adapter_menu_fn)(SpellingTextBufferAdapter *);
typedef void (*adapter_enable_fn)(SpellingTextBufferAdapter *, gboolean);

static SpellingTextBufferAdapter *bridge_attach_spelling(GtkTextView *view, gboolean enabled) {
  if (!spelling_lib) {
    spelling_lib = dlopen("libspelling-1.so.2", RTLD_NOW | RTLD_LOCAL);
    if (!spelling_lib) return NULL;
    spelling_init_fn init = (spelling_init_fn)sym(spelling_lib, "spelling_init");
    if (init) init();
  }
  checker_default_fn checker_default = (checker_default_fn)sym(spelling_lib, "spelling_checker_get_default");
  adapter_new_fn adapter_new = (adapter_new_fn)sym(spelling_lib, "spelling_text_buffer_adapter_new");
  adapter_menu_fn get_menu = (adapter_menu_fn)sym(spelling_lib, "spelling_text_buffer_adapter_get_menu_model");
  adapter_enable_fn set_enabled = (adapter_enable_fn)sym(spelling_lib, "spelling_text_buffer_adapter_set_enabled");
  if (!checker_default || !adapter_new || !get_menu || !set_enabled) return NULL;
  SpellingTextBufferAdapter *adapter = adapter_new((GtkSourceBuffer *)gtk_text_view_get_buffer(view), checker_default());
  if (!adapter) return NULL;
  gtk_text_view_set_extra_menu(view, get_menu(adapter));
  gtk_widget_insert_action_group(GTK_WIDGET(view), "spelling", G_ACTION_GROUP(adapter));
  set_enabled(adapter, enabled);
  return adapter;
}

static void bridge_set_spelling(SpellingTextBufferAdapter *adapter, gboolean enabled) {
  if (!adapter || !spelling_lib) return;
  adapter_enable_fn set_enabled = (adapter_enable_fn)sym(spelling_lib, "spelling_text_buffer_adapter_set_enabled");
  if (set_enabled) set_enabled(adapter, enabled);
}

static void bridge_unref(void *object) {
  if (object) g_object_unref(object);
}

static GtkTextView *bridge_text_view(uintptr_t pointer) {
  return (GtkTextView *)pointer;
}
*/
import "C"

import (
	"fmt"
	"runtime"
	"unsafe"

	"github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// View is a GtkSourceView exposed through its GtkTextView base wrapper.
type View struct {
	Widget       *gtk.TextView
	Buffer       *gtk.TextBuffer
	spellAdapter unsafe.Pointer
}

// New constructs a native GtkSourceView 5 editor.
func New() (*View, error) {
	pointer := C.bridge_source_view_new()
	if pointer == nil {
		return nil, fmt.Errorf("initialize GtkSourceView 5: %s", C.GoString(C.bridge_last_error()))
	}
	object := glib.Take(unsafe.Pointer(pointer))
	textView, ok := object.CastType(gtk.GTypeTextView).(*gtk.TextView)
	if !ok {
		return nil, fmt.Errorf("wrap GtkSourceView as GtkTextView")
	}
	view := &View{Widget: textView, Buffer: textView.Buffer()}
	runtime.SetFinalizer(view, func(v *View) { v.Close() })
	return view, nil
}

// SetLanguage guesses syntax highlighting from the filename and content type.
func (v *View) SetLanguage(filename, contentType string) bool {
	cFilename := C.CString(filename)
	defer C.free(unsafe.Pointer(cFilename))
	var cContentType *C.char
	if contentType != "" {
		cContentType = C.CString(contentType)
		defer C.free(unsafe.Pointer(cContentType))
	}
	ok := C.bridge_guess_language(v.native(), cFilename, cContentType) != 0
	runtime.KeepAlive(v)
	return ok
}

// SetOptions applies source-editor presentation and indentation settings.
func (v *View) SetOptions(lineNumbers, currentLine, spaces bool, tabWidth, indentWidth int) {
	C.bridge_set_options(v.native(), boolValue(lineNumbers), boolValue(currentLine), boolValue(spaces), C.guint(tabWidth), C.gint(indentWidth))
	runtime.KeepAlive(v)
}

// SetHighlighting enables or disables syntax highlighting.
func (v *View) SetHighlighting(enabled bool) {
	C.bridge_set_highlight(v.native(), boolValue(enabled))
	runtime.KeepAlive(v)
}

// EnableSpelling attaches libspelling when available and toggles it.
func (v *View) EnableSpelling(enabled bool) bool {
	if v.spellAdapter == nil {
		v.spellAdapter = unsafe.Pointer(C.bridge_attach_spelling(v.native(), boolValue(enabled)))
		return v.spellAdapter != nil
	}
	C.bridge_set_spelling((*C.SpellingTextBufferAdapter)(v.spellAdapter), boolValue(enabled))
	return true
}

// Close releases native helper objects owned by this wrapper.
func (v *View) Close() {
	if v.spellAdapter != nil {
		C.bridge_unref(v.spellAdapter)
		v.spellAdapter = nil
	}
	runtime.SetFinalizer(v, nil)
}

func (v *View) native() *C.GtkTextView {
	return C.bridge_text_view(C.uintptr_t(glib.BaseObject(v.Widget).Native()))
}

func boolValue(value bool) C.gboolean {
	if value {
		return 1
	}
	return 0
}
