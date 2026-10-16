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

typedef GType (*get_type_fn)(void);

// Ghost text is drawn by a GtkSourceView subclass in the ABOVE_TEXT layer, so
// GTK renders it as a text node in the same pass and coordinates as the
// document. It never changes the text layout.
typedef struct {
  PangoLayout *layout;
  int x, y;
  GdkRGBA color;
} GhostText;

// Secondary carets of multiple-cursor editing, as character offsets. They are
// drawn in the same layer, so they match the primary caret and scroll with it.
typedef struct {
  int count;
  int *offsets;
} CaretSet;

// Change marks against the file's base version, as runs of lines. They are
// drawn as bars in the left text margin, for visible lines only.
typedef struct {
  int line, count, kind;
} DiffMark;

typedef struct {
  int count;
  DiffMark *marks;
} DiffSet;

enum { DIFF_ADDED = 1, DIFF_MODIFIED = 2, DIFF_DELETED = 3 };

static GType ghost_view_type;
static GQuark ghost_quark;
static GQuark caret_quark;
static GQuark diff_quark;
static void (*parent_snapshot_layer)(GtkTextView *, GtkTextViewLayer, GtkSnapshot *);

static void ghost_free(gpointer data) {
  GhostText *ghost = data;
  g_clear_object(&ghost->layout);
  g_free(ghost);
}

static void caret_free(gpointer data) {
  CaretSet *carets = data;
  g_free(carets->offsets);
  g_free(carets);
}

static void snapshot_carets(GtkTextView *view, GtkSnapshot *snapshot) {
  CaretSet *carets = g_object_get_qdata(G_OBJECT(view), caret_quark);
  if (!carets || carets->count == 0) return;
  GtkTextBuffer *buffer = gtk_text_view_get_buffer(view);
  GdkRectangle visible;
  gtk_text_view_get_visible_rect(view, &visible);
  // Only lines on screen are laid out; skip carets outside them so a caret
  // far away never forces layout of its line.
  GtkTextIter top, bottom;
  gtk_text_view_get_iter_at_location(view, &top, visible.x, visible.y);
  gtk_text_view_get_iter_at_location(view, &bottom, visible.x, visible.y + visible.height);
  gtk_text_iter_set_line_offset(&top, 0);
  gtk_text_iter_forward_to_line_end(&bottom);
  int first = gtk_text_iter_get_offset(&top);
  int last = gtk_text_iter_get_offset(&bottom);
  GdkRGBA color;
  gtk_widget_get_color(GTK_WIDGET(view), &color);
  for (int i = 0; i < carets->count; i++) {
    int offset = carets->offsets[i];
    if (offset < first || offset > last) continue;
    GtkTextIter iter;
    gtk_text_buffer_get_iter_at_offset(buffer, &iter, offset);
    GdkRectangle strong;
    gtk_text_view_get_cursor_locations(view, &iter, &strong, NULL);
    gtk_snapshot_append_color(snapshot, &color, &GRAPHENE_RECT_INIT(strong.x, strong.y, 1, strong.height));
  }
}

static void diff_free(gpointer data) {
  DiffSet *set = data;
  g_free(set->marks);
  g_free(set);
}

