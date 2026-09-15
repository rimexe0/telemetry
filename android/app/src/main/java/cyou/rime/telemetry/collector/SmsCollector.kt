package cyou.rime.telemetry.collector

import android.Manifest
import android.content.ContentResolver
import android.content.Context
import android.content.pm.PackageManager
import android.os.Bundle
import android.os.CancellationSignal
import android.os.OperationCanceledException
import android.provider.BaseColumns
import android.provider.Telephony
import androidx.core.content.ContextCompat
import java.time.Instant
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import kotlinx.coroutines.CancellationException
import org.json.JSONObject

data class SmsRecord(
    val id: Long,
    val threadId: Long?,
    val address: String?,
    val dateMillis: Long,
    val dateSentMillis: Long?,
    val type: Int,
    val status: Int?,
    val read: Int?,
    val seen: Int?,
    val subscriptionId: Int?,
    val body: String?,
)

enum class SmsQueryMode {
    HISTORY,
    RECENT_REFRESH,
}

data class SmsQuery(
    val mode: SmsQueryMode,
    val afterDateMillis: Long? = null,
    val afterId: Long? = null,
    val lowerBoundDateMillis: Long = 0L,
    val limit: Int = SmsCollector.MAX_HISTORY_ROWS,
)

data class SmsPage(
    val records: List<SmsRecord>,
    val hasMore: Boolean,
    val invalidRows: Int = 0,
)

interface SmsSource {
    /** Returns null when the provider cannot be queried. */
    fun query(query: SmsQuery): SmsPage?
}

interface SmsCursorStore {
    fun load(): SmsCursor
    fun save(cursor: SmsCursor): Boolean
}

data class SmsCursor(
    val lastDateMillis: Long = SMS_NO_DATE_MILLIS,
    val lastId: Long = 0L,
    val historyComplete: Boolean = false,
) {
    fun normalized(): SmsCursor = copy(
        lastId = lastId.coerceAtLeast(0L),
    )
}

internal const val SMS_NO_DATE_MILLIS = Long.MIN_VALUE

data class SmsCollectionResult(
    val recordsRead: Int,
    val insertedRevisions: Int,
    val state: String,
    val historyComplete: Boolean,
    val invalidRows: Int = 0,
)

internal object SmsCollectionState {
    const val DISABLED = "disabled"
    const val PERMISSION_REQUIRED = "permission_required"
    const val PROVIDER_UNAVAILABLE = "provider_unavailable"
    const val EMPTY_INITIAL = "empty_initial"
    const val UNCHANGED = "unchanged"
    const val OK = "ok"
    const val PARTIAL = "partial"
    const val INVALID_ROWS = "invalid_rows"
    const val INVALID_DATE = "invalid_date"
    const val CURSOR_WRITE_ERROR = "cursor_write_error"
    const val QUERY_TIMEOUT = "query_timeout"
    const val ERROR = "error"
}

internal class SmsQueryTimeoutException : IllegalStateException("SMS provider query timed out")

internal class SmsInvalidDateException : IllegalStateException(
    "SMS provider returned a null receive timestamp",
)

internal fun smsErrorState(error: Throwable): String = when (error) {
    is SmsQueryTimeoutException -> SmsCollectionState.QUERY_TIMEOUT
    is SmsInvalidDateException -> SmsCollectionState.INVALID_DATE
    is SecurityException -> SmsCollectionState.PERMISSION_REQUIRED
    else -> SmsCollectionState.ERROR
}

internal fun smsAccessState(
    enabled: Boolean,
    permissionGranted: Boolean,
    telephonyAvailable: Boolean,
): String = when {
    !enabled -> SmsCollectionState.DISABLED
    !permissionGranted -> SmsCollectionState.PERMISSION_REQUIRED
    !telephonyAvailable -> SmsCollectionState.PROVIDER_UNAVAILABLE
    else -> SmsCollectionState.OK
}

/**
 * Bounded SMS inbox synchronization. The strict history cursor catches old
 * rows over multiple cycles. The recent refresh catches changed read, seen or
 * delivery fields without relying on a broadcast receiver.
 */
