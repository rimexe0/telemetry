package cyou.rime.telemetry.collector

import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Build
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/**
 * Pairs this phone with the server. With a setup code made in the panel it is
 * connected at once; without one it asks, and polls until the owner clicks
 * Connect in the panel. Progress goes to [onStatus], in words that say what
 * to do next; [onPaired] runs once the credential is stored and delivery is
 * scheduled.
 */
class PairingFlow(
    private val context: Context,
    private val scope: CoroutineScope,
    private val onStatus: (String) -> Unit,
    private val onPaired: () -> Unit = {},
) {
    private companion object {
        const val POLL_INTERVAL_MS = 3_000L
        val RETRYABLE = setOf(408, 425, 429, 500, 502, 503, 504)
    }

    private var job: Job? = null

    /** The panel link for approving on this phone instead of another signed-in panel. */
    var approvalUrl: String? = null
        private set

    fun start(serverUrl: String, openBrowser: Boolean = true, code: String = "") {
        val config = DeliveryConfig(context)
        if (serverUrl.isBlank()) {
            onStatus("Enter your Telemetry server's address first.")
            return
        }
        job?.cancel()
        val secret = config.newPairingSecret(serverUrl)
        onStatus(if (code.isNotBlank()) "Connecting with the setup code…" else "Asking the server…")
        scope.launch {
            try {
                val retainedId = config.pairingId.takeIf { code.isBlank() }
                val start = if (retainedId != null) {
                    PairingStart(retainedId, Uri.parse(serverUrl.trimEnd('/') + "/panel/").buildUpon().appendQueryParameter("pair", retainedId).build().toString())
                } else withContext(Dispatchers.IO) {
                    client(serverUrl).start(secret, code)
                }
                config.savePairing(start.id, secret)
                if (start.approved) {
                    connected(config, serverUrl, secret)
                    return@launch
                }
                approvalUrl = start.panelUrl
                if (openBrowser) {
                    context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(start.panelUrl)).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
                    onStatus("Sign in in the browser and tap Connect, then come back here.")
                } else {
                    onStatus("Now open the Telemetry panel and click Connect on the \"wants to connect\" banner. This screen moves on by itself.")
                }
                poll(serverUrl, start.id, secret)
            } catch (error: Exception) {
                if (error is kotlinx.coroutines.CancellationException) throw error
                if (error is PairingException && error.code == "invalid_code") config.setupCode = ""
                onStatus(explain(error, serverUrl))
            }
        }
    }

    private fun connected(config: DeliveryConfig, serverUrl: String, secret: String) {
        config.completePairing(serverUrl, secret)
        BackgroundJobs.scheduleDelivery(context)
        onStatus("Phone connected.")
        onPaired()
    }

    /** Continues a pairing that was started before the app was left. */
    fun resume() {
        val config = DeliveryConfig(context)
        val pairingId = config.pairingId ?: return
        val secret = config.pairingSecret() ?: return
        if (job?.isActive == true) return
        val server = config.pairingServer() ?: return
        // A pairing with an address that no longer exists never completes.
        if (server.trimEnd('/') in RETIRED_SERVERS) {
            config.clearPairing()
            return
        }
        poll(server, pairingId, secret, resumed = true)
    }

    private fun client(serverUrl: String) = TelemetryPairingClient(
        serverUrl = serverUrl,
        deviceId = DeviceIdentity.get(context.applicationContext),
        deviceName = deviceDisplayName(),
        sealed = SealedChannel.configured(context),
    )

    // A resumed request that has expired is cleared quietly; it is not news.
    private fun poll(serverUrl: String, pairingId: String, secret: String, resumed: Boolean = false) {
        job?.cancel()
        job = scope.launch {
            val config = DeliveryConfig(context.applicationContext)
            val client = client(serverUrl)
            while (true) {
                try {
                    val result = withContext(Dispatchers.IO) { client.poll(pairingId, secret) }
                    when (result.state) {
                        PairingStatus.State.PENDING -> onStatus("Waiting for approval. Click Connect on the \"wants to connect\" banner in the Telemetry panel.")
                        PairingStatus.State.APPROVED -> {
                            if (result.deviceId != DeviceIdentity.get(context.applicationContext)) {
                                throw IllegalStateException("pairing approval was for another device")
                            }
                            connected(config, serverUrl, secret)
                            return@launch
                        }
                        PairingStatus.State.DENIED -> {
                            config.clearPairing()
                            onStatus("Pairing was denied. Start it again when ready.")
                            return@launch
                        }
                        PairingStatus.State.EXPIRED -> {
                            config.clearPairing()
                            onStatus(if (resumed) "" else "Nobody clicked Connect in time, so the request expired. Tap Connect to ask again, or use a setup code from the panel (Devices → Add a device).")
                            return@launch
                        }
                    }
                } catch (error: PairingException) {
                    if (error.status !in RETRYABLE) {
                        config.clearPairing()
                        onStatus(if (resumed) "" else explain(error, serverUrl))
                        return@launch
                    }
                    onStatus("The server is busy (HTTP ${error.status}); still trying…")
                } catch (error: IllegalStateException) {
                    if (error is kotlinx.coroutines.CancellationException) throw error
                    onStatus("Pairing response was invalid; start it again when ready.")
                    return@launch
                } catch (error: Exception) {
                    if (error is kotlinx.coroutines.CancellationException) throw error
                    onStatus("Can't reach the server right now; still trying. Is Tailscale on?")
                }
                delay(POLL_INTERVAL_MS)
            }
        }
    }

    /** What went wrong, in words that say what to do about it. */
    private fun explain(error: Throwable, serverUrl: String): String {
        if (error is PairingException) return when {
            error.code == "invalid_code" -> "That setup code is wrong, already used or expired. Make a new one in the panel: Devices → Add a device."
            error.status == 404 -> "$serverUrl answered, but it does not accept new devices yet: its TELEMETRY_PUBLIC_URL is not set. Running install-server.sh again sets it."
            error.status == 429 -> "Too many tries. Wait ten minutes, then try again."
            error.code == "device_already_registered" -> "The server refused this phone's ID. Remove the old phone in the panel under Devices, then try again."
            else -> "The server refused this (HTTP ${error.status}${error.detail?.let { ": $it" } ?: ""})."
        }
        val message = error.message.orEmpty()
        return when {
            message.contains("changed server") -> "The server calls itself by another address than $serverUrl. Use the exact address from the server setup, or scan the QR in the panel."
            message.contains("private HTTPS origin") -> "That address doesn't look right. It should look like https://server.your-tailnet.ts.net:8443, with nothing after the port."
            error is java.net.UnknownHostException -> "This phone can't find $serverUrl. Turn on Tailscale on this phone, signed in to the same account as the server, then try again."
            error is javax.net.ssl.SSLException -> "The connection to $serverUrl wasn't secure. Check the address, and that the server's HTTPS is on (tailscale serve)."
            error is java.io.IOException -> "Could not reach $serverUrl. Is Tailscale on on this phone? Queued records stay here meanwhile."
            else -> "Connecting failed: ${message.ifBlank { error.javaClass.simpleName }}"
        }
    }

    private fun deviceDisplayName(): String = listOf(Build.MANUFACTURER, Build.MODEL)
        .filter(String::isNotBlank)
        .joinToString(" ")
        .ifBlank { "Android device" }
}