static void snapshot_diff(GtkTextView *view, GtkSnapshot *snapshot) {
  DiffSet *set = g_object_get_qdata(G_OBJECT(view), diff_quark);
  if (!set || set->count == 0) return;
  GtkTextBuffer *buffer = gtk_text_view_get_buffer(view);
  GdkRectangle visible;
  gtk_text_view_get_visible_rect(view, &visible);
  GtkTextIter top, bottom;
  gtk_text_view_get_iter_at_location(view, &top, visible.x, visible.y);
  gtk_text_view_get_iter_at_location(view, &bottom, visible.x, visible.y + visible.height);
  int first = gtk_text_iter_get_line(&top);
  int last = gtk_text_iter_get_line(&bottom);
  int lines = gtk_text_buffer_get_line_count(buffer);
  static const GdkRGBA added = {0.18f, 0.76f, 0.49f, 0.95f};
  static const GdkRGBA modified = {0.21f, 0.52f, 0.89f, 0.95f};
  static const GdkRGBA deleted = {0.88f, 0.11f, 0.14f, 0.95f};
  for (int i = 0; i < set->count; i++) {
    DiffMark mark = set->marks[i];
    GtkTextIter iter;
    int y, height;
    if (mark.kind == DIFF_DELETED) {
      if (mark.line < first || mark.line > last + 1) continue;
      // The removed lines sat just above mark.line, or below the last line.
      gboolean end = mark.line >= lines;
      gtk_text_buffer_get_iter_at_line(buffer, &iter, end ? lines - 1 : mark.line);
      gtk_text_view_get_line_yrange(view, &iter, &y, &height);
      int edge = end ? y + height : y;
      gtk_snapshot_append_color(snapshot, &deleted, &GRAPHENE_RECT_INIT(visible.x, edge - 2, 7, 4));
      continue;
    }
    int start = MAX(mark.line, first);
    int stop = MIN(mark.line + mark.count - 1, MIN(last, lines - 1));
    if (start > stop) continue;
    gtk_text_buffer_get_iter_at_line(buffer, &iter, start);
    gtk_text_view_get_line_yrange(view, &iter, &y, &height);
    int bottom_y = y + height;
    if (stop != start) {
      int stop_y;
      gtk_text_buffer_get_iter_at_line(buffer, &iter, stop);
      gtk_text_view_get_line_yrange(view, &iter, &stop_y, &height);
      bottom_y = stop_y + height;
    }
    gtk_snapshot_append_color(snapshot, mark.kind == DIFF_ADDED ? &added : &modified,
                              &GRAPHENE_RECT_INIT(visible.x, y, 3, bottom_y - y));
  }
}

static void ghost_snapshot_layer(GtkTextView *view, GtkTextViewLayer layer, GtkSnapshot *snapshot) {
  if (parent_snapshot_layer) parent_snapshot_layer(view, layer, snapshot);
  if (layer != GTK_TEXT_VIEW_LAYER_ABOVE_TEXT) return;
  snapshot_diff(view, snapshot);
  snapshot_carets(view, snapshot);
  GhostText *ghost = g_object_get_qdata(G_OBJECT(view), ghost_quark);
  if (!ghost || !ghost->layout) return;
  gtk_snapshot_save(snapshot);
  gtk_snapshot_translate(snapshot, &GRAPHENE_POINT_INIT(ghost->x, ghost->y));
  gtk_snapshot_append_layout(snapshot, ghost->layout, &ghost->color);
  gtk_snapshot_restore(snapshot);
}

static void ghost_class_init(gpointer klass, gpointer data) {
  GtkTextViewClass *view_class = GTK_TEXT_VIEW_CLASS(klass);
  parent_snapshot_layer = view_class->snapshot_layer;
  view_class->snapshot_layer = ghost_snapshot_layer;
}

static GType ghost_view_get_type(void) {
  if (ghost_view_type) return ghost_view_type;
  get_type_fn parent_type = (get_type_fn)sym(source_lib, "gtk_source_view_get_type");
  if (!parent_type) return 0;
  GTypeQuery query;
  g_type_query(parent_type(), &query);
  if (!query.type) return 0;
  ghost_quark = g_quark_from_static_string("e-ghost-text");
  caret_quark = g_quark_from_static_string("e-secondary-carets");
  diff_quark = g_quark_from_static_string("e-diff-marks");
  ghost_view_type = g_type_register_static_simple(parent_type(), "EGhostSourceView",
                                                  query.class_size, ghost_class_init,
                                                  query.instance_size, NULL, 0);
  return ghost_view_type;
}

static GtkWidget *bridge_source_view_new(void) {
  if (!ensure_source()) return NULL;
  GType type = ghost_view_get_type();
  if (type) return g_object_new(type, NULL);
  source_view_new_fn make = (source_view_new_fn)sym(source_lib, "gtk_source_view_new");
  if (!make) return NULL;
  return make();
}

