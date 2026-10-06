# Releasing

Releases are built and published by
[`.github/workflows/release.yml`](../.github/workflows/release.yml) when a
version tag is pushed.

1. **Check the commit.** It must be on `main` with CI passing.
2. **Tag and push:**

   ```bash
   git tag -a v0.1.0 -m "LoadTool v0.1.0"
   git push origin v0.1.0
   ```

   Versions follow semantic versioning. A suffix (`v0.2.0-rc.1`) publishes
   a pre-release.
3. **The workflow publishes the release.**
   - It runs `go vet` and `go test -race`.
   - It builds the archives with
     [`scripts/release-build.sh`](../scripts/release-build.sh): Linux,
     macOS and Windows, on amd64 and arm64.
   - It publishes them with `checksums.txt` and generated release notes.
4. **Move the major tag** the GitHub Action is used by, if there is one
   (for example `v0`), once the release is checked.

**To try the build locally:**

```bash
scripts/release-build.sh v0.0.0-local dist
(cd dist && sha256sum -c checksums.txt)
```

**The version.** The binaries report it with `loadtool --version`, and
the JSON summary and HTML report record it. `dev` means a local build.
