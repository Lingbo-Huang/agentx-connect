# Releasing AgentX Connect

1. Reconcile public changes with the maintained client source, run all client
   tests, and review the exact public diff. Update the version in both bootstrap
   scripts and plugin manifests before tagging. Never move a published tag.
2. Push a version tag. Release CI requires macOS, Linux and Windows tests, builds
   amd64/arm64 binaries, publishes SHA256SUMS and GitHub provenance attestations,
   then installs/removes the actual release on each native CI OS.
3. Review the release smoke results. CI evidence is separate from actual Host UI
   acceptance and live AgentX authorization. Do not remove prerelease status
   until the release's documented external approvals have been obtained.

## Apple production signing

The `Developer ID and Apple notarization` workflow signs source from a reviewed
tag using the current reviewed signing script. Configure environment `apple-release`:

| Setting | GitHub storage | Value |
| --- | --- | --- |
| APPLE_DEVELOPER_ID_P12 | Secret | Base64 Developer ID Application certificate including its private key |
| APPLE_CERTIFICATE_PASSWORD | Secret | P12 import password |
| APPLE_NOTARY_KEY_P8 | Secret | Base64 App Store Connect API private key |
| APPLE_SIGNING_IDENTITY | Variable | Exact Developer ID Application identity |
| APPLE_NOTARY_KEY_ID | Variable | API key ID |
| APPLE_NOTARY_ISSUER_ID | Variable | API issuer ID |

Enter these in GitHub's protected environment directly; never paste secrets into
an issue, prompt or repository. The workflow imports into an ephemeral keychain,
signs the binaries, requires Apple's `Accepted` response, and exports checksums,
notarization receipt and build attestations. Its output is a reviewable artifact;
it does not silently replace already published release assets. Publish signed
bytes under a new version with a new checksum manifest.

GitHub provenance does not replace an Apple Developer account, Developer ID
certificate or Apple's approval. Marketplace publication likewise uses the
publisher's account and the marketplace's own review; direct Git marketplaces
remain available while that review is pending.

## Obtaining the Apple inputs

Checked against Apple's documentation on 2026-09-04. Account enrollment and
software notarization are separate processes.

1. Use an existing paid Apple Developer Program team, or enroll at
   [Apple Developer](https://developer.apple.com/help/account/membership/program-enrollment).
   The program costs USD 99 per membership year, with regional pricing. Enable
   two-factor authentication. Individuals do not need a D-U-N-S number;
   organizations require a legal entity, binding authority and a D-U-N-S number.
2. On the Mac that will hold the signing key, open Keychain Access → Certificate
   Assistant → Request a Certificate from a Certificate Authority. Enter the
   account holder's information and save the CSR to disk. In the developer
   account, the Account Holder opens Certificates, Identifiers & Profiles →
   Certificates → + → **Developer ID Application**, then uploads that CSR.
   Download the `.cer` and import it into the same Mac's keychain. This is the
   certificate used for the current executables; Developer ID Installer is for
   a future `.pkg` distribution. See Apple's [certificate instructions](https://developer.apple.com/help/account/certificates/create-developer-id-certificates/).
3. In Keychain Access → My Certificates, select the Developer ID identity with
   its associated private key and export it as a password-protected `.p12`.
   A `.cer` alone is insufficient for CI signing. Keep the export password for
   `APPLE_CERTIFICATE_PASSWORD`; the complete identity name becomes
   `APPLE_SIGNING_IDENTITY`. See Apple's [keychain export instructions](https://support.apple.com/guide/keychain-access/kyca35961/mac).
4. In App Store Connect → Users and Access → Integrations → App Store Connect
   API → **Team Keys**, create a key for this signing workflow. If API access has
   not been enabled, the Account Holder first requests access. Download the
   `.p8` once and record its Key ID and Issuer ID. These are different from the
   Developer Team ID. Use a team key for this workflow's `--issuer` authentication;
   do not substitute an individual key. See Apple's [API setup instructions](https://developer.apple.com/help/app-store-connect/get-started/app-store-connect-api/).
5. In this GitHub repository, open Settings → Environments → `apple-release`.
   Add the three secrets and three variables listed above. The `.p12` and `.p8`
   secrets contain base64-encoded file bytes; the password is its original text.
   Encoding is only a transport format, not encryption. Enter values directly
   into GitHub's secret fields, never a chat or issue.
6. In Actions, select Developer ID and Apple notarization → Run workflow, and
   supply the reviewed source tag. Successful execution produces the
   `notarized-macos` artifact and an Apple `Accepted` receipt. Review those bytes
   before integrating them into a new signed release; existing release assets
   and tags must remain immutable.

## Timing

| Situation | Planning expectation |
| --- | --- |
| Paid account and API access already available | Allow roughly 30–60 minutes for certificate export and CI input setup; this is an engineering estimate, not an Apple SLA. |
| New individual membership | Apple does not promise a total approval time. Its current guidance says to contact support if membership confirmation has not arrived within 24 hours after purchase. Identity checks may add time. |
| Organization without D-U-N-S | Apple advises allowing up to five business days for D&B issuance, then up to two business days for Apple to receive the record, followed by enrollment verification. See [D-U-N-S guidance](https://developer.apple.com/help/account/membership/D-U-N-S/). |
| Notarization submission | Apple's published guidance says most submissions finish within five minutes and 98% within fifteen minutes. This is not a deadline; pending submissions need status reconciliation rather than blind resubmission. See [notarization workflow](https://developer.apple.com/documentation/security/customizing-the-notarization-workflow). |

API access requests are reviewed case by case. If that step delays initial
validation, Apple also supports `notarytool` using an Apple Account and an
app-specific password stored in Keychain. The checked-in CI workflow currently
uses the team API key path; do not put an Apple Account password into its P8 field.
