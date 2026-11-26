package cyou.rime.telemetry.collector

import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import org.json.JSONObject
import java.time.Instant

/**
 * The names the phone shows for the apps it sees, sent to the server as an
 * `android.app_labels` observation when one is new or changed, so Activity
 * can show "WhatsApp" next to com.whatsapp. The package stays the identity.
 */
object AppLabels {
    private const val PREFS = "telemetry-app-labels"
    private const val KEY_SENT = "sent"
    const val KIND = "android.app_labels"

    /** The labels in [seen] that were never sent or have changed since. */
    fun changed(sent: Map<String, String>, seen: Map<String, String>): Map<String, String> =
        seen.filter { (pkg, label) -> pkg.isNotBlank() && label.isNotBlank() && sent[pkg] != label }.toSortedMap()

    fun encode(labels: Map<String, String>): String = JSONObject(labels).toString()

    fun decode(json: String?): Map<String, String> {
        val root = json?.let { runCatching { JSONObject(it) }.getOrNull() } ?: return emptyMap()
        return root.keys().asSequence().associateWith { root.optString(it) }.filterValues { it.isNotBlank() }
    }

    /** The observation for [labels]: `{"labels": {package: label}}`. */
    fun observation(deviceId: String, labels: Map<String, String>, now: Instant): LocalObservation {
        val payload = JSONObject().put("labels", JSONObject(labels))
        return LocalObservation(
            sourceInstanceKey = "android:$deviceId:apps",
            sourceKind = "android_apps",
            deviceId = deviceId,
            sourceRecordKey = "labels:${now.toEpochMilli()}",
            kind = KIND,
            recordType = "app_labels",
            occurredAt = now.toString(),
            endedAt = null,
            collectedAt = now.toString(),
            sourceSchemaVersion = "android-app-labels-v1",
            payloadJson = payload.toString(),
        )
    }

    /**
     * The labels to send for the [packages] seen in this cycle, or null when
     * none changed. The first time, every app with a launcher icon is
     * included too, so apps seen before this was sent get their names.
     */
    fun pending(context: Context, packages: Set<String>): Map<String, String>? {
        val sent = decode(context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).getString(KEY_SENT, null))
        val wanted = if (sent.isEmpty()) packages + launcherPackages(context) else packages
        val manager = context.packageManager
        val seen = wanted.mapNotNull { pkg -> label(manager, pkg)?.let { pkg to it } }.toMap()
        return changed(sent, seen).takeIf { it.isNotEmpty() }
    }

    /** Remembers what was queued, so it is not sent again. */
    fun markSent(context: Context, labels: Map<String, String>) {
        val preferences = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        val sent = decode(preferences.getString(KEY_SENT, null)) + labels
        preferences.edit().putString(KEY_SENT, encode(sent)).commit()
    }

    private fun label(manager: PackageManager, pkg: String): String? = runCatching {
        manager.getApplicationLabel(manager.getApplicationInfo(pkg, 0)).toString().trim()
    }.getOrNull()?.takeIf { it.isNotBlank() && it != pkg }

    private fun launcherPackages(context: Context): Set<String> = runCatching {
        context.packageManager.queryIntentActivities(Intent(Intent.ACTION_MAIN).addCategory(Intent.CATEGORY_LAUNCHER), 0)
            .map { it.activityInfo.packageName }.toSet()
    }.getOrDefault(emptySet())
}
