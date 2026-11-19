# Publishing Pulso

## Automatic releases

Every push accepted on `main` starts the **Release** workflow. Documentation and
installer changes also participate; there is no path filter on that trigger.
The default version increment is a patch: `v0.2.1` becomes `v0.2.2`.

The workflow performs these steps:

1. Runs the reusable CI, container and installer checks against the event commit.
2. Finds the highest stable Git tag and reserves the next version for that commit.
3. Builds Linux, macOS and Windows binaries and both Docker runtime variants.
4. Uploads binaries, archives, SHA-256 checksums and generated notes to a draft release.
5. Verifies that both Docker variants contain Linux amd64 and arm64 images.
6. Promotes the stable images to `latest` and `latest-python311`, then publishes
   the release and marks it as the latest release.

The image tags `vX.Y.Z` and `vX.Y.Z-python311`, binary versions and GitHub release
all use the same version and source revision. The panel only sees the release
after its assets and images have been uploaded. Servers still adopt an update
through the panel or installer; publishing a release does not restart servers.

The jobs use the repository's `GITHUB_TOKEN` with `contents: write` and
`packages: write` only where needed. No personal token is required. Reusable
workflows run the publication directly, because [events created by
GITHUB_TOKEN do not normally trigger another workflow](https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/trigger-a-workflow).

## Other version increments and manual tags

For a minor or major release, open **Actions → Release → Run workflow**, select
`main`, and choose `minor` or `major`. No version file or release commit is
created: Git tags are the version history. Each new manual execution requests
a new version, even if the commit already has a patch release. Re-running that
same execution reuses its reserved tag, identified by the run ID in the tag's
annotation.

Existing manual tag publication remains supported:

```bash
git tag v1.0.0-rc.1
git push origin v1.0.0-rc.1
```

Manual tags must be valid semantic versions and point at the checked-out event
commit. Prereleases are published without moving the Docker `latest` tags or
the latest stable GitHub release. Publishing an older stable version also
preserves the current latest version.

## Retries and concurrent updates

Release executions share a concurrency group, do not cancel an active run,
and use GitHub.com's [`queue: max`](https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/control-workflow-concurrency)
to retain up to 100 pending executions. A stale automatic execution whose
commit has already been superseded by a stable tag is skipped, preventing a
later version number from publishing older code.

A failed validation does not reserve a tag or publish a release. A later build
or upload failure can leave a reserved tag, versioned images or a draft release;
rerun the failed jobs in Actions. The same commit reuses its reserved version.
An already published release is skipped, rather than overwritten or duplicated.
The two moving Docker tags are updated sequentially; they are not a transaction.
If final promotion or publication fails, rerun to finish the same version.

The automation policy and publication ordering are covered by local tests using
an isolated bare Git remote and simulated GitHub/registry responses:

```bash
python3 -m unittest discover -s scripts -p 'test_release_*.py' -v
```

Actionlint versions through 1.7.12 predate the documented `queue` field. With
that version, validate all other workflow fields using:

```bash
actionlint -ignore '^unexpected key "queue" for "concurrency" section' .github/workflows/*.yml
```

## Binaries, checksums and repository identity

The source module is `pulso.local/pulso`, so development builds resolve internal
imports locally without claiming a public repository or domain.

Release builds inject `GITHUB_REPOSITORY` into
`internal/version.ReleaseRepository`. Local builds have no release repository.
You can explicitly configure one with `PULSO_RELEASE_REPOSITORY=owner/repository`.
Only use a repository you control and trust. Both the binary installer and
`pulso update` use GitHub HTTPS release downloads and require SHA-256 checksums.

The `pulso update` command refuses to replace the executable if checksum
verification fails. Releases include `checksums.txt`, the platform binaries
and platform archives built by `.github/workflows/release-binaries.yml`.
Archives contain a top-level directory, README, LICENSE and third-party notices;
the installer supports that layout and flat archives.

If you publish a Go module, replace `pulso.local/pulso` consistently in go.mod,
imports and linker flags with your actual module path before releasing.

The Nix dependency vendor hash includes the pure Go SQLite driver and its
dependencies. It is tied to `go.mod` and `go.sum`, so refresh it with
`sh scripts/update-vendor-hash.sh` whenever a dependency changes, then verify the
package with `nix build .` before publishing it. See the Nix package section of
[developer-guide.md](developer-guide.md).

Keep LICENSE intact when redistributing this source or binaries.
