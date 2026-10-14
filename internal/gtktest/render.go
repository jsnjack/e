// Package gtktest renders live GTK widgets for pixel tests. gotk4 wraps
// GskRenderNode as a GObject, which it is not, so rendering goes through C.
package gtktest

/*
#cgo pkg-config: gtk4
#include <gtk/gtk.h>
#include <stdint.h>
#include <string.h>

static GBytes *render_widget(uintptr_t pointer, gsize *stride) {
  GtkWidget *widget = (GtkWidget *)pointer;
  GtkWidget *parent = gtk_widget_get_parent(widget);
  GtkNative *native = gtk_widget_get_native(widget);
  graphene_rect_t bounds;
  if (!parent || !native || !gtk_widget_compute_bounds(widget, parent, &bounds)) return NULL;
  GtkSnapshot *snapshot = gtk_snapshot_new();
  gtk_widget_snapshot_child(parent, widget, snapshot);
  GskRenderNode *node = gtk_snapshot_free_to_node(snapshot);
  if (!node) return NULL;
  GdkTexture *texture = gsk_renderer_render_texture(gtk_native_get_renderer(native), node, &bounds);
  gsk_render_node_unref(node);
  if (!texture) return NULL;
  GdkTextureDownloader *downloader = gdk_texture_downloader_new(texture);
  gdk_texture_downloader_set_format(downloader, GDK_MEMORY_R8G8B8A8_PREMULTIPLIED);
  GBytes *bytes = gdk_texture_downloader_download_bytes(downloader, stride);
  gdk_texture_downloader_free(downloader);
  g_object_unref(texture);
  return bytes;
}
*/
import "C"

import (
	"errors"
	"runtime"
	"unsafe"

	"github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// Render returns the widget's current pixels as premultiplied RGBA rows with
// the given stride in bytes. Pixel (0, 0) is the widget's top-left corner.
func Render(widget gtk.Widgetter) ([]byte, int, error) {
	base := gtk.BaseWidget(widget)
	var stride C.gsize
	bytes := C.render_widget(C.uintptr_t(glib.BaseObject(base).Native()), &stride)
	runtime.KeepAlive(base)
	if bytes == nil {
		return nil, 0, errors.New("widget has no parent, native surface, or render node")
	}
	defer C.g_bytes_unref(bytes)
	var size C.gsize
	data := C.g_bytes_get_data(bytes, &size)
	return C.GoBytes(unsafe.Pointer(data), C.int(size)), int(stride), nil
}
