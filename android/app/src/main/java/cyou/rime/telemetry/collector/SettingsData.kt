package cyou.rime.telemetry.collector

import android.content.Context
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONObject
import java.io.File
import java.time.Instant
import java.time.ZoneId

/** Settings' pages, with the panel's names. Sign-in and the theme are left to the phone. */
enum class SettingsPage(val title: String) {
    BUDGET("Budget and costs"),
    TIMES("Distraction time, bedtime"),
    DISTRACTIONS("What counts as a distraction"),
    LOCK("Settings lock"),
    DEVICES("Devices"),
    SOURCES("Data sources"),
}

/** A setup code for a new device, from POST /v1/pairing/invites. */
data class SetupCode(val code: String, val setupLink: String, val joinUrl: String, val qrPng: ByteArray?, val expiresAt: Instant?)

/**
 * Settings' answers and changes. Every value shows from the copy saved on
 * the phone at once. A change is saved on the phone first ([SettingsQueue])
 * and sent to the same owner route the panel uses; the server decides, so a
 * loosening waits for the next day and the settings lock refuses it without
 * the PIN, and its words are shown as they come.
 */
class SettingsData(private val context: Context, private val scope: CoroutineScope, private val zone: ZoneId = ZoneId.systemDefault()) {
    var page by mutableStateOf<SettingsPage?>(null)

    var policy by mutableStateOf(Shown<PolicyView>(null, null, Refresh.Never, loading = true))
        private set
    var lock by mutableStateOf(Shown<LockView>(null, null, Refresh.Never, loading = true))
        private set
    var labels by mutableStateOf(Shown<List<LabelView>>(null, null, Refresh.Never, loading = true))
        private set
    var devices by mutableStateOf(Shown<List<DeviceView>>(null, null, Refresh.Never, loading = true))
        private set
    var collection by mutableStateOf(Shown<CollectionView>(null, null, Refresh.Never, loading = true))
        private set

    /** The changes made on the phone the server has not answered yet. */
    var waiting by mutableStateOf<List<SettingsChange>>(emptyList())
        private set
    /** What the server said to the last change of each kind, or to the PIN. */
    val notices = mutableStateMapOf<String, String>()
    var busy by mutableStateOf(false)
        private set
    var setupCode by mutableStateOf<SetupCode?>(null)
        private set

    private val loader = AnswerLoader(context, scope)
    private val queue = SettingsQueue(File(context.filesDir, "settings-queue.json"))
    @Volatile private var flushing = false

    init {
        scope.launch(Dispatchers.IO) {
            val entries = queue.entries()
            withContext(Dispatchers.Main) { waiting = entries }
        }
    }

    fun open(next: SettingsPage?) {
        page = next
        if (next == SettingsPage.DEVICES) setupCode = null
        show()
    }

    /** Shows the saved values, asks for fresh ones behind them, and sends what waits. */
    fun show(force: Boolean = false) {
        loader.load("policy", force, SettingsRules::policy, { policy = it }, { policy.takeIf { it.value != null || it.at != null } }) { OwnerApi.get(context, "/v1/policy/config") }
        loader.load("lock", force, SettingsRules::lock, { lock = it }, { lock.takeIf { it.value != null || it.at != null } }) { OwnerApi.get(context, "/v1/lock") }
        loader.load("labels", force, SettingsRules::labels, { labels = it }, { labels.takeIf { it.value != null || it.at != null } }) { OwnerApi.get(context, "/v1/activity-labels") }
        loader.load("devices", force, SettingsRules::devices, { devices = it }, { devices.takeIf { it.value != null || it.at != null } }) { OwnerApi.get(context, "/v1/devices") }
        loader.load("collection", force, SettingsRules::collection, { collection = it }, { collection.takeIf { it.value != null || it.at != null } }) {
            OwnerApi.get(context, "/v1/collection/health?limit=100&samples=1")
        }
        if (waiting.isNotEmpty()) flush()
    }

    fun saveBudget(numbers: BudgetNumbers) = change(SettingsChange.BUDGET, numbers.toJson(), SettingsPage.BUDGET.title)

    fun saveBedtime(bedtime: Bedtime) = change(SettingsChange.BEDTIME, JSONObject().put("bedtime", bedtime.toJson()), "Bedtime")

    private fun change(kind: String, payload: JSONObject, label: String) {
        notices.remove(kind)
        scope.launch {
            val entries = withContext(Dispatchers.IO) {
                queue.add(kind, payload, label)
                queue.entries()
            }
            waiting = entries
            flush()
        }
    }

