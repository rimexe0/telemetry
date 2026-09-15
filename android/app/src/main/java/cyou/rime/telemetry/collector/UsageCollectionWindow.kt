package cyou.rime.telemetry.collector

internal object UsageCollectionWindow {
    fun begin(now: Long, lastSuccess: Long): Long =
        if (lastSuccess <= 0L || lastSuccess > now) now - 86_400_000L
        else (lastSuccess - 120_000L).coerceAtLeast(0L)
}
