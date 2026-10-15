# Third-Party Components

The exporter bundles the unmodified LibreSpeed CLI as a separate executable.
This notice is informational and is not legal advice; verify redistribution
obligations for each release.

Go module dependencies are analyzed with pinned Google `go-licenses` v2.0.1.
Generated notices and copyright statements live in `LICENSES/GO-MODULES`, ship
in the runtime image, and are attached to GitHub releases as
`go-module-licenses.tar.gz`. The exporter module's Apache-2.0 license and the
LibreSpeed CLI notices below are distributed separately and excluded from the
generated module notice set.

## LibreSpeed CLI

- Project: <https://github.com/librespeed/speedtest-cli>
- Release: `v1.0.14`
- Source commit: `42e5f76c90e64e04ab7079fd76d833dfe41805da`
- License: GNU Lesser General Public License, version 3.0, incorporating
  GNU General Public License, version 3.0 terms; see
  `LICENSES/librespeed-cli-LGPL-3.0.txt` and `LICENSES/GPL-3.0.txt`.
- Corresponding source: release assets use
  `https://github.com/home-ops/librespeed-exporter/releases/download/<exporter-release>/librespeed-cli-<CLI-version>-source.tar.gz`.
- Corresponding-source archive: unmodified `git archive` of the exact commit
  above, compressed with `gzip -n`; each release includes a `.sha256` sidecar
  generated from that archive and verified before publication.
- Corresponding-source archive SHA-256: `d55a4da7bf41f07f4b8ec07703978ba0da2944c69af62cbd396be4aa7cea4298`.
- Build toolchain: `golang:1.25.8-bookworm`, pinned to OCI index digest
  `sha256:4557cf171e3cdf5053a298d5171b1a5f5734d920260c25f22c79e94760eb2084`.
- Runtime: `gcr.io/distroless/static-debian12:nonroot`, pinned to OCI index
  digest `sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab`.

The corresponding-source archive is generated from the exact upstream commit
above, without modifications. Publish the archive beside each exporter release
that distributes the CLI binary, and verify the archive checksum and commit
before publication. Runtime image intentionally contains exporter and CLI
executables, CA certificates, and license notices, but not source archive. The
digest-pinned images above were resolved from their registries on 2026-10-14.
Future source/toolchain/base changes require updating pins and corresponding
checksums together.
