#!/bin/sh
# Downloads printpipe for this computer into a temp folder, checks it against
# SHA256SUMS, runs its install with the arguments given here and deletes it.
# Placing the binary, PATH and everything else are up to that install.
#
#   curl -fsSL https://github.com/alexgred/printpipe/releases/latest/download/install.sh | sh -s -- --origin <url>
#
# Nothing runs before the last line, so a download cut short does nothing.

releases=https://github.com/alexgred/printpipe/releases/latest/download

# fail says what went wrong, and on the lines under it what to do, and exits.
fail() {
	printf 'error: %s\n' "$1" >&2
	shift
	for hint in "$@"; do
		printf '  %s\n' "$hint" >&2
	done
	exit 1
}

# asset_of_this_computer sets asset to the name of the build to download.
asset_of_this_computer() {
	os=$(uname -s)
	arch=$(uname -m)
	case "$os $arch" in
	"Linux x86_64") asset=printpipe-linux-amd64 ;;
	"Darwin x86_64") asset=printpipe-darwin-amd64 ;;
	"Darwin arm64") asset=printpipe-darwin-arm64 ;;
	*) fail "printpipe has no build for $os $arch" "There are builds for Windows and Linux on x86-64, and for macOS." ;;
	esac
}

download() {
	curl -fsL --proto =https -o "$dir/$1" "$releases/$1"
	code=$?
	case $code in
	0) ;;
	# curl's codes for no name lookup, no connection, a timeout, TLS and a lost connection.
	5 | 6 | 7 | 28 | 35 | 52 | 55 | 56) fail "could not reach GitHub" "Check the internet connection and run the same command again." ;;
	*) fail "could not download $1 from GitHub" "Run the same command again." ;;
	esac
}

# sha256 prints a file's hash: Linux has sha256sum, macOS shasum.
sha256() {
	if command -v sha256sum >/dev/null; then
		sha256sum "$1" | awk '{ print $1 }'
	else
		shasum -a 256 "$1" | awk '{ print $1 }'
	fi
}

# verify refuses the build unless its line in SHA256SUMS has its hash. The line
# is a hash, spaces and the name, with a * before it when made in binary mode.
verify() {
	want=$(awk -v name="$1" '$2 == name || $2 == "*" name { print $1; exit }' "$dir/SHA256SUMS")
	[ -n "$want" ] || fail "SHA256SUMS does not list $1" "Nothing was installed."
	[ "$(sha256 "$dir/$1")" = "$want" ] ||
		fail "the download does not match SHA256SUMS: it is damaged or was tampered with" "Nothing was installed."
}

main() {
	asset_of_this_computer
	dir=$(mktemp -d 2>/dev/null) ||
		fail "could not make a temporary folder" "Check that the temp folder is writable and not full, then run the same command again."
	trap 'rm -rf "$dir"' EXIT
	trap 'exit 129' HUP
	trap 'exit 130' INT
	trap 'exit 143' TERM

	download "$asset"
	download SHA256SUMS
	verify "$asset"
	chmod +x "$dir/$asset"
	"$dir/$asset" install "$@"
	code=$?
	[ "$code" -ne 126 ] ||
		fail "could not run the download in the temp folder" "It may be mounted noexec: set TMPDIR to another folder and run the same command again."
	exit "$code"
}

main "$@"
