package cyou.rime.telemetry.collector

import org.json.JSONArray
import java.net.HttpURLConnection
import java.net.URL
import java.net.URLEncoder
import java.util.Locale

/**
 * Finds places by name for a habit rule, with OpenStreetMap's Nominatim.
 * Only the words typed and, to put near places first, a box around the
 * phone's last position are sent. The User-Agent is the app's generic name,
 * never anything about the owner.
 */
object PlaceSearch {
    /** The User-Agent for Nominatim and the map's tiles. */
    const val USER_AGENT = "Telemetry-Android/1 (habit place picker)"
    private const val ENDPOINT = "https://nominatim.openstreetmap.org/search"

    data class Found(val name: String, val detail: String, val lat: Double, val lon: Double)

    /** The search address: near [near] first when known, but not only there. */
    fun url(query: String, near: RuleFix?): String {
        val params = mutableListOf("format=jsonv2", "limit=6", "q=" + URLEncoder.encode(query.trim(), "UTF-8"))
        if (near != null) {
            val box = String.format(Locale.ROOT, "%.4f,%.4f,%.4f,%.4f", near.lon - 0.2, near.lat + 0.2, near.lon + 0.2, near.lat - 0.2)
            params += "viewbox=$box"
        }
        return "$ENDPOINT?${params.joinToString("&")}"
    }

    /** Nominatim's answer as places: the first part of the address as the name, the next two as the detail. */
    fun parse(body: String): List<Found> {
        val array = runCatching { JSONArray(body) }.getOrNull() ?: return emptyList()
        return (0 until array.length()).mapNotNull { index ->
            val item = array.optJSONObject(index) ?: return@mapNotNull null
            val lat = item.optString("lat").toDoubleOrNull() ?: return@mapNotNull null
            val lon = item.optString("lon").toDoubleOrNull() ?: return@mapNotNull null
            val parts = item.optString("display_name").split(",").map { it.trim() }.filter { it.isNotEmpty() }
            val name = item.optString("name").ifBlank { parts.firstOrNull().orEmpty() }.ifBlank { return@mapNotNull null }
            Found(name.take(100), parts.drop(1).take(2).joinToString(", "), lat, lon)
        }
    }

    /** Blocking IO; null when it could not be asked. */
    fun search(query: String, near: RuleFix?): List<Found>? = runCatching {
        val connection = URL(url(query, near)).openConnection() as HttpURLConnection
        connection.connectTimeout = 8_000
        connection.readTimeout = 8_000
        connection.setRequestProperty("User-Agent", USER_AGENT)
        connection.setRequestProperty("Accept-Language", Locale.getDefault().toLanguageTag())
        try {
            if (connection.responseCode !in 200..299) return@runCatching null
            parse(connection.inputStream.bufferedReader().use { it.readText() })
        } finally {
            connection.disconnect()
        }
    }.getOrNull()
}