    /** Sends the waiting changes; one that can't be sent now stays for the next try. */
    fun flush() {
        if (flushing) return
        flushing = true
        scope.launch {
            val results = withContext(Dispatchers.IO) { runCatching { queue.flush(::send) }.getOrDefault(emptyList()) }
            for ((change, outcome) in results) {
                when (outcome) {
                    is SendOutcome.Sent -> notices[change.kind] = outcome.notice
                    is SendOutcome.Refused -> notices[change.kind] = outcome.message
                    SendOutcome.Unreachable -> Unit
                }
            }
            waiting = withContext(Dispatchers.IO) { queue.entries() }
            flushing = false
            if (results.any { it.second !is SendOutcome.Unreachable }) show(force = true)
        }
    }

    private fun outcomeOf(answer: RawAnswer?): SendOutcome? = when {
        answer == null || answer.status >= 500 -> SendOutcome.Unreachable
        !answer.ok -> SendOutcome.Refused(answer.message())
        else -> null
    }

    /** Blocking IO. */
    private fun send(change: SettingsChange): SendOutcome = when (change.kind) {
        SettingsChange.BUDGET -> {
            // The budget goes into the server's config as it is now, so
            // nothing else in it is changed back by a copy saved earlier.
            val current = OwnerApi.get(context, "/v1/policy/config")
            outcomeOf(current) ?: run {
                val config = runCatching { JSONObject(current!!.text).optJSONObject("policy")?.optJSONObject("config") }.getOrNull() ?: JSONObject()
                config.put("budget", change.payload)
                val body = JSONObject().put("config", config).put("note", "Distraction budget set in the phone app").toString()
                val answer = OwnerApi.send(context, "PUT", "/v1/policy/config", body)
                outcomeOf(answer) ?: run {
                    AnswerStore.of(context).put("policy", answer!!.text)
                    val saved = SettingsRules.policy(answer.text)
                    val from = saved?.budgetNextFrom?.takeIf { saved.budgetNext != null && !SettingsRules.started(it, Instant.now(), saved.zone) }
                    SendOutcome.Sent(if (from != null) "Saved. It starts at ${SettingsRules.waitStart(from, zone)}." else "Saved.")
                }
            }
        }
        SettingsChange.BEDTIME -> {
            val answer = OwnerApi.send(context, "PUT", "/v1/lock", change.payload.toString())
            outcomeOf(answer) ?: run {
                AnswerStore.of(context).put("lock", answer!!.text)
                val saved = SettingsRules.lock(answer.text)
                val from = saved?.bedtimeNextFrom?.takeIf { saved.bedtimeNext != null && !SettingsRules.started(it, Instant.now(), zone) }
                SendOutcome.Sent(if (from != null) "Saved. It starts at ${SettingsRules.waitStart(from, zone)}." else "Saved.")
            }
        }
        else -> SendOutcome.Refused("This change can't be sent from this version of the app.")
    }

    /** The PIN opens the rules for a while; it needs the server then and there, so it never waits. */
    fun unlock(pin: String) {
        busy = true
        notices.remove("pin")
        scope.launch {
            val answer = withContext(Dispatchers.IO) { OwnerApi.send(context, "POST", "/v1/lock/unlock", JSONObject().put("pin", pin).toString()) }
            notices["pin"] = when {
                answer == null -> "The server can't be reached, so the PIN can't be checked now."
                answer.ok -> runCatching { JSONObject(answer.text).optString("unlocked_until") }.getOrNull()
                    ?.let { until -> runCatching { java.time.OffsetDateTime.parse(until).toInstant() }.getOrNull() }
                    ?.let { "Unlocked until ${BudgetText.clock(it, zone)}." } ?: "Unlocked."
                else -> answer.message().removePrefix("blocking settings are locked: ")
            }
            busy = false
            show(force = true)
        }
    }

    /** A setup code for a new device, made by the server as the panel's Add a device does. */
    fun addDevice() {
        busy = true
        notices.remove("devices")
        setupCode = null
        scope.launch {
            val answer = withContext(Dispatchers.IO) { OwnerApi.send(context, "POST", "/v1/pairing/invites", "{}") }
            when {
                answer == null -> notices["devices"] = "The server can't be reached, so a setup code can't be made now."
                !answer.ok -> notices["devices"] = answer.message()
                else -> setupCode = runCatching {
                    val json = JSONObject(answer.text)
                    val qr = json.optString("qr").substringAfter("base64,", "").takeIf { it.isNotEmpty() }?.let { android.util.Base64.decode(it, android.util.Base64.DEFAULT) }
                    SetupCode(json.optString("code"), json.optString("setup_link"), json.optString("join_url"), qr,
                        json.optString("expires_at").takeIf { it.isNotBlank() }?.let { runCatching { java.time.OffsetDateTime.parse(it).toInstant() }.getOrNull() })
                }.getOrNull()
            }
            busy = false
        }
    }
}
