# TailSplit

Choose which traffic goes through your Tailscale exit node and which stays direct.
TailSplit uses sing-box routing rules, supports switching between Tailscale
networks, and lets you configure routing by domain and IP.

[Website](https://tailsplit.idy.dev/) · [Download](https://github.com/idy/tailsplit/releases/latest) · [Guide](docs/guide.md) · [Discord](https://discord.gg/RY7ktfh8Pr)

## Install

| Platform | Available packages |
| --- | --- |
| macOS 14+, Apple Silicon or Intel | Desktop app (`.dmg` / `.zip`) and CLI |
| Windows 10+ x64 or Windows 11 ARM64 | Desktop Setup EXE and CLI ZIP |
| Linux amd64 or arm64 | CLI archive and `.deb` |

Get desktop installers from [GitHub Releases](https://github.com/idy/tailsplit/releases/latest).
macOS and Linux also support the installer below, which selects your platform
and verifies the release's SHA-256 checksums:

```sh
curl -fsSL https://tailsplit.idy.dev/install.sh | sh
```

macOS users can also use the [Homebrew tap](https://github.com/idy/homebrew#install).
For a user-local CLI, pass `sh -s -- --cli --no-sudo` after the pipe.
See [installer options](docs/guide.md#install) for other destinations and versions.
Windows executables are unsigned; verify the release checksums before installation.

## Get connected

1. Open TailSplit, sign in to Tailscale, and select a network and exit node.
2. Review your routing rules in the configuration panel.
3. Connect when ready. Opening the app or signing in does not connect automatically.

**Application proxy** lets individual applications use the local HTTP/SOCKS
listeners. **System routing** captures system traffic when `tun.enabled` is set.
Both modes create a DNS TUN and require the privileged network service:
Windows Setup installs it; macOS requests administrator approval on first connection.

Changing networks signs out the embedded device and asks you to authorize the new
network. Configuration and device state are retained across normal updates.

## CLI

Start from [config.example.yaml](config.example.yaml):

```sh
tailsplit init -c config.yaml
tailsplit check -c config.yaml
sudo tailsplit run -c config.yaml
```

Run the Windows CLI from an Administrator terminal when connecting.
See [CLI configuration](docs/guide.md#cli-configuration) for DNS, rule lists,
network privileges, and configuration migration.

## Community

Join the [TailSplit channel in IDY Community](https://discord.gg/RY7ktfh8Pr) for
questions, routing examples, and feature ideas. Use the **ideas** and
**bug-reports** forums with the **TailSplit** tag to track feedback.
For reproducible bugs, [open a GitHub issue](https://github.com/idy/tailsplit/issues)
with your version, platform, and reproduction steps.

## Development

Requires Go 1.26.2+ and Python 3.11+. From the repository root:

```sh
make test check
python3 -B -m unittest discover -s tests/installer
python3 -B -m unittest discover -s tests/packaging
```

macOS app builds also need Xcode Command Line Tools and Swift.
See the [build guide](docs/guide.md#build-and-test) for platform-specific commands.

| Path | Contents |
| --- | --- |
| `cmd/`, `internal/` | CLI, desktop controls, network service, and configuration UI |
| `config.example.yaml` | Example routing configuration |
| `packaging/`, `scripts/` | Installers, release tools, and development helpers |
| `site/` | Public website |
| `tests/` | Installer, packaging, and website checks |

## Documentation

- [Desktop setup and controls](docs/guide.md#macos-app)
- [Windows desktop and CLI](docs/guide.md#windows-desktop)
- [Updates and older-version migration](docs/guide.md#desktop-updates)
- [CLI configuration and DNS](docs/guide.md#cli-configuration)
- [Releases and signing](docs/guide.md#ci-releases)
- [Website preview](docs/guide.md#website-preview)

## License

[GPL-3.0-or-later](LICENSE). See [third-party notices](THIRD_PARTY_NOTICES.md).
