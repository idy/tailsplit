# Optional macOS release credentials

Source builds, tests, and `make app` use no private credentials. The default app
package uses an ad-hoc signature. On macOS, install Go, Python 3, and the Xcode
command-line tools; then run `make app`. Override the executables with
`make app GO=/path/to/go PYTHON=/path/to/python3` when needed.

Developer ID signing and Apple notarization are optional operator steps. Keep
all credential files outside the checkout. This directory contains public setup
instructions only. `../release-identity.json` contains the public Apple team ID
used to validate official release identities; it is not a credential. Operators
using their own Apple team must update that public metadata in their own fork.

## SOPS configuration

Install [SOPS](https://github.com/getsops/sops) and configure the age identity with
one of SOPS's standard `SOPS_AGE_KEY`, `SOPS_AGE_KEY_FILE`, or `SOPS_AGE_KEY_CMD`
environment variables. The wrappers pass the environment directly to SOPS.
They do not consult another repository, invoke a secret helper, or retrieve a
Keychain identity for decryption.

Supply the encrypted JSON file explicitly with `--credentials`, or set its
external path in `TAILSPLIT_SIGNING_CREDENTIALS` (Developer ID) or
`TAILSPLIT_NOTARY_CREDENTIALS` (notarization). A command-line path takes precedence.
No default credential file is shipped or loaded. The encrypted files and their
decrypted contents must remain private.

## Developer ID signing

The signing credential's decrypted JSON has these fields:

- `team_id`: the team ID in `release-identity.json`.
- `intermediate_certificate`: a base64-encoded DER Apple intermediate certificate.
- `application` and `installer`: objects containing `status` set to
  `issued-and-validated`, `certificate` as base64-encoded DER, `private_key_encoding`
  set to `base64gzip`, and `private_key` as a base64-encoded gzip stream containing
  a PEM PKCS#8 private key.

The certificates must be Developer ID Application and Developer ID Installer
identities for the expected team. The wrapper validates each certificate and its
private key, imports them into a temporary keychain, verifies an export round
trip, and deletes the temporary keychain when the command exits. It uses the
bundled `scripts/signing-keychain.swift` helper, OpenSSL 3, and macOS signing tools.

```sh
python3 scripts/with-signing-credentials.py \
  --credentials /absolute/operator/path/developer-id.json.sops \
  -- env SIGNING_MODE=developer-id make app
```

Use `--check` instead of a command to sign and verify temporary probes. The
explicit `--install` option retains identities in the default local keychain;
normal command execution uses a temporary keychain.

## Notarization

The notarization credential's decrypted JSON contains `team_id`, `key_id`,
`issuer_id`, `private_key_encoding` set to `base64gzip`, and `private_key` as a
base64-encoded gzip stream containing the Apple API PEM private key. The wrapper
writes the key into a private temporary directory, passes its path to the child
command, and removes the directory on exit. It checks the expected team and
supplies `NOTARY_KEY_FILE`, `NOTARY_KEY_ID`, and `NOTARY_ISSUER_ID` to packaging.

To sign and notarize with two external encrypted files:

```sh
python3 scripts/with-signing-credentials.py \
  --credentials /absolute/operator/path/developer-id.json.sops \
  --notary-credentials /absolute/operator/path/app-store-connect.json.sops \
  -- make dist
```

For identities already installed in an operator-managed keychain, use the notary
wrapper on its own:

```sh
python3 scripts/with-notary-credentials.py \
  --credentials /absolute/operator/path/app-store-connect.json.sops \
  -- make dist
```

Packaging also accepts `APPLE_KEYCHAIN`, `APPLE_APPLICATION_IDENTITY`,
`APPLE_INSTALLER_IDENTITY`, and an existing `NOTARY_KEYCHAIN_PROFILE`. Setting a
notary Keychain profile uses that profile instead of the temporary API key.
`APPLE_TEAM_ID`, when supplied, must match the credential wrappers' public team
metadata. The package builder can select already installed Developer ID
identities for that team when explicit identity fingerprints are omitted.

Before running the build command, both wrappers remove all `SOPS_AGE_*`
environment variables. The combined signing command forwards those variables
only to the bundled notarization loader, which removes them before invoking the
build. Neither loader prints private key material. The synthetic Python tests
mock every credential decryption, signing, and Keychain operation.

## GitHub Actions setup

The release workflow uses the `release` environment. Restrict its deployment
policy to version tags (`v*`), then configure these environment secrets:

- `TAILSPLIT_DEVELOPER_ID_SOPS`: the encrypted Developer ID JSON file contents.
- `TAILSPLIT_NOTARY_SOPS`: the encrypted notarization JSON file contents.
- `TAILSPLIT_RELEASE_AGE_KEY`: a dedicated age identity that decrypts those two
  files. Do not use an identity that also decrypts unrelated operational secrets.

Set the environment variable `TAILSPLIT_SIGNED_RELEASES` to `true` only after all
three inputs are available. Both macOS architectures then require Developer ID
signing and accepted notarization before publication; a signing failure does not
fall back to an ad-hoc public release. Without that variable, the workflow builds
explicitly labeled ad-hoc receipts. The encrypted inputs are materialized in the
runner's private temporary directory, outside the source and artifact paths.
