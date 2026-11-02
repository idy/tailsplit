#!/bin/sh
# Public release installer. Run with sh; no repository checkout is required.
set -eu
LC_ALL=C
export LC_ALL
umask 077

project=tailsplit
repository="https://github.com/idy/$project"
version=latest
prefix=/usr/local
prefix_set=0
no_sudo=0
method=auto
cli=0
app_dir=
app_dir_set=0
install_root=0
app_stage=
app_target=
app_installed=0

say() { printf '%s\n' "$*"; }
die() { printf '%s: %s\n' "$project" "$*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || die "Required command not found: $1"; }
usage() {
    printf '%s\n' \
        'Install TailSplit from a public GitHub release, verified with SHA256SUMS.' \
        'Usage: sh install.sh [options]' \
        '  --version VERSION  vX.Y.Z or X.Y.Z (default: latest; prereleases accepted)' \
        '  --prefix DIR       CLI prefix (default: /usr/local); selects binary install' \
        '  --no-sudo          Never use sudo or packages; CLI default: $HOME/.local' \
        '  --method METHOD    auto, deb, or binary (default: auto; binary selects CLI)' \
        '  --cli              Install the CLI instead of the default macOS app' \
        '  --app-dir DIR      macOS app directory (default: $HOME/Applications)' \
        '  --help             Show this help' \
        '' \
        'Linux auto uses apt-get (or dpkg) when no prefix or --no-sudo is supplied.' \
        'Quit the installed TailSplit app before updating it. External state is retained.' \
        'With curl piping, pass options using: sh -s -- --no-sudo'
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --version|--prefix|--method|--app-dir)
            [ "$#" -ge 2 ] || die "Missing value for $1"
            [ -n "$2" ] || die "Empty value for $1"
            case "$1" in
                --version) version=$2 ;;
                --prefix) prefix=$2; prefix_set=1 ;;
                --method) method=$2 ;;
                --app-dir) app_dir=$2; app_dir_set=1 ;;
            esac
            shift 2 ;;
        --cli) cli=1; shift ;;
        --no-sudo) no_sudo=1; shift ;;
        --help|-h) usage; exit 0 ;;
        *) die "Unknown option: $1 (use --help)" ;;
    esac
done
case "$method" in auto|deb|binary) ;; *) die "Invalid method: $method" ;; esac

validate_version() {
    # An allowlist keeps tags safe as both URL components and archive filenames.
    case "$1" in ''|*[!A-Za-z0-9.+-]*) return 1 ;; esac
    [ "${#1}" -le 128 ] && printf '%s\n' "$1" |
        grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$'
}
if [ "$version" != latest ]; then
    version=${version#v}
    validate_version "$version" || die 'Invalid version; expected vX.Y.Z or X.Y.Z, optionally with prerelease/build identifiers.'
fi

os=$(uname -s) || die 'Cannot detect operating system.'
case "$os" in
    Linux) goos=linux ;;
    Darwin) goos=darwin ;;
    *) die "Unsupported operating system: $os (Linux and macOS are supported)." ;;
esac
machine=$(uname -m) || die 'Cannot detect architecture.'
case "$machine" in
    x86_64|amd64) arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    *) die "Unsupported architecture: $machine (amd64 and arm64 are supported)." ;;
esac

