#!/bin/sh
# Installs jenktui on macOS or Linux without needing Go.
#
#   curl -fsSL https://raw.githubusercontent.com/dursunkoc/jenktui/main/install.sh | sh
#
# It downloads the archive for your system from the latest GitHub release,
# checks it against the release's checksums.txt, and installs the binary.
#
# Optional environment variables:
#   JENKTUI_VERSION   version to install, e.g. 0.1.0 (default: the latest release)
#   INSTALL_DIR       where to put the binary (default: /usr/local/bin if you can
#                     write to it, otherwise ~/.local/bin)
#   JENKTUI_BASE_URL  download location, for testing; default is the GitHub release

set -eu

REPO="dursunkoc/jenktui"

say() { printf '%s\n' "$*"; }
err() { printf 'error: %s\n' "$*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || err "'$1' is required but not installed"; }

detect_os() {
	case "$(uname -s)" in
	Darwin) echo darwin ;;
	Linux) echo linux ;;
	*) err "unsupported system '$(uname -s)'. On Windows, download the .zip from https://github.com/$REPO/releases" ;;
	esac
}

detect_arch() {
	case "$(uname -m)" in
	x86_64 | amd64) echo amd64 ;;
	arm64 | aarch64) echo arm64 ;;
	*) err "unsupported CPU '$(uname -m)'" ;;
	esac
}

# The latest release's tag is the last part of the URL that /releases/latest redirects to.
latest_version() {
	url=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest") ||
		err "could not reach GitHub to find the latest release"
	tag=${url##*/}
	case "$tag" in
	v[0-9]*) echo "${tag#v}" ;;
	*) err "no release found yet at https://github.com/$REPO/releases" ;;
	esac
}

sha256_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{print $1}'
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | awk '{print $1}'
	else
		err "need 'sha256sum' or 'shasum' to verify the download"
	fi
}

choose_dir() {
	if [ -n "${INSTALL_DIR:-}" ]; then
		echo "$INSTALL_DIR"
	elif [ -w /usr/local/bin ]; then
		echo /usr/local/bin
	else
		echo "$HOME/.local/bin"
	fi
}

# Everything runs inside main, called on the last line, so a download that is
# cut off halfway never executes a partial script.
main() {
	case "${1:-}" in
	-h | --help)
		sed -n '2,15p' "$0" 2>/dev/null | sed 's/^# \{0,1\}//' || true
		return 0
		;;
	esac

	need curl
	need tar
	need uname
	need mktemp
	need awk

	os=$(detect_os)
	arch=$(detect_arch)
	version=${JENKTUI_VERSION:-$(latest_version)}
	version=${version#v}

	archive="jenktui_${version}_${os}_${arch}.tar.gz"
	base=${JENKTUI_BASE_URL:-https://github.com/$REPO/releases/download/v$version}

	tmp=$(mktemp -d)
	trap 'rm -rf "$tmp"' EXIT

	say "Downloading jenktui $version for $os/$arch..."
	curl -fsSL "$base/$archive" -o "$tmp/$archive" || err "download failed: $base/$archive"
	curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt" || err "download failed: $base/checksums.txt"

	expected=$(awk -v f="$archive" '$2 == f { print $1 }' "$tmp/checksums.txt")
	[ -n "$expected" ] || err "$archive is not listed in checksums.txt"
	actual=$(sha256_of "$tmp/$archive")
	[ "$expected" = "$actual" ] || err "checksum mismatch for $archive; nothing was installed"

	tar -xzf "$tmp/$archive" -C "$tmp" jenktui || err "could not unpack $archive"

	dir=$(choose_dir)
	mkdir -p "$dir" 2>/dev/null || err "cannot create $dir. Set INSTALL_DIR to a folder you can write to"
	[ -w "$dir" ] || err "cannot write to $dir. Set INSTALL_DIR to a folder you can write to"
	install -m 0755 "$tmp/jenktui" "$dir/jenktui" || err "could not install to $dir"

	say "Installed jenktui $version to $dir/jenktui"
	case ":$PATH:" in
	*":$dir:"*) ;;
	*) say "Note: $dir is not on your PATH. Add it, for example: export PATH=\"$dir:\$PATH\"" ;;
	esac
	say "Run 'jenktui --version' to check, then 'jenktui' to start."
}

main "$@"
