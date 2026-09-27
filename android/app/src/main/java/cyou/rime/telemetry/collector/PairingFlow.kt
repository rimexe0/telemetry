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
 * Pairs this phone through the owner's Clerk sign-in in the browser, then polls
 * until the server approves. Progress goes to [onStatus]; [onPaired] runs once
 * the credential is stored and delivery is scheduled.
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

    fun start(serverUrl: String, openBrowser: Boolean = true) {
        val config = DeliveryConfig(context)
        if (serverUrl.isBlank()) {
            onStatus("Enter the private Telemetry server URL first.")
            return
        }
        job?.cancel()
        val secret = try {
            config.newPairingSecret(serverUrl)
        } catch (error: Exception) {
            onStatus("Finish the pending connection before changing servers.")
            return
        }
        onStatus("Creating a secure pairing request…")
        scope.launch {
            try {
                val retainedId = config.pairingId
                val start = if (retainedId != null) {
                    PairingStart(retainedId, Uri.parse(serverUrl.trimEnd('/') + "/panel/").buildUpon().appendQueryParameter("pair", retainedId).build().toString())
                } else withContext(Dispatchers.IO) {
                    client(serverUrl).start(secret)
                }
                config.savePairing(start.id, secret)
                approvalUrl = start.panelUrl
                if (openBrowser) {
                    context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(start.panelUrl)).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
                    onStatus("Sign in in the browser and tap Connect, then come back here.")
                } else {
                    onStatus("Now open the Telemetry panel on your Mac and click Connect on the \"wants to connect\" banner. This screen moves on by itself.")
                }
                poll(serverUrl, start.id, secret)
            } catch (error: Exception) {
                if (error is kotlinx.coroutines.CancellationException) throw error
                onStatus("Could not reach the server. Is Tailscale on? Queued records stay on this phone.")
            }
        }
    }

    /** Continues a pairing that was started before the app was left. */
    fun resume() {
        val config = DeliveryConfig(context)
        val pairingId = config.pairingId ?: return
        val secret = config.pairingSecret() ?: return
        if (job?.isActive == true) return
        poll(config.pairingServer() ?: return, pairingId, secret, resumed = true)
    }

    private fun client(serverUrl: String) = TelemetryPairingClient(
        serverUrl = serverUrl,
        deviceId = DeviceIdentity.get(context.applicationContext),
        deviceName = deviceDisplayName(),
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
                        PairingStatus.State.PENDING -> onStatus("Waiting for you to approve in the browser…")
                        PairingStatus.State.APPROVED -> {
                            if (result.deviceId != DeviceIdentity.get(context.applicationContext)) {
                                throw IllegalStateException("pairing approval was for another device")
                            }
                            config.completePairing(serverUrl, secret)
                            BackgroundJobs.scheduleDelivery(context)
                            onStatus("Phone connected.")
                            onPaired()
                            return@launch
                        }
                        PairingStatus.State.DENIED -> {
                            config.clearPairing()
                            onStatus("Pairing was denied. Start it again when ready.")
                            return@launch
                        }
                        PairingStatus.State.EXPIRED -> {
                            config.clearPairing()
                            onStatus(if (resumed) "" else "Pairing expired. Tap Connect to try again.")
                            return@launch
                        }
                    }
                } catch (error: PairingException) {
                    if (error.status !in RETRYABLE) {
                        config.clearPairing()
                        onStatus(if (resumed) "" else "Pairing could not be continued; start it again when ready.")
                        return@launch
                    }
                    onStatus("Waiting for the private Telemetry server…")
                } catch (error: IllegalStateException) {
                    if (error is kotlinx.coroutines.CancellationException) throw error
                    onStatus("Pairing response was invalid; start it again when ready.")
                    return@launch
                } catch (error: Exception) {
                    if (error is kotlinx.coroutines.CancellationException) throw error
                    onStatus("Waiting for the private Telemetry server…")
                }
                delay(POLL_INTERVAL_MS)
            }
        }
    }

    private fun deviceDisplayName(): String = listOf(Build.MANUFACTURER, Build.MODEL)
        .filter(String::isNotBlank)
        .joinToString(" ")
        .ifBlank { "Android device" }
}
