# Security policy

## Supported versions

Security fixes are made for the latest release. Update from **Update available** in the sidebar or **Settings → Updates**.

## Supply chain

- **Signed images.** Every release image (`ghcr.io/gavrilencu/portdock`) is signed by the release workflow with Sigstore keyless signing (GitHub OIDC) and carries an SPDX SBOM and a SLSA provenance attestation; the SBOM is attached to the GitHub release as well.
- **Updates check the signature.** Before installing a new version, PortDock runs `cosign verify` and accepts only a signature from `.github/workflows/release.yml` of the update repository, for a version tag; it then installs exactly the verified digest. Networks that cannot reach Sigstore can switch the check to *warn* (or *off*) in **Settings → Updates**.
- **Checks on every change.** Each push and pull request runs the Go tests (with the race detector), `govulncheck`, the TypeScript and translation checks, `npm audit` and an image build; CodeQL analyzes the Go and TypeScript code weekly and on every pull request; dependency updates are applied by the maintainers, and `govulncheck` and CodeQL flag vulnerable ones.
- **Verify by hand:**

  ```bash
  cosign verify ghcr.io/gavrilencu/portdock:<version> \
    --certificate-identity-regexp '^https://github.com/gavrilencu/PortDock/.github/workflows/release.yml@refs/tags/v' \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com
  ```

- **Protected history.** Nobody can force-push to `main` or delete it, and release tags (`v*`) cannot be moved or deleted once published, so a signed release always points to the code it was built from. Changes from contributors reach `main` through a pull request reviewed by the maintainer (`.github/CODEOWNERS`), with the CI checks passing. Workflows of first-time contributors run only after approval, with a read-only token.
- **Secret scanning with push protection** blocks commits that contain credentials.

## Reporting a vulnerability

Please don't open a public issue. Report it privately through GitHub: **Security → Report a vulnerability** on the [repository page](https://github.com/gavrilencu/PortDock/security/advisories/new).

Include the affected version, the steps to reproduce and the impact you see. You will get an answer as soon as possible, and credit in the release notes once the fix is out, if you want it.
