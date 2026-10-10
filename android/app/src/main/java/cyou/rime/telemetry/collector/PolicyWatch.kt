package cyou.rime.telemetry.collector

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import java.time.Instant

/**
 * Runs once per collection cycle: refreshes the cached policy and notices when
 * blocking is switched off while the policy is blocking something. One bypass
 * is recorded and asked about per episode.
 */
object PolicyWatch {
    private const val PREFS = "telemetry-policy-watch"
    private const val KEY_BYPASS_SINCE = "bypass_since"
    private const val CHANNEL = "telemetry-checkins"
    private const val NOTIFICATION_ID = 4201

    // Updates and reboots delete the app's notifications. The first check in a
    // new process asks again about a bypass that is still open; later checks
    // leave a dismissed question alone.
    @Volatile private var askedThisProcess = false

    /** With fetch false, applies the cached answer as it is, such as one that just arrived. */
    fun check(context: Context, fetch: Boolean = true) {
        if (fetch) NowCache.refresh(context)
        val entry = NowCache.read(context)
        IncidentWatch.check(context, entry?.json)
        runCatching { PlaceReminders.evaluate(context, notify = false) }
        runCatching { FollowUpWatch.check(context, entry?.json) }
        runCatching { TurnWatch.check(context, entry?.json) }
        val decision = entry?.let { BlockPolicy.current(context, it.json) }
        // Optional root suspension; lifts everything when nothing should be blocked.
        runCatching { AppSuspender.reconcile(context, decision) }
        runCatching { TelemetryNotificationListenerService.connected?.quietBlocked() }
        val bypassed = decision?.active == true && !PolicyEvents.blockingEnabled(context)
        val preferences = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        val openSince = preferences.getString(KEY_BYPASS_SINCE, null)
        when {
            bypassed && openSince == null -> {
                val since = Instant.now().toString()
                PolicyEvents.record(context, "android.policy_bypass", "bypass:$since", decision, mapOf("cause" to "blocking_turned_off"))
                preferences.edit().putString(KEY_BYPASS_SINCE, since).apply()
                notify(context, decision!!)
            }
            bypassed && !askedThisProcess -> notify(context, decision!!)
            !bypassed && openSince != null -> {
                PolicyEvents.record(context, "android.policy_bypass_end", "bypass-end:$openSince", decision, mapOf("started_at" to openSince))
                preferences.edit().remove(KEY_BYPASS_SINCE).apply()
                context.getSystemService(NotificationManager::class.java).cancel(NOTIFICATION_ID)
            }
        }
    }

    private fun notify(context: Context, decision: BlockDecision) {
        askedThisProcess = true
        val manager = context.getSystemService(NotificationManager::class.java)
        manager.createNotificationChannel(NotificationChannel(CHANNEL, "Check-ins", NotificationManager.IMPORTANCE_DEFAULT))
        val open = PendingIntent.getActivity(context, 2, Intent(context, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE)
        manager.notify(NOTIFICATION_ID, Notification.Builder(context, CHANNEL)
            .setSmallIcon(android.R.drawable.ic_dialog_info)
            .setContentTitle("Blocking is switched off")
            .setContentText("${decision.reason} What's going on?")
            .setStyle(Notification.BigTextStyle().bigText("${decision.reason} Blocking was switched off. What's going on?"))
            .setContentIntent(open)
            .setAutoCancel(true)
            .build())
    }
}