internal class SmsSync(
    private val source: SmsSource,
    private val cursorStore: SmsCursorStore,
    private val deviceId: String = "test-device",
    private val enqueue: (List<LocalObservation>) -> Int,
) {
    fun collect(nowMillis: Long): SmsCollectionResult {
        val cursor = cursorStore.load().normalized()
        val historyPage = source.query(historyQuery(cursor))
            ?: return SmsCollectionResult(0, 0, SmsCollectionState.PROVIDER_UNAVAILABLE, cursor.historyComplete)
        val refreshPage = source.query(recentRefreshQuery(nowMillis))
            ?: return SmsCollectionResult(0, 0, SmsCollectionState.PROVIDER_UNAVAILABLE, cursor.historyComplete)

        // The refresh query runs second. Prefer that row if a message changed
        // between the two provider reads, while keeping the queue key stable.
        val records = linkedMapOf<Long, SmsRecord>()
        historyPage.records.forEach { records[it.id] = it }
        refreshPage.records.forEach { records[it.id] = it }
        val observations = records.values.map { observation(it, nowMillis) }
        val inserted = enqueue(observations)
        val invalidRows = historyPage.invalidRows + refreshPage.invalidRows

        val nextCursor = cursorAfter(cursor, historyPage)
        if (!cursorStore.save(nextCursor)) {
            return SmsCollectionResult(
                recordsRead = records.size,
                insertedRevisions = inserted,
                state = SmsCollectionState.CURSOR_WRITE_ERROR,
                historyComplete = cursor.historyComplete,
                invalidRows = invalidRows,
            )
        }
        val state = when {
            invalidRows > 0 -> SmsCollectionState.INVALID_ROWS
            historyPage.hasMore || refreshPage.hasMore -> SmsCollectionState.PARTIAL
            records.isEmpty() && !cursor.historyComplete -> SmsCollectionState.EMPTY_INITIAL
            records.isEmpty() -> SmsCollectionState.UNCHANGED
            inserted == 0 -> SmsCollectionState.UNCHANGED
            else -> SmsCollectionState.OK
        }
        return SmsCollectionResult(records.size, inserted, state, nextCursor.historyComplete, invalidRows)
    }

    private fun historyQuery(cursor: SmsCursor): SmsQuery = SmsQuery(
        mode = SmsQueryMode.HISTORY,
        afterDateMillis = cursor.lastDateMillis.takeIf {
            cursor.lastDateMillis != SMS_NO_DATE_MILLIS || cursor.lastId > 0L
        },
        afterId = cursor.lastId.takeIf { cursor.lastId > 0L },
        lowerBoundDateMillis = SMS_NO_DATE_MILLIS,
    )

    private fun recentRefreshQuery(nowMillis: Long): SmsQuery = SmsQuery(
        mode = SmsQueryMode.RECENT_REFRESH,
        lowerBoundDateMillis = (nowMillis - SmsCollector.RECENT_REFRESH_MILLIS).coerceAtLeast(0L),
    )

    private fun cursorAfter(cursor: SmsCursor, page: SmsPage): SmsCursor {
        val last = page.records.lastOrNull() ?: return cursor.copy(historyComplete = !page.hasMore)
        val advanced = if (last.dateMillis > cursor.lastDateMillis ||
            (last.dateMillis == cursor.lastDateMillis && last.id > cursor.lastId)
        ) {
            SmsCursor(last.dateMillis, last.id, historyComplete = !page.hasMore)
        } else {
            cursor.copy(historyComplete = !page.hasMore)
        }
        return advanced
    }

    private fun observation(record: SmsRecord, nowMillis: Long): LocalObservation {
        val occurredAt = Instant.ofEpochMilli(record.dateMillis).toString()
        val collectedAt = Instant.ofEpochMilli(nowMillis).toString()
        val payload = JSONObject()
            .put("provider", "android.telephony.sms.inbox")
            .put("row_id", record.id)
            .putNullable("thread_id", record.threadId)
            .putNullable("address", record.address)
            .put("date_millis", record.dateMillis)
            .put("date", occurredAt)
            .putNullable(
                "date_sent_millis",
                record.dateSentMillis,
            )
            .putNullable(
                "date_sent",
                record.dateSentMillis?.let { Instant.ofEpochMilli(it).toString() },
            )
            .put("type", record.type)
            .putNullable("status", record.status)
            .putNullable("read", record.read)
            .putNullable("seen", record.seen)
            .putNullable("subscription_id", record.subscriptionId)
            .putNullable("body", record.body)
        return LocalObservation(
            sourceInstanceKey = "android:$deviceId:sms",
            sourceKind = "android_sms",
            deviceId = deviceId,
            sourceRecordKey = "sms:${record.id}:${record.dateMillis}",
            kind = "android.sms.inbox",
            recordType = "sms_inbox",
            occurredAt = occurredAt,
            endedAt = null,
            collectedAt = collectedAt,
            sourceSchemaVersion = "android-telephony-sms-inbox-api-36",
            payloadJson = payload.toString(),
        )
    }

    private fun JSONObject.putNullable(key: String, value: Any?): JSONObject = put(
        key,
        value ?: JSONObject.NULL,
    )
}

