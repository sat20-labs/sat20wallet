# PWA biometric unlock notes

Updated: 2026-05-30

The PWA biometric unlock uses WebAuthn platform authenticator plus the PRF
extension. The PRF result is used to derive the local AES-GCM key that decrypts
the stored wallet password hash. Do not enable passwordless unlock on a platform
that can authenticate but cannot return PRF output.

## Verified behavior

### macOS / MacBook

Effective path:

1. Create a platform credential with `extensions.prf.eval`.
2. If `navigator.credentials.create()` does not return `prf.results.first`,
   immediately call `navigator.credentials.get()` for the created credential.
3. Try `evalByCredential` first, then `eval`.
4. Encrypt the wallet password hash only after a PRF output is returned.

This path has been verified on a MacBook and must be preserved.

## Current implementation

The implementation keeps two compatible PRF paths:

### Apple platform path

iOS/iPadOS and macOS use the same Apple platform credential PRF path. The
MacBook part of this path has already been verified and must not be replaced by
the Android compatibility path:

1. Create a platform credential with `extensions.prf.eval.first`.
2. Use `residentKey: 'discouraged'` and `requireResidentKey: false`.
3. If `navigator.credentials.create()` returns `prf.results.first`, use that
   output directly.
4. If create does not return PRF output, immediately call
   `navigator.credentials.get()` for the created credential and try
   `evalByCredential` first, then `eval`.

If iPhone/iPad still fails on a real device, treat that as a platform/version/
installed-PWA availability issue and keep password unlock. Do not save a
passwordless biometric credential unless a concrete PRF output is returned.

### Other platforms, including Windows and Android

Other platforms follow the `passkeyprf.com` flow unless we have a separate
device/browser combination that has been tested and verified:

1. Create a platform passkey with `residentKey: 'required'`,
   `userVerification: 'required'`, and `extensions.prf: {}`.
2. Require the created credential to report `prf.enabled === true`.
3. Immediately authenticate that credential with `extensions.prf.eval.first`
   using the wallet salt.
4. Save the biometric unlock record only if `prf.results.first` is returned.

If any step cannot return a PRF output, the wallet must not save a passwordless
biometric unlock credential. The user keeps using password unlock.

For Windows, align with the current `passkeyprf.com` result: Windows Hello may
authenticate but should be treated as unsupported for wallet passwordless
biometric unlock unless it returns `prf.results.first` in our concrete test.

## Open investigation

### iPhone

Observed on a real iPhone:

- `checkBiometricSupport()` reported `No platform authenticator is available`.

Current mitigation:

- Also read `PublicKeyCredential.getClientCapabilities()` and treat
  `userVerifyingPlatformAuthenticator: true` as available on iOS even when
  `isUserVerifyingPlatformAuthenticatorAvailable()` reports false.

Next data to collect from `https://sat20.org/pwa/?debug=1`:

```js
PublicKeyCredential.getClientCapabilities?.().then(console.log)
window.__SAT20_WEBAUTHN_SUPPORT__
window.__SAT20_WEBAUTHN_DEBUG__
```

### Android Edge

Observed:

- Enabling biometric unlock can hang in the installed PWA.
- Chrome-installed PWA can show the system fingerprint prompt, but repeated
  PRF fallback attempts can trigger repeated fingerprint prompts and end in a
  timeout.

Current mitigation:

- Do not disable Android Edge by user agent.
- Remove the intermediate browser `confirm()` so WebAuthn creation is invoked
  directly from the user's click.
- Use static imports for biometric modules in the settings page, avoiding
  chunk-loading between the user's click and WebAuthn creation.
- Do not block creation on `isUserVerifyingPlatformAuthenticatorAvailable()`;
  call `navigator.credentials.create()` and use its concrete result/error.
- Follow `passkeyprf.com`: request PRF capability during create with `prf: {}`,
  then do a concrete `get()` request with `prf.eval.first`.
- Use a 60 second timeout, matching the reference site, so the system biometric
  prompt has enough time to complete.

Next data to collect from Android Edge remote debugging:

```js
PublicKeyCredential.getClientCapabilities?.().then(console.log)
window.__SAT20_WEBAUTHN_SUPPORT__
window.__SAT20_WEBAUTHN_DEBUG__
```

If Android still hangs before debug output reaches `create`, compare behavior in
browser mode and installed PWA mode. If it hangs only in installed mode, keep the
wallet password unlock path as the fallback and isolate whether Edge's installed
PWA WebAuthn implementation is losing the callback.
