package cyou.rime.telemetry.collector

import android.content.Context
import android.content.Intent
import java.net.URI
import java.net.URLDecoder

/**
 * What a freshly installed phone needs, since a public build has none built
 * in: the server's address, the relay's address and key when there is one,
 * and a one-time setup code made in the panel. It arrives as
 * telemetry://setup?server=…&code=…&relay=…&key=… (from the join page the
 * panel's QR opens, or from deploy/macos/install-phone.sh), or as the join
 * link itself, https://…/join#s=…&c=…&r=…&k=…, pasted into setup. A phone
 * already paired ignores it, so a link can never move a working phone to
 * another server.
 */
object SetupLink {
    data class Addresses(val server: String, val relay: String, val relayKey: String, val code: String = "")

    fun parse(link: String?): Addresses? {
        val text = link?.trim() ?: return null
        val uri = runCatching { URI(text) }.getOrNull() ?: return null
        val fields = when {
            uri.scheme == "telemetry" && uri.host == "setup" -> query(uri.rawQuery ?: return null)
                .let { mapOf("s" to it["server"], "c" to it["code"], "r" to it["relay"], "k" to it["key"]) }
            uri.scheme == "https" && uri.rawFragment != null -> query(uri.rawFragment)
            else -> return null
        }
        val server = fields["s"]?.trim()?.trimEnd('/').orEmpty()
        val relay = fields["r"]?.trim()?.trimEnd('/').orEmpty()
        val key = fields["k"]?.trim().orEmpty()
        val code = fields["c"]?.trim().orEmpty()
        if (!server.startsWith("https://")) return null
        if (relay.isEmpty() && key.isEmpty()) return Addresses(server, "", "", code)
        if (!relay.startsWith("https://") || runCatching { SealedChannel(relay, key) }.isFailure) return null
        return Addresses(server, relay, key, code)
    }

    private fun query(raw: String): Map<String, String> = raw.split("&").mapNotNull {
        val parts = it.split("=", limit = 2)
        if (parts.size == 2) parts[0] to URLDecoder.decode(parts[1], "UTF-8") else null
    }.toMap()

    /** Saves the link's addresses and code on a phone that is not paired yet. */
    fun apply(context: Context, intent: Intent?): Boolean = applyLink(context, intent?.dataString)

    fun applyLink(context: Context, link: String?): Boolean {
        val addresses = parse(link) ?: return false
        val config = DeliveryConfig(context)
        if (config.token() != null) return false
        config.serverUrl = addresses.server
        config.relayUrl = addresses.relay
        config.relayKey = addresses.relayKey
        config.setupCode = addresses.code
        return true
    }
}