private class SharedPreferencesSmsCursorStore(context: Context) : SmsCursorStore {
    private val preferences = context.applicationContext.getSharedPreferences(
        SmsCollectionSettings.PREFERENCES_NAME,
        Context.MODE_PRIVATE,
    )

    override fun load(): SmsCursor = SmsCursor(
        lastDateMillis = preferences.getLong(KEY_LAST_DATE, SMS_NO_DATE_MILLIS),
        lastId = preferences.getLong(KEY_LAST_ID, 0L),
        historyComplete = preferences.getBoolean(KEY_HISTORY_COMPLETE, false),
    )

    override fun save(cursor: SmsCursor): Boolean = preferences.edit()
        .putLong(KEY_LAST_DATE, cursor.lastDateMillis)
        .putLong(KEY_LAST_ID, cursor.lastId)
        .putBoolean(KEY_HISTORY_COMPLETE, cursor.historyComplete)
        .commit()

    private companion object {
        const val KEY_LAST_DATE = "history-last-date-millis"
        const val KEY_LAST_ID = "history-last-id"
        const val KEY_HISTORY_COMPLETE = "history-complete"
    }
}

private class AndroidSmsSource(context: Context) : SmsSource {
    private val resolver: ContentResolver = context.applicationContext.contentResolver

    override fun query(query: SmsQuery): SmsPage? {
        val selectionArgs = mutableListOf<String>()
        val selection = buildString {
            append("${Telephony.TextBasedSmsColumns.TYPE} = ?")
            selectionArgs += Telephony.TextBasedSmsColumns.MESSAGE_TYPE_INBOX.toString()
            if (query.mode == SmsQueryMode.HISTORY && query.afterDateMillis != null && query.afterId != null) {
                append(
                    " AND (${Telephony.TextBasedSmsColumns.DATE} > ? OR " +
                        "(${Telephony.TextBasedSmsColumns.DATE} = ? AND ${BaseColumns._ID} > ?))",
                )
                selectionArgs += query.afterDateMillis.toString()
                selectionArgs += query.afterDateMillis.toString()
                selectionArgs += query.afterId.toString()
            } else {
                append(" AND ${Telephony.TextBasedSmsColumns.DATE} >= ?")
                selectionArgs += query.lowerBoundDateMillis.toString()
            }
        }
        val sort = if (query.mode == SmsQueryMode.HISTORY) {
            "${Telephony.TextBasedSmsColumns.DATE} ASC, ${BaseColumns._ID} ASC"
        } else {
            "${Telephony.TextBasedSmsColumns.DATE} DESC, ${BaseColumns._ID} DESC"
        }
        val queryArgs = Bundle().apply {
            putString(ContentResolver.QUERY_ARG_SQL_SELECTION, selection)
            putStringArray(ContentResolver.QUERY_ARG_SQL_SELECTION_ARGS, selectionArgs.toTypedArray())
            putString(ContentResolver.QUERY_ARG_SQL_SORT_ORDER, sort)
            putInt(ContentResolver.QUERY_ARG_LIMIT, query.limit + 1)
        }
        val projection = arrayOf(
            BaseColumns._ID,
            Telephony.TextBasedSmsColumns.THREAD_ID,
            Telephony.TextBasedSmsColumns.ADDRESS,
            Telephony.TextBasedSmsColumns.DATE,
            Telephony.TextBasedSmsColumns.DATE_SENT,
            Telephony.TextBasedSmsColumns.TYPE,
            Telephony.TextBasedSmsColumns.STATUS,
            Telephony.TextBasedSmsColumns.READ,
            Telephony.TextBasedSmsColumns.SEEN,
            Telephony.TextBasedSmsColumns.SUBSCRIPTION_ID,
            Telephony.TextBasedSmsColumns.BODY,
        )
        val rows = mutableListOf<SmsRecord>()
        val cancellationSignal = CancellationSignal()
        val timeout = TIMEOUT_EXECUTOR.schedule(
            { cancellationSignal.cancel() },
            QUERY_TIMEOUT_MILLIS,
            TimeUnit.MILLISECONDS,
        )
        var providerRowsRead = 0
        var invalidRows = 0
        try {
            val cursor = resolver.query(
                Telephony.Sms.Inbox.CONTENT_URI,
                projection,
                queryArgs,
                cancellationSignal,
            ) ?: return null
            try {
                cursor.use { cursor ->
                    val id = cursor.getColumnIndexOrThrow(BaseColumns._ID)
                    val thread = cursor.getColumnIndexOrThrow(Telephony.TextBasedSmsColumns.THREAD_ID)
                    val address = cursor.getColumnIndexOrThrow(Telephony.TextBasedSmsColumns.ADDRESS)
                    val date = cursor.getColumnIndexOrThrow(Telephony.TextBasedSmsColumns.DATE)
                    val dateSent = cursor.getColumnIndexOrThrow(Telephony.TextBasedSmsColumns.DATE_SENT)
                    val type = cursor.getColumnIndexOrThrow(Telephony.TextBasedSmsColumns.TYPE)
                    val status = cursor.getColumnIndexOrThrow(Telephony.TextBasedSmsColumns.STATUS)
                    val read = cursor.getColumnIndexOrThrow(Telephony.TextBasedSmsColumns.READ)
                    val seen = cursor.getColumnIndexOrThrow(Telephony.TextBasedSmsColumns.SEEN)
                    val subscription = cursor.getColumnIndexOrThrow(Telephony.TextBasedSmsColumns.SUBSCRIPTION_ID)
                    val body = cursor.getColumnIndexOrThrow(Telephony.TextBasedSmsColumns.BODY)
                    while (cursor.moveToNext() && providerRowsRead < query.limit + 1) {
                        providerRowsRead++
                        if (cursor.isNull(date)) throw SmsInvalidDateException()
                        val dateMillis = cursor.getLong(date)
                        if (dateMillis < 0L) invalidRows++
                        rows += SmsRecord(
                            id = cursor.getLong(id),
                            threadId = cursor.getLong(thread).takeIf { !cursor.isNull(thread) },
                            address = cursor.getString(address),
                            dateMillis = dateMillis,
                            dateSentMillis = cursor.getLong(dateSent).takeIf { !cursor.isNull(dateSent) },
                            type = cursor.getInt(type),
                            status = cursor.getInt(status).takeIf { !cursor.isNull(status) },
                            read = cursor.getInt(read).takeIf { !cursor.isNull(read) },
                            seen = cursor.getInt(seen).takeIf { !cursor.isNull(seen) },
                            subscriptionId = cursor.getInt(subscription).takeIf { !cursor.isNull(subscription) },
                            body = cursor.getString(body),
                        )
                    }
                }
            } catch (_: OperationCanceledException) {
                throw SmsQueryTimeoutException()
            }
            return SmsPage(rows.take(query.limit), providerRowsRead > query.limit, invalidRows)
        } catch (_: OperationCanceledException) {
            throw SmsQueryTimeoutException()
        } finally {
            timeout.cancel(false)
        }
    }

