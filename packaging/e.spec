# Binary RPM for E. The Makefile builds the binary first and rpmbuild installs
# it from the source tree (srcdir define); rpmbuild does not compile Go here.
# Build with: make rpm
%global debug_package %{nil}
# Releases are tracked as git tags, not an RPM %changelog.
%global source_date_epoch_from_changelog 0

Name:           e
Version:        %{?appversion}%{!?appversion:0.0.0}
Release:        1%{?dist}
Summary:        A native GNOME text and code editor with AI autocomplete

License:        MIT
URL:            https://github.com/jsnjack/e

Requires:       gtk4
Requires:       libadwaita
Requires:       gtksourceview5
Recommends:     libspelling
Recommends:     webkitgtk6.0
Recommends:     git

%description
E is a native GTK4 and libadwaita editor with syntax highlighting, search and
replace, multiple cursors, session recovery, language-server completion,
EditorConfig support, a live Markdown preview, and inline completion from
ordered local or remote OpenAI-compatible models.

%install
install -Dm0755 %{srcdir}/bin/e %{buildroot}%{_bindir}/e
install -Dm0644 %{srcdir}/data/com.jsnjack.e.desktop %{buildroot}%{_datadir}/applications/com.jsnjack.e.desktop
install -Dm0644 %{srcdir}/data/com.jsnjack.e.metainfo.xml %{buildroot}%{_datadir}/metainfo/com.jsnjack.e.metainfo.xml
install -Dm0644 %{srcdir}/data/icons/scalable/apps/com.jsnjack.e.svg %{buildroot}%{_datadir}/icons/hicolor/scalable/apps/com.jsnjack.e.svg
install -Dm0644 %{srcdir}/LICENSE %{buildroot}%{_licensedir}/e/LICENSE

# Refresh the desktop and icon caches so the launcher entry and icon appear.
%post
update-desktop-database &>/dev/null || :
touch --no-create %{_datadir}/icons/hicolor &>/dev/null || :
gtk-update-icon-cache %{_datadir}/icons/hicolor &>/dev/null || :

%postun
update-desktop-database &>/dev/null || :
if [ $1 -eq 0 ]; then
    touch --no-create %{_datadir}/icons/hicolor &>/dev/null || :
    gtk-update-icon-cache %{_datadir}/icons/hicolor &>/dev/null || :
fi

%files
%license %{_licensedir}/e/LICENSE
%{_bindir}/e
%{_datadir}/applications/com.jsnjack.e.desktop
%{_datadir}/metainfo/com.jsnjack.e.metainfo.xml
%{_datadir}/icons/hicolor/scalable/apps/com.jsnjack.e.svg
