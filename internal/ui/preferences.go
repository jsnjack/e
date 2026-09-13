package ui

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/jsnjack/e/internal/activity"
	"github.com/jsnjack/e/internal/config"
	"github.com/jsnjack/e/internal/dispatch"
)

type modelPreference struct {
	row      *adw.ExpanderRow
	name     *adw.EntryRow
	endpoint *adw.EntryRow
	key      *adw.PasswordEntryRow
	http     *adw.SwitchRow
	up       *gtk.Button
	protocol string
}

func (w *window) buildPreferences() *adw.PreferencesDialog {
	dialog := adw.NewPreferencesDialog()
	dialog.SetTitle("E Preferences")
	dialog.SetContentWidth(680)
	dialog.SetContentHeight(680)
	page := adw.NewPreferencesPage()
	page.SetTitle("Editor")
	page.SetIconName("document-edit-symbolic")

	editorGroup := adw.NewPreferencesGroup()
	editorGroup.SetTitle("Editing")
	lineNumbers := preferenceSwitch("Line numbers", "Show line numbers beside the document.", w.options.Config.Editor.LineNumbers)
	wordWrap := preferenceSwitch("Wrap long lines", "Keep long text inside the window.", w.options.Config.Editor.WordWrap)
	spell := preferenceSwitch("Spell checking", "Use GNOME libspelling when it is installed.", w.options.Config.Editor.SpellCheck)
	spaces := preferenceSwitch("Insert spaces", "Use spaces instead of a tab character.", w.options.Config.Editor.InsertSpaces)
	tabWidth := adw.NewEntryRow()
	tabWidth.SetTitle("Tab width")
	tabWidth.SetText(strconv.Itoa(w.options.Config.Editor.TabWidth))
	editorGroup.Add(lineNumbers)
	editorGroup.Add(wordWrap)
	editorGroup.Add(spell)
	editorGroup.Add(spaces)
	editorGroup.Add(tabWidth)
	page.Add(editorGroup)

	aiGroup := adw.NewPreferencesGroup()
	aiGroup.SetTitle("AI Autocomplete")
	aiGroup.SetDescription("Models are tried in this order. If one is offline, E automatically uses the next available model.")
	aiEnabled := preferenceSwitch("Automatic suggestions", "Use this model chain for inline completions while typing.", w.options.Config.AI.Enabled)
	aiGroup.Add(aiEnabled)

	var rows []*modelPreference
	var inGroup []*adw.ExpanderRow
	relayout := func() {
		for _, row := range inGroup {
			aiGroup.Remove(row)
		}
		inGroup = inGroup[:0]
		for i, item := range rows {
			aiGroup.Add(item.row)
			inGroup = append(inGroup, item.row)
			item.up.SetVisible(i > 0)
		}
	}
	updateTitle := func(item *modelPreference) {
		title := strings.TrimSpace(item.name.Text())
		if title == "" {
			title = "New model"
		}
		item.row.SetTitle(title)
		item.row.SetSubtitle(strings.TrimSpace(item.endpoint.Text()))
	}
	addModel := func(model config.Model, expand bool) {
		item := &modelPreference{row: adw.NewExpanderRow(), protocol: model.Protocol}
		item.row.SetUseMarkup(false)
		item.name = adw.NewEntryRow()
		item.name.SetTitle("Model")
		item.name.SetText(model.Name)
		item.endpoint = adw.NewEntryRow()
		item.endpoint.SetTitle("Endpoint")
		item.endpoint.SetText(model.Endpoint)
		item.key = adw.NewPasswordEntryRow()
		item.key.SetTitle("API key (stored in the system keyring)")
		item.http = preferenceSwitch("Allow insecure HTTP", "Only for a trusted local endpoint.", model.AllowInsecureHTTP)
		item.row.AddRow(item.name)
		item.row.AddRow(item.endpoint)
		item.row.AddRow(item.key)
		item.row.AddRow(item.http)
		item.name.Connect("changed", func() { updateTitle(item) })
		item.endpoint.Connect("changed", func() { updateTitle(item) })

		if w.options.TestModel != nil {
			test := gtk.NewButtonWithLabel("Test")
			test.SetVAlign(gtk.AlignCenter)
			test.SetTooltipText("Check this model")
			test.ConnectClicked(func() {
				entry := modelFromPreference(item)
				label := entry.Name
				if label == "" {
					label = "model"
				}
				operation := w.activityHub.Begin(activity.AI, "Testing "+label)
				key := item.key.Text()
				test.SetSensitive(false)
				test.SetLabel("Testing…")
				test.RemoveCSSClass("success")
				test.RemoveCSSClass("error")
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					err := w.options.TestModel(ctx, entry, key)
					dispatch.Main(func() {
						test.SetSensitive(true)
						if err != nil {
							operation.Fail(err)
							test.SetLabel("Failed")
							test.SetTooltipText(err.Error())
							test.AddCSSClass("error")
						} else {
							operation.Finish("available", entry.Name, false)
							test.SetLabel("Connected ✓")
							test.SetTooltipText("Model is available")
							test.AddCSSClass("success")
						}
					})
				}()
			})
			item.row.AddSuffix(test)
		}

		item.up = gtk.NewButtonFromIconName("go-up-symbolic")
		item.up.SetTooltipText("Try earlier (higher priority)")
		item.up.AddCSSClass("flat")
		item.up.SetVAlign(gtk.AlignCenter)
		item.up.ConnectClicked(func() {
			for i, candidate := range rows {
				if candidate == item && i > 0 {
					rows[i-1], rows[i] = rows[i], rows[i-1]
					break
				}
			}
			relayout()
		})
		remove := gtk.NewButtonFromIconName("user-trash-symbolic")
		remove.SetTooltipText("Remove model")
		remove.AddCSSClass("flat")
		remove.SetVAlign(gtk.AlignCenter)
		remove.ConnectClicked(func() {
			for i, candidate := range rows {
				if candidate == item {
					rows = append(rows[:i], rows[i+1:]...)
					break
				}
			}
			relayout()
		})
		item.row.AddSuffix(item.up)
		item.row.AddSuffix(remove)
		rows = append(rows, item)
		updateTitle(item)
		relayout()
		item.row.SetExpanded(expand)
	}
	for _, model := range w.options.Config.AI.Chain {
		addModel(model, false)
	}
	header := gtk.NewBox(gtk.OrientationHorizontal, 6)
	add := gtk.NewButtonWithLabel("Add model")
	add.SetVAlign(gtk.AlignCenter)
	add.ConnectClicked(func() {
		model := config.Model{Provider: "openai", Protocol: "chat"}
		if len(rows) > 0 {
			last := rows[len(rows)-1]
			model.Endpoint = strings.TrimSpace(last.endpoint.Text())
			model.AllowInsecureHTTP = last.http.Active()
		}
		addModel(model, true)
	})
	header.Append(add)
	aiGroup.SetHeaderSuffix(header)
	page.Add(aiGroup)
	dialog.Add(page)

	dialog.ConnectClosed(func() {
		cfg := w.options.Config
		cfg.Editor.LineNumbers = lineNumbers.Active()
		cfg.Editor.WordWrap = wordWrap.Active()
		cfg.Editor.SpellCheck = spell.Active()
		cfg.Editor.InsertSpaces = spaces.Active()
		if width, err := strconv.Atoi(strings.TrimSpace(tabWidth.Text())); err == nil && width >= 1 && width <= 16 {
			cfg.Editor.TabWidth = width
		}
		cfg.AI.Enabled = aiEnabled.Active()
		cfg.AI.Chain = nil
		keys := make(map[string]string)
		for _, item := range rows {
			model := modelFromPreference(item)
			if model.Name == "" || model.Endpoint == "" {
				continue
			}
			cfg.AI.Chain = append(cfg.AI.Chain, model)
			if key := item.key.Text(); key != "" {
				keys[model.Endpoint] = key
			}
		}
		// Let AdwDialog finish removing its modal host before changing widgets in
		// the parent window. Re-entering the editor tree from the closed signal
		// could leave the modal shade visible on some compositors.
		w.preferencesApplying = true
		dispatch.Main(func() {
			defer func() { w.preferencesApplying = false }()
			if err := config.Save(w.options.ConfigPath, cfg); err != nil {
				slog.Warn("save preferences", "err", err)
				w.toast("Couldn’t save preferences")
				return
			}
			w.options.Config = cfg
			for _, doc := range w.documents {
				doc.configureRuntimePreferences()
			}
			if w.options.RebuildCompleter == nil {
				w.toast("Preferences saved")
				return
			}
			go func() {
				completer, err := w.options.RebuildCompleter(cfg, keys)
				dispatch.Main(func() {
					if err != nil {
						slog.Warn("apply AI preferences", "err", err)
						w.toast("Saved, but couldn’t apply AI settings")
						return
					}
					w.setCompleter(completer)
					w.toast("Preferences saved and applied")
				})
			}()
		})
	})
	return dialog
}

func modelFromPreference(item *modelPreference) config.Model {
	protocol := item.protocol
	if protocol == "" {
		protocol = "chat"
	}
	return config.Model{
		Name: strings.TrimSpace(item.name.Text()), Provider: "openai",
		Endpoint: strings.TrimSpace(item.endpoint.Text()), Protocol: protocol,
		AllowInsecureHTTP: item.http.Active(),
	}
}

func preferenceSwitch(title, subtitle string, active bool) *adw.SwitchRow {
	row := adw.NewSwitchRow()
	row.SetTitle(title)
	row.SetSubtitle(subtitle)
	row.SetActive(active)
	return row
}

func (d *editorDocument) configureRuntimePreferences() {
	cfg := d.window.options.Config.Editor
	d.view.SetOptions(cfg.LineNumbers, true, cfg.InsertSpaces, cfg.TabWidth, -1)
	d.view.Widget.SetWrapMode(gtk.WrapNone)
	if cfg.WordWrap {
		d.view.Widget.SetWrapMode(gtk.WrapWordChar)
	}
	d.view.EnableSpelling(cfg.SpellCheck)
	d.applyEditorConfig()
}
