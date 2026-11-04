package cyou.rime.telemetry.collector

import android.content.Context
import android.content.Intent
import java.net.URI
import java.net.URLDecoder

/**
 * The addresses a freshly installed phone needs, since a public build has none
 * built in: telemetry://setup?server=…&relay=…&key=…, opened by
 * deploy/macos/install-phone.sh. A phone already paired ignores it, so a link
 * can never move a working phone to another server.
 */
object SetupLink {
    data class Addresses(val server: String, val relay: String, val relayKey: String)

    fun parse(link: String?): Addresses? {
        val uri = runCatching { URI(link ?: return null) }.getOrNull() ?: return null
        if (uri.scheme != "telemetry" || uri.host != "setup") return null
        val query = (uri.rawQuery ?: return null).split("&").mapNotNull {
            val parts = it.split("=", limit = 2)
            if (parts.size == 2) parts[0] to URLDecoder.decode(parts[1], "UTF-8") else null
        }.toMap()
        val server = query["server"]?.trim()?.trimEnd('/').orEmpty()
        val relay = query["relay"]?.trim()?.trimEnd('/').orEmpty()
        val key = query["key"]?.trim().orEmpty()
        if (!server.startsWith("https://")) return null
        if (relay.isEmpty() && key.isEmpty()) return Addresses(server, "", "")
        if (!relay.startsWith("https://") || runCatching { SealedChannel(relay, key) }.isFailure) return null
        return Addresses(server, relay, key)
    }

    /** Saves the link's addresses on a phone that is not paired yet. */
    fun apply(context: Context, intent: Intent?): Boolean {
        val addresses = parse(intent?.dataString) ?: return false
        val config = DeliveryConfig(context)
        if (config.token() != null) return false
        config.serverUrl = addresses.server
        config.relayUrl = addresses.relay
        config.relayKey = addresses.relayKey
        return true
    }
}