static gboolean bridge_set_ghost(GtkTextView *view, PangoLayout *layout, int x, int y,
                                 float red, float green, float blue, float alpha) {
  if (!ghost_view_type || !G_TYPE_CHECK_INSTANCE_TYPE(view, ghost_view_type)) return FALSE;
  GhostText *ghost = NULL;
  if (layout) {
    ghost = g_new0(GhostText, 1);
    ghost->layout = g_object_ref(layout);
    ghost->x = x;
    ghost->y = y;
    ghost->color = (GdkRGBA){red, green, blue, alpha};
  }
  // Replacing the qdata frees the previous layout.
  g_object_set_qdata_full(G_OBJECT(view), ghost_quark, ghost, ghost ? ghost_free : NULL);
  gtk_widget_queue_draw(GTK_WIDGET(view));
  return TRUE;
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
  if ((!filename || !*filename) && (!content_type || !*content_type)) {
    set_language((GtkSourceBuffer *)gtk_text_view_get_buffer(view), NULL);
    return FALSE;
  }
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

static gboolean bridge_set_carets(GtkTextView *view, const int *offsets, int count) {
  if (!ghost_view_type || !G_TYPE_CHECK_INSTANCE_TYPE(view, ghost_view_type)) return FALSE;
  CaretSet *carets = NULL;
  if (count > 0) {
    carets = g_new0(CaretSet, 1);
    carets->count = count;
    carets->offsets = g_memdup2(offsets, sizeof(int) * count);
  }
  g_object_set_qdata_full(G_OBJECT(view), caret_quark, carets, carets ? caret_free : NULL);
  gtk_widget_queue_draw(GTK_WIDGET(view));
  return TRUE;
}

static gboolean bridge_set_diff(GtkTextView *view, const int *values, int count) {
  if (!ghost_view_type || !G_TYPE_CHECK_INSTANCE_TYPE(view, ghost_view_type)) return FALSE;
  DiffSet *set = NULL;
  if (count > 0) {
    set = g_new0(DiffSet, 1);
    set->count = count;
    set->marks = g_new0(DiffMark, count);
    for (int i = 0; i < count; i++) {
      set->marks[i] = (DiffMark){values[3 * i], values[3 * i + 1], values[3 * i + 2]};
    }
  }
  g_object_set_qdata_full(G_OBJECT(view), diff_quark, set, set ? diff_free : NULL);
  gtk_widget_queue_draw(GTK_WIDGET(view));
  return TRUE;
}

static PangoLayout *bridge_layout(uintptr_t pointer) {
  return (PangoLayout *)pointer;
}
*/
import "C"

import (
	"fmt"
	"runtime"
	"unsafe"

	"github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
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

// SetGhost draws layout above the document text at buffer coordinates x, y.
// It reports false when the view cannot draw ghost text.
func (v *View) SetGhost(layout *pango.Layout, x, y int, color *gdk.RGBA) bool {
	ok := C.bridge_set_ghost(v.native(), C.bridge_layout(C.uintptr_t(glib.BaseObject(layout).Native())),
		C.int(x), C.int(y), C.float(color.Red()), C.float(color.Green()), C.float(color.Blue()), C.float(color.Alpha())) != 0
	runtime.KeepAlive(v)
	runtime.KeepAlive(layout)
	return ok
}

// ClearGhost removes ghost text drawn by SetGhost.
func (v *View) ClearGhost() {
	C.bridge_set_ghost(v.native(), nil, 0, 0, 0, 0, 0, 0)
	runtime.KeepAlive(v)
}

// SetCarets draws a secondary caret at each character offset. An empty slice
// removes them. It reports false when the view cannot draw carets.
func (v *View) SetCarets(offsets []int) bool {
	var pointer *C.int
	if len(offsets) > 0 {
		native := make([]C.int, len(offsets))
		for i, offset := range offsets {
			native[i] = C.int(offset)
		}
		pointer = &native[0]
		defer runtime.KeepAlive(native)
	}
	ok := C.bridge_set_carets(v.native(), pointer, C.int(len(offsets))) != 0
	runtime.KeepAlive(v)
	return ok
}

// DiffMark is one run of changed lines, in 0-based buffer lines. Kind is 1
// for added, 2 for modified, and 3 for lines deleted just above Line.
type DiffMark struct {
	Line, Count, Kind int
}

// SetDiffMarks draws change bars in the left text margin. An empty slice
// removes them. It reports false when the view cannot draw them.
func (v *View) SetDiffMarks(marks []DiffMark) bool {
	var pointer *C.int
	if len(marks) > 0 {
		native := make([]C.int, 0, 3*len(marks))
		for _, mark := range marks {
			native = append(native, C.int(mark.Line), C.int(mark.Count), C.int(mark.Kind))
		}
		pointer = &native[0]
		defer runtime.KeepAlive(native)
	}
	ok := C.bridge_set_diff(v.native(), pointer, C.int(len(marks))) != 0
	runtime.KeepAlive(v)
	return ok
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