if [ "$goos" = darwin ] && [ "$method" = auto ] && [ "$cli" -eq 0 ] && [ "$prefix_set" -eq 0 ]; then
    method=app
    if [ "$app_dir_set" -eq 0 ]; then
        [ -n "${HOME:-}" ] || die 'HOME is required for the app destination; supply --app-dir DIR.'
        app_dir="$HOME/Applications"
    fi
    case "$app_dir" in /*) ;; *) app_dir="$(pwd -P)/$app_dir" ;; esac
    while [ "$app_dir" != / ] && [ "${app_dir%/}" != "$app_dir" ]; do app_dir=${app_dir%/}; done
elif [ "$app_dir_set" -eq 1 ]; then
    die '--app-dir applies only to the macOS GUI; omit --cli, --prefix and --method binary.'
elif [ "$method" = deb ]; then
    [ "$goos" = linux ] || die '--method deb is supported only on Linux.'
    [ "$no_sudo" -eq 0 ] || die '--no-sudo cannot install packages; use --method binary.'
    [ "$prefix_set" -eq 0 ] || die '--prefix selects a binary install; use --method binary.'
elif [ "$method" = auto ]; then
    method=binary
    if [ "$goos" = linux ] && [ "$prefix_set" -eq 0 ] && [ "$no_sudo" -eq 0 ]; then
        if command -v apt-get >/dev/null 2>&1 || command -v dpkg >/dev/null 2>&1; then
            method=deb
        fi
    fi
fi
if [ "$no_sudo" -eq 1 ] && [ "$prefix_set" -eq 0 ] && [ "$method" != app ]; then
    [ -n "${HOME:-}" ] || die 'HOME is required for --no-sudo; supply --prefix DIR.'
    prefix="$HOME/.local"
fi
case "$prefix" in /*) ;; *) prefix="$(pwd -P)/$prefix" ;; esac
while [ "$prefix" != / ] && [ "${prefix%/}" != "$prefix" ]; do prefix=${prefix%/}; done

if [ "$method" = deb ]; then
    if command -v apt-get >/dev/null 2>&1; then package_tool=apt-get
    elif command -v dpkg >/dev/null 2>&1; then package_tool=dpkg
    else die 'A Debian install requires apt-get or dpkg; use --method binary.'
    fi
elif [ "$method" = app ]; then
    need unzip
    need plutil
    need ps
    need find
    need install
else
    need tar
    need install
fi
need curl
need awk
if command -v sha256sum >/dev/null 2>&1; then hash_tool=sha256sum
elif command -v shasum >/dev/null 2>&1; then hash_tool=shasum
else die 'SHA256 verification requires sha256sum or shasum.'
fi

workdir=$(mktemp -d "${TMPDIR:-/tmp}/$project-install.XXXXXXXX") || die 'Cannot create a temporary directory.'
cleanup() {
    result=$?
    trap - 0 HUP INT TERM
    if [ -n "$app_stage" ]; then
        if [ -d "$app_stage/previous.app" ] && [ "$app_installed" -eq 0 ]; then
            if [ ! -e "$app_target" ] && [ ! -L "$app_target" ] &&
                run_install mv "$app_stage/previous.app" "$app_target"; then :
            else
                printf '%s\n' "App update interrupted; previous bundle retained at $app_stage/previous.app" >&2
                rm -rf "$workdir"
                exit 1
            fi
        fi
        run_install rm -rf "$app_stage" || result=1
    fi
    rm -rf "$workdir"
    exit "$result"
}
trap cleanup 0
trap 'exit 1' HUP INT TERM

fetch() {
    # -q disables curlrc credentials/options; all initial and redirect URLs use HTTPS.
    curl -q --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
        --output "$2" "$1" || die "Download failed: $1"
}
if [ "$version" = latest ]; then
    resolved=$(curl -q --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
        --output /dev/null --write-out '%{url_effective}' "$repository/releases/latest") || die 'Cannot resolve the latest release.'
    case "$resolved" in
        "$repository/releases/tag/v"*) version=${resolved#"$repository/releases/tag/v"} ;;
        *) die "Unexpected latest release redirect: $resolved" ;;
    esac
    validate_version "$version" || die 'The latest release redirect does not contain a valid version tag.'
fi
tag="v$version"
case "$method" in
    deb) asset="${project}_${version}_linux_${arch}.deb" ;;
    app) asset="TailSplit_${version}_darwin_${arch}.zip" ;;
    *) asset="${project}_${version}_${goos}_${arch}.tar.gz" ;;
esac
case "$asset" in ''|*[!A-Za-z0-9._+-]*) die 'Unsafe release asset filename.' ;; esac
base="$repository/releases/download/$tag"
say "Downloading $project $tag ($goos/$arch, $method)..."
fetch "$base/$asset" "$workdir/$asset"
fetch "$base/SHA256SUMS" "$workdir/SHA256SUMS"
expected=$(awk -v name="$asset" '
    $2 == name || $2 == "*" name {
        count++
        if (NF != 2 || length($1) != 64 || $1 ~ /[^0-9a-fA-F]/) bad = 1
        hash = tolower($1)
    }
    END { if (count != 1 || bad) exit 1; print hash }
' "$workdir/SHA256SUMS") || die "SHA256SUMS must contain exactly one valid SHA256 for $asset."
if [ "$hash_tool" = sha256sum ]; then actual=$(sha256sum < "$workdir/$asset")
else actual=$(shasum -a 256 < "$workdir/$asset")
fi
actual=${actual%% *}
[ "$actual" = "$expected" ] || die "SHA256 mismatch for $asset; nothing was installed."

privileged() {
    if [ "$(id -u)" = 0 ]; then "$@"
    elif [ "$no_sudo" -eq 1 ]; then
        die 'Destination is not writable. Use --no-sudo --prefix "$HOME/.local", or --app-dir "$HOME/Applications" for the macOS app.'
    elif command -v sudo >/dev/null 2>&1; then sudo "$@"
    else die 'sudo is required for this destination. Use --no-sudo --prefix "$HOME/.local", or --app-dir "$HOME/Applications" for the macOS app.'
    fi
}
run_install() {
    if [ "$install_root" -eq 1 ]; then privileged "$@"; else "$@"; fi
}

if [ "$method" = deb ]; then
    # Package maintainer scripts own upgrades; no service/config/device commands run here.
    if [ "$package_tool" = apt-get ]; then
        privileged apt-get install -y --no-install-recommends "$workdir/$asset"
    else
        privileged dpkg -i "$workdir/$asset"
    fi
    say "Installed $project $tag using $package_tool."
    exit 0
fi

if [ "$method" = app ]; then
    # Inspect names AND link types before either ZIP extractor can write any entries.
    unzip -Z1 "$workdir/$asset" > "$workdir/entries" || die 'Cannot list the app archive.'
    awk '
        $0 !~ /^TailSplit[.]app\// && $0 !~ /^__MACOSX\// { bad = 1 }
        /[[:cntrl:]\\]/ || /\/\// { bad = 1 }
        {
            n = split($0, parts, "/")
            for (i = 1; i <= n; i++) if (parts[i] == "." || parts[i] == "..") bad = 1
            if (++seen[$0] != 1) bad = 1
        }
        END { if (bad || !seen["TailSplit.app/Contents/Info.plist"]) exit 1 }
    ' "$workdir/entries" || die 'Unsafe or unexpected app archive entries.'
    entry_count=$(awk 'END { print NR }' "$workdir/entries")
    unzip -Z -l "$workdir/$asset" > "$workdir/types" || die 'Cannot inspect app archive types.'
    awk -v want="$entry_count" '
        length($1) == 10 && $1 ~ /^[-d?lcbps]/ {
            count++
            if ($1 !~ /^[-d?]/) bad = 1
        }
        END { if (bad || count != want) exit 1 }
    ' "$workdir/types" || die 'App archive must contain only regular files and directories (no links).'
    mkdir "$workdir/extracted"
    if command -v ditto >/dev/null 2>&1; then
        ditto -x -k "$workdir/$asset" "$workdir/extracted" || die 'Cannot extract the app archive.'
    else
        unzip -q "$workdir/$asset" -d "$workdir/extracted" || die 'Cannot extract the app archive.'
    fi
    find "$workdir/extracted" -type l -print > "$workdir/links"
    [ ! -s "$workdir/links" ] || die 'Extracted app contains a symbolic link.'
    app="$workdir/extracted/TailSplit.app"
    [ -d "$app" ] && [ -f "$app/Contents/Info.plist" ] || die 'App archive has no TailSplit.app/Contents/Info.plist.'
    identity=$(plutil -extract CFBundleIdentifier raw -o - "$app/Contents/Info.plist") || die 'Cannot read the app bundle identity.'
    [ "$identity" = im.lbv.tailsplit ] || die "Unexpected app bundle identity: $identity"
    executable=$(plutil -extract CFBundleExecutable raw -o - "$app/Contents/Info.plist") || die 'Cannot read the app executable name.'
    case "$executable" in ''|.|..|*[!A-Za-z0-9._-]*) die 'Unsafe app executable name.' ;; esac
    [ -f "$app/Contents/MacOS/$executable" ] && [ -x "$app/Contents/MacOS/$executable" ] || die 'The app executable is missing or not executable.'
    app_target="${app_dir%/}/TailSplit.app"
    if [ -e "$app_target" ] || [ -L "$app_target" ]; then
        [ -d "$app_target" ] && [ ! -L "$app_target" ] &&
            [ -f "$app_target/Contents/Info.plist" ] && [ ! -L "$app_target/Contents/Info.plist" ] ||
            die "Refusing to overwrite a non-app or symlink: $app_target"
        identity=$(plutil -extract CFBundleIdentifier raw -o - "$app_target/Contents/Info.plist") || die 'Cannot read the installed app identity; refusing to overwrite it.'
        [ "$identity" = im.lbv.tailsplit ] || die "Refusing to overwrite a different app bundle identity: $identity"
        ps -axo command= > "$workdir/processes" || die 'Cannot check running apps; quit TailSplit before updating.'
        if awk -v prefix="$app_target/Contents/MacOS/" 'index($0, prefix) == 1 { found = 1 } END { exit !found }' "$workdir/processes"; then
            die 'TailSplit is running at the destination; quit it before updating. No processes were stopped.'
        fi
    fi
    parent=$app_dir
    while [ ! -e "$parent" ]; do parent=${parent%/*}; [ -n "$parent" ] || parent=/; done
    [ -d "$parent" ] || die "Not a directory: $parent"
    if [ ! -w "$parent" ]; then install_root=1; fi
    if [ "$install_root" -eq 1 ] && [ "$no_sudo" -eq 1 ]; then
        die 'App directory is not writable; use --app-dir "$HOME/Applications".'
    fi
    [ -d "$app_dir" ] || run_install install -d -m 0755 "$app_dir"
    # Stage on the destination filesystem. A whole-bundle rename avoids stale files.
    app_stage=$(run_install mktemp -d "$app_dir/.tailsplit-install.XXXXXXXX") || die 'Cannot stage the app in its destination directory.'
    # A sudo-created staging directory must remain inspectable for rollback checks.
    if [ "$install_root" -eq 1 ]; then run_install chmod 0755 "$app_stage"; fi
    if command -v ditto >/dev/null 2>&1; then
        run_install ditto "$app" "$app_stage/TailSplit.app" || die 'Cannot stage TailSplit.app.'
    else
        run_install cp -Rp "$app" "$app_stage/TailSplit.app" || die 'Cannot stage TailSplit.app.'
    fi
    if [ -e "$app_target" ]; then
        run_install mv "$app_target" "$app_stage/previous.app" || die 'Cannot move the old app bundle aside.'
    fi
    run_install mv "$app_stage/TailSplit.app" "$app_target" || die 'Cannot activate the new app; restoring the previous bundle.'
    app_installed=1
    say "Installed TailSplit $tag at $app_target."
    say 'Signing and notarization depend on the selected release; this installer verifies SHA256, not notarization. macOS may require Gatekeeper approval.'
    exit 0
fi

# Validate the entire archive before extraction, including duplicate names and types.
tar -tzf "$workdir/$asset" > "$workdir/entries" || die 'Cannot list the CLI archive.'
awk -v name="$project" '
    $0 != name && $0 != "LICENSE" && $0 != "THIRD_PARTY_NOTICES.md" && $0 != "build.json" { bad = 1 }
    { if (++seen[$0] != 1) bad = 1 }
    END { if (bad || NR != 4 || !seen[name] || !seen["LICENSE"] || !seen["THIRD_PARTY_NOTICES.md"] || !seen["build.json"]) exit 1 }
' "$workdir/entries" || die 'Unsafe or unexpected CLI archive entries.'
tar -tvzf "$workdir/$asset" > "$workdir/types" || die 'Cannot inspect CLI archive types.'
awk 'substr($0, 1, 1) != "-" { bad = 1 } END { if (bad || NR != 4) exit 1 }' "$workdir/types" ||
    die 'CLI archive must contain only regular files (no links).'
mkdir "$workdir/extracted"
tar -xzf "$workdir/$asset" -C "$workdir/extracted" "$project" || die 'Cannot extract the CLI executable.'
binary="$workdir/extracted/$project"
[ -f "$binary" ] && [ ! -L "$binary" ] || die 'The CLI executable is not a regular file.'
bin_dir="${prefix%/}/bin"
destination="$bin_dir/$project"
[ ! -L "$destination" ] && [ ! -d "$destination" ] || die "Refusing to overwrite a link or directory: $destination"
parent=$bin_dir
while [ ! -e "$parent" ]; do parent=${parent%/*}; [ -n "$parent" ] || parent=/; done
[ -d "$parent" ] || die "Not a directory: $parent"
if [ ! -w "$parent" ] || { [ -e "$destination" ] && [ ! -w "$destination" ]; }; then install_root=1; fi
if [ "$install_root" -eq 1 ] && [ "$no_sudo" -eq 1 ]; then
    die 'Destination is not writable. Use --no-sudo --prefix "$HOME/.local" (or another writable prefix).'
fi
[ -d "$bin_dir" ] || run_install install -d -m 0755 "$bin_dir"
run_install install -m 0755 "$binary" "$destination"
say "Installed $project $tag at $destination."
