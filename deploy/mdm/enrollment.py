#!/usr/bin/env python3
"""Writes the Mac's MDM enrollment profile.

Usage: enrollment.py SERVER_URL TOPIC IDENTITY_P12 P12_PASSWORD POLICY_PLIST OUT SERVER_CA_PEM

POLICY_PLIST is the Chromium browsers' policy; Firefox and LibreWolf get
theirs from this script. With TELEMETRY_UNSIGNED_XPI=1 LibreWolf may also
install add-ons Mozilla did not sign, for a release published without
Mozilla's signature.

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
import copy
import os
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
        # macOS refuses an MDM payload that does not say it handles the user
        # channel too; NanoMDM does.
        "ServerCapabilities": ["com.apple.mdm.per-user-connections"],
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

# Firefox and LibreWolf read enterprise policies from managed preferences for
# their own bundle ID (/Library/Managed Preferences/<bundle ID>.plist, which
# macOS writes from this profile), once EnterprisePoliciesEnabled is set. The
# same policy as Policies in internal/xpi: Telemetry force-installed from the
# collector, in private windows too, updating from there. The extension
# records the tab in front itself, so no ActivityWatch add-on is installed
# (owner 2026-11-26).
firefox_policy = {
    "EnterprisePoliciesEnabled": True,
    "ExtensionSettings": {
        "telemetry@rime.cyou": {
            "installation_mode": "force_installed",
            "install_url": "http://127.0.0.1:5600/api/telemetry/extension.xpi",
            "update_url": "http://127.0.0.1:5600/api/telemetry/updates.json",
            "private_browsing": True,
        },
    },
    # Firefox cannot be told to check for an update now; it asks hourly.
    "Preferences": {"extensions.update.interval": {"Value": 3600, "Status": "default"}},
}
# A policy from here replaces the policy of the same name in the browser's
# own distribution/policies.json, so LibreWolf's own extension settings (as
# shipped in LibreWolf 161) are kept beside ours.
librewolf_policy = copy.deepcopy(firefox_policy)
librewolf_policy["ExtensionSettings"].update({
    "*": {
        "blocked_install_message": "LibreWolf does not allow installing Language Packs.",
        "installation_mode": "allowed",
        "allowed_types": ["dictionary", "extension", "sitepermission", "theme"],
    },
    "uBlock0@raymondhill.net": {
        "install_url": "https://addons.mozilla.org/firefox/downloads/latest/uBlock0@raymondhill.net/latest.xpi",
        "installation_mode": "normal_installed",
        "private_browsing": True,
    },
})
# A release without Mozilla's signature (no AMO credentials) installs only
# where signatures are not required, which LibreWolf lets policy say. That
# lets any unsigned add-on install, so it is set only on request.
if os.environ.get("TELEMETRY_UNSIGNED_XPI") == "1":
    librewolf_policy["Preferences"]["xpinstall.signatures.required"] = {"Value": False, "Status": "locked"}
for browser, content in [("net.librewolf.librewolf", librewolf_policy), ("org.mozilla.firefox", firefox_policy)]:
    payloads.append({
        "PayloadType": "com.apple.ManagedClient.preferences",
        "PayloadVersion": 1,
        "PayloadIdentifier": "cyou.rime.telemetry.browser." + browser,
        "PayloadUUID": str(uuid.uuid5(uuid.NAMESPACE_URL, "telemetry-browser-policy/" + browser)).upper(),
        "PayloadDisplayName": "Telemetry extension in " + browser,
        "PayloadContent": {browser: {"Forced": [{"mcx_preference_settings": content}]}},
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
