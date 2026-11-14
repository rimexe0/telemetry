package cyou.rime.telemetry.collector

import android.content.Context
import java.security.MessageDigest
import java.security.SecureRandom
import java.time.Duration
import java.time.Instant
import java.util.Base64
import javax.crypto.SecretKeyFactory
import javax.crypto.spec.PBEKeySpec

/**
 * Turning a block off by hand needs the password its holder keeps, and cannot
 * be done in a moment: the first try only starts a wait, and the password is
 * asked after it. Nothing here runs in the background.
 */
object UnlockGate {
    val WAIT: Duration = Duration.ofMinutes(30)
    private const val PREFS = "telemetry-unlock"
    private const val KEY_SALT = "salt"
    private const val KEY_HASH = "hash"
    private const val KEY_PREFIX = "waiting_"
    private const val ITERATIONS = 120_000

    /** Every way of turning blocking off by hand goes through the same gate. */
    enum class Action(val label: String) {
        SUSPENSION("Gray out blocked apps"),
        WATCHER("Keep blocking enforced"),
        PASSWORD("Change the unlock password"),
    }

    /** What the next tap should show. */
    enum class Step { NO_PASSWORD, START, WAIT, PASSWORD }

    fun step(now: Instant, armed: Boolean, waitingUntil: Instant?): Step = when {
        !armed -> Step.NO_PASSWORD
        waitingUntil == null -> Step.START
        now.isBefore(waitingUntil) -> Step.WAIT
        else -> Step.PASSWORD
    }

    fun armed(context: Context): Boolean = prefs(context).contains(KEY_HASH)

    /** Stores only a salted hash; the password itself is not kept. */
    fun setPassword(context: Context, password: String): Boolean {
        if (password.length < 8) return false
        val salt = ByteArray(16).also { SecureRandom().nextBytes(it) }
        val digest = hash(password, salt, ITERATIONS)
        prefs(context).edit()
            .putString(KEY_SALT, Base64.getEncoder().encodeToString(salt))
            .putString(KEY_HASH, Base64.getEncoder().encodeToString(digest))
            .apply()
        return true
    }

    fun verify(context: Context, password: String): Boolean {
        val preferences = prefs(context)
        val salt = preferences.getString(KEY_SALT, null)?.let { Base64.getDecoder().decode(it) } ?: return false
        val stored = preferences.getString(KEY_HASH, null)?.let { Base64.getDecoder().decode(it) } ?: return false
        return MessageDigest.isEqual(hash(password, salt, ITERATIONS), stored)
    }

    fun waitingUntil(context: Context, action: Action): Instant? =
        prefs(context).getLong(KEY_PREFIX + action.name, 0L).takeIf { it > 0 }?.let(Instant::ofEpochMilli)

    fun startWaiting(context: Context, action: Action, now: Instant = Instant.now()): Instant {
        val until = now.plus(WAIT)
        prefs(context).edit().putLong(KEY_PREFIX + action.name, until.toEpochMilli()).apply()
        return until
    }

    /** The wait is spent once the change is made. */
    fun used(context: Context, action: Action) {
        prefs(context).edit().remove(KEY_PREFIX + action.name).apply()
    }

    internal fun hash(password: String, salt: ByteArray, iterations: Int): ByteArray {
        val spec = PBEKeySpec(password.toCharArray(), salt, iterations, 256)
        return SecretKeyFactory.getInstance("PBKDF2WithHmacSHA256").generateSecret(spec).encoded
    }

    private fun prefs(context: Context) = context.applicationContext.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
}
