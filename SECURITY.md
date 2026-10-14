# Security policy

## Supported versions

Security fixes are made for the latest release. Update from **Update available** in the sidebar or **Settings → Updates**.

## Supply chain

- **Signed images.** Every release image (`ghcr.io/gavrilencu/portdock`) is signed by the release workflow with Sigstore keyless signing (GitHub OIDC) and carries an SPDX SBOM and a SLSA provenance attestation; the SBOM is attached to the GitHub release as well.
- **Updates check the signature.** Before installing a new version, PortDock runs `cosign verify` and accepts only a signature from `.github/workflows/release.yml` of the update repository, for a version tag; it then installs exactly the verified digest. Networks that cannot reach Sigstore can switch the check to *warn* (or *off*) in **Settings → Updates**.
- **Checks on every change.** Each push and pull request runs the Go tests (with the race detector), `govulncheck`, the TypeScript and translation checks, `npm audit` and an image build; CodeQL analyzes the Go and TypeScript code weekly and on every pull request; Dependabot proposes dependency updates.
- **Verify by hand:**

  ```bash
  cosign verify ghcr.io/gavrilencu/portdock:<version> \
    --certificate-identity-regexp '^https://github.com/gavrilencu/PortDock/.github/workflows/release.yml@refs/tags/v' \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com
  ```

- **Maintainers** sign their commits and tags (`git config commit.gpgsign true`, or SSH signing) and protect `main` with required status checks (*ci*, *codeql*) and required signed commits in the repository settings.

## Reporting a vulnerability

Please don't open a public issue. Report it privately through GitHub: **Security → Report a vulnerability** on the [repository page](https://github.com/gavrilencu/PortDock/security/advisories/new).

Include the affected version, the steps to reproduce and the impact you see. You will get an answer as soon as possible, and credit in the release notes once the fix is out, if you want it.
