// The Kubernetes installer prints a first-run link with a setup token in the
// fragment (https://panel.example.com/login#setup=<token>): the fragment never
// reaches the server or its logs. It is read once, before any redirect can
// drop it, kept for this tab only, and removed from the address bar.

const key = "portdock-setup-token";

/** Moves a setup token from the URL fragment to this tab's storage. */
export function captureSetupToken() {
  const m = /(?:^#|&)setup=([^&]+)/.exec(location.hash);
  if (!m) return;
  try {
    sessionStorage.setItem(key, decodeURIComponent(m[1]));
  } catch {
    // Storage blocked: the token can still be typed on the first-run page.
  }
  history.replaceState(history.state, "", location.pathname + location.search);
}

/** The setup token from the installer's link, or "". */
export function storedSetupToken(): string {
  try {
    return sessionStorage.getItem(key) ?? "";
  } catch {
    return "";
  }
}

/** Forgets the setup token once the owner account exists. */
export function forgetSetupToken() {
  try {
    sessionStorage.removeItem(key);
  } catch {
    // nothing stored
  }
}
