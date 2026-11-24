#!/usr/bin/env python3
"""Writes the Mac's MDM enrollment profile.

Usage: enrollment.py SERVER_URL TOPIC IDENTITY_P12 P12_PASSWORD POLICY_PLIST OUT SERVER_CA_PEM

The profile enrolls the Mac in the owner's own NanoMDM and, in the same
profile, sets the browser policy that installs the Telemetry extension. A
Chromium browser installs an extension from outside its Web Store by policy
only on a Mac enrolled in MDM; with it, the extension shows as managed by the
organization and cannot be switched off or removed in the browser, as on
Linux (owner request 2026-11-24).

The MDM may only list and install profiles (AccessRights 3): it cannot lock
or erase the Mac, query it, or install apps. Removing the profile in System
Settings → Device Management un-enrolls the Mac.
"""
import base64
import plistlib
import sys
import uuid

server, topic, p12_path, p12_password, policy_path, out, server_ca = sys.argv[1:8]
policy = plistlib.load(open(policy_path, "rb"))
# Profiles carry certificates as DER.
pem = open(server_ca).read()
server_ca_der = base64.b64decode("".join(pem.split("-----BEGIN CERTIFICATE-----")[1].split("-----END CERTIFICATE-----")[0].split()))
identity = str(uuid.uuid4()).upper()

payloads = [
    # The MDM server's HTTPS certificate comes from this CA, which is
    # name-constrained to the server's own tailnet name and address.
    {
        "PayloadType": "com.apple.security.root",
        "PayloadVersion": 1,
        "PayloadIdentifier": "cyou.rime.telemetry.mdm.server-ca",
        "PayloadUUID": str(uuid.uuid5(uuid.NAMESPACE_URL, "telemetry-mdm-server-ca")).upper(),
        "PayloadDisplayName": "Telemetry MDM server CA",
        "PayloadContent": server_ca_der,
    },
    {
        "PayloadType": "com.apple.security.pkcs12",
        "PayloadVersion": 1,
        "PayloadIdentifier": "cyou.rime.telemetry.mdm.identity",
        "PayloadUUID": identity,
        "PayloadDisplayName": "Telemetry MDM device identity",
        "PayloadContent": open(p12_path, "rb").read(),
        "Password": p12_password,
    },
    {
        "PayloadType": "com.apple.mdm",
        "PayloadVersion": 1,
        "PayloadIdentifier": "cyou.rime.telemetry.mdm",
        "PayloadUUID": str(uuid.uuid4()).upper(),
        "PayloadDisplayName": "Telemetry MDM",
        "ServerURL": server + "/mdm",
        "Topic": topic,
        "IdentityCertificateUUID": identity,
        "SignMessage": True,
        "CheckOutWhenRemoved": True,
        # 1: list profiles, 2: install and remove profiles. Nothing else.
        "AccessRights": 3,
    },
]
for browser in ["net.imput.helium", "com.google.Chrome", "org.chromium.Chromium", "com.brave.Browser", "com.microsoft.Edge"]:
    payloads.append({
        "PayloadType": "com.apple.ManagedClient.preferences",
        "PayloadVersion": 1,
        "PayloadIdentifier": "cyou.rime.telemetry.browser." + browser,
        "PayloadUUID": str(uuid.uuid5(uuid.NAMESPACE_URL, "telemetry-browser-policy/" + browser)).upper(),
        "PayloadDisplayName": "Telemetry extension in " + browser,
        "PayloadContent": {browser: {"Forced": [{"mcx_preference_settings": policy}]}},
    })

profile = {
    "PayloadType": "Configuration",
    "PayloadVersion": 1,
    "PayloadIdentifier": "cyou.rime.telemetry.enrollment",
    "PayloadUUID": str(uuid.uuid4()).upper(),
    "PayloadDisplayName": "Telemetry",
    "PayloadDescription": "Enrolls this Mac in your own Telemetry MDM so browsers install the Telemetry extension by policy. "
                          "The MDM can only list and install profiles; it cannot lock, erase or inspect the Mac.",
    "PayloadOrganization": "Telemetry",
    "PayloadScope": "System",
    "PayloadContent": payloads,
}
with open(out, "wb") as f:
    plistlib.dump(profile, f)
