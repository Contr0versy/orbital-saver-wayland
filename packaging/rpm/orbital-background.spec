# Fedora / COPR spec. Builds from a release tag. The build needs no Go
# modules beyond the standard library.
#
# Local build:
#   rpmbuild -ba packaging/rpm/orbital-background.spec --define "_sourcedir $PWD/dist"

%global goipath github.com/Contr0versy/orbital-background
%global debug_package %{nil}

Name:           orbital-background
Version:        1.1.0
Release:        1%{?dist}
Summary:        Retro wireframe solar system live wallpaper for Wayland
License:        MIT
URL:            https://%{goipath}
Source0:        %{url}/archive/v%{version}/%{name}-%{version}.tar.gz

BuildRequires:  golang >= 1.19
BuildRequires:  make
BuildRequires:  systemd-rpm-macros

%description
Polyhedral planets, a ringed world, moons and a comet follow elliptical
Keplerian orbits around a central star, drawn as depth-faded wireframes over
a parallax starfield. Runs natively on Wayland compositors with
wlr-layer-shell (niri, sway, Hyprland) as a power-aware live wallpaper. A
single static binary.

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
%{_userunitdir}/%{name}.service
%{_datadir}/doc/%{name}/niri.kdl