    private companion object {
        const val QUERY_TIMEOUT_MILLIS = 5_000L
        val TIMEOUT_EXECUTOR = Executors.newSingleThreadScheduledExecutor { runnable ->
            Thread(runnable, "telemetry-sms-query-timeout").apply { isDaemon = true }
        }
    }
}

/** Production adapter that checks opt-in and Android permission before reading. */
class SmsCollector(
    private val context: Context,
    private val queue: HealthQueue,
    private val deviceId: String,
) {
    fun collect(nowMillis: Long = System.currentTimeMillis()): SmsCollectionResult {
        val settings = SmsCollectionSettings(context)
        val accessState = smsAccessState(
            enabled = settings.enabled,
            permissionGranted = ContextCompat.checkSelfPermission(
                context,
                Manifest.permission.READ_SMS,
            ) == PackageManager.PERMISSION_GRANTED,
            telephonyAvailable = context.packageManager.hasSystemFeature(
                PackageManager.FEATURE_TELEPHONY_MESSAGING,
            ),
        )
        when (accessState) {
            SmsCollectionState.DISABLED,
            SmsCollectionState.PERMISSION_REQUIRED,
            SmsCollectionState.PROVIDER_UNAVAILABLE -> return SmsCollectionResult(0, 0, accessState, false)
        }
        return try {
            SmsSync(
                source = AndroidSmsSource(context),
                cursorStore = SharedPreferencesSmsCursorStore(context),
                enqueue = { records -> queue.enqueue(records).insertedRevisions },
                deviceId = deviceId,
            ).collect(nowMillis)
        } catch (error: Exception) {
            if (error is CancellationException) throw error
            SmsCollectionResult(0, 0, smsErrorState(error), false)
        }
    }

    companion object {
        const val MAX_HISTORY_ROWS = 200
        const val RECENT_REFRESH_MILLIS = 7L * 24L * 60L * 60L * 1_000L
    }
}
