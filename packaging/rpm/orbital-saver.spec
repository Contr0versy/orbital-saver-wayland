# Fedora / COPR spec. Builds from a release tag. The build fetches one Go
# module (github.com/jezek/xgb), so in COPR enable "internet access during
# builds", or vendor it into the source tarball (go mod vendor) first.
#
# Local build:
#   rpmbuild -ba packaging/rpm/orbital-saver.spec --define "_sourcedir $PWD/dist"

%global goipath github.com/codetheuri/orbital-saver
%global debug_package %{nil}

Name:           orbital-saver
Version:        1.1.0
Release:        1%{?dist}
Summary:        Retro wireframe solar system screensaver and live wallpaper
License:        MIT
URL:            https://%{goipath}
Source0:        %{url}/archive/v%{version}/%{name}-%{version}.tar.gz

BuildRequires:  golang >= 1.19
BuildRequires:  make
BuildRequires:  systemd-rpm-macros

%description
Polyhedral planets, a ringed world, moons and a comet follow elliptical
Keplerian orbits around a central star, drawn as depth-faded wireframes over
a parallax starfield. Runs as an X11 screensaver hack, or natively on Wayland
compositors with wlr-layer-shell (niri, sway, Hyprland) as a power-aware live
wallpaper or a screensaver. A single static binary.

%prep
%autosetup

%build
make build VERSION=%{version} GOFLAGS='-trimpath -mod=readonly'

%check
go test ./...

%install
make install PREFIX=%{_prefix} DESTDIR=%{buildroot}

%files
%license LICENSE
%doc README.md
%{_bindir}/%{name}
%{_prefix}/lib/xscreensaver/%{name}
%{_datadir}/applications/screensavers/%{name}.desktop
%{_datadir}/xscreensaver/config/%{name}.xml
%{_userunitdir}/%{name}.service
%{_datadir}/doc/%{name}/niri.kdl
