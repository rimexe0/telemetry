package cyou.rime.telemetry.collector

import android.accessibilityservice.AccessibilityService
import android.database.ContentObserver
import android.graphics.Color
import android.graphics.PixelFormat
import android.graphics.Typeface
import android.graphics.drawable.GradientDrawable
import android.os.Handler
import android.os.Looper
import android.os.PowerManager
import android.provider.Settings
import android.util.Log
import android.view.Gravity
import android.view.View
import android.view.WindowManager
import android.view.accessibility.AccessibilityEvent
import android.widget.Button
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.TextView
import java.time.Duration
import java.time.Instant
import java.time.ZoneId

/**
 * Stage-1 blocking (#20). When the approved policy blocks the app coming to the
 * foreground, a full-screen reason covers it and the owner is sent home. Every
 * block is recorded. It follows the cached server timeline, so it keeps working
 * offline, and it never blocks when that cache is missing or out of date.
 *
 * With the budget on (2026-11-26) the block screen is also the door: what
 * opening costs now, the habits, the owner's reasons, Back and Still want it.
 * Two small overlays come from here too, never taking input: the counter in a
 * corner while a distraction is in front and open, and the bedtime chip over
 * every app during the wind-down. The grayscale notice is the one small
 * overlay that takes a tap.
 */
class TelemetryAccessibilityService : AccessibilityService() {
    companion object {
        private const val TAG = "TelemetryBlocking"
        private const val TICK_MS = 5_000L
        /** An unanswered grayscale notice switches grayscale back, as before. */
        private const val NOTICE_TIMEOUT_MS = 60_000L
        /** Older figures on the door screen say when they are from. */
        private val STALE: Duration = Duration.ofMinutes(15)

        /** The running service, so the grayscale check can ask through it. */
        @Volatile var connected: TelemetryAccessibilityService? = null
    }

    private val main = Handler(Looper.getMainLooper())
    private val zone: ZoneId get() = ZoneId.systemDefault()

    private var overlay: ScrollView? = null
    private var overlayKey = ""
    private var waitText: TextView? = null
    private var blockedPackage = ""
    private var corner: TextView? = null
    private var notice: LinearLayout? = null
    private var noticeShown = false

    private var systemSafe: Set<String> = emptySet()
    private var cached: Triple<Instant, String, Set<String>>? = null
    private var cachedBudget: Pair<String, BudgetView?>? = null
    private var lastRecorded = ""
    private var foreground = ""
    private val sitting = Sitting()

    private var doorBusy = false
    private var doorMessage: String? = null
    private var reconciledDoor = 0L
    private var ticking = false
    private var grayscaleObserver: ContentObserver? = null

    override fun onServiceConnected() {
        super.onServiceConnected()
        connected = this
        systemSafe = PolicyEvents.systemSafe(this)
        // Switching grayscale off in quick settings is seen at once, not at the next app switch.
        grayscaleObserver = object : ContentObserver(main) {
            override fun onChange(selfChange: Boolean) {
                runCatching { Grayscale.reconcile(this@TelemetryAccessibilityService, currentDecision()) }
            }
        }.also { observer ->
            runCatching { contentResolver.registerContentObserver(Settings.Secure.getUriFor(Grayscale.ENABLED), false, observer) }
        }
        // Android reconnects this service after updates and reboots.
        CollectionStarter.ensureRunning(this)
    }

    override fun onAccessibilityEvent(event: AccessibilityEvent) {
        if (event.eventType != AccessibilityEvent.TYPE_WINDOW_STATE_CHANGED) return
        val pkg = event.packageName?.toString().orEmpty()
        if (pkg.isBlank() || pkg == "com.android.systemui") return
        val decision = currentDecision()
        // The wind-down turns the screen gray on time, at the next app switch.
        runCatching { Grayscale.reconcile(this, decision) }
        // Keyboards, popups and toasts are not full screen; the app is still
        // underneath them. This app's own overlays are not an app in front.
        val ownOverlay = pkg == packageName && event.className?.toString()?.endsWith("Activity") != true
        if (event.isFullScreen && !ownOverlay) foreground = pkg
        if (!BlockPolicy.shouldBlock(decision, pkg, systemSafe)) {
            if (pkg != packageName && event.isFullScreen) removeOverlay()
            refreshCorner()
            return
        }
        block(pkg, decision!!)
    }

    private fun block(pkg: String, decision: BlockDecision) {
        Log.i(TAG, "blocking $pkg (${decision.mode})")
        if (blockedPackage != pkg) {
            doorMessage = null
            blockedPackage = pkg
        }
        showOverlay(decision)
        refreshCorner()
        val key = "block:$pkg:${Instant.now().epochSecond / 60}"
        if (key != lastRecorded) {
            lastRecorded = key
            Thread { runCatching { PolicyEvents.record(this, "android.policy_block", key, decision, mapOf("package" to pkg)) } }.start()
        }
    }

    /** The cached answer, when it was fetched, and the distractions it names; parsed once per answer. */
    private fun entry(): Triple<Instant, String, Set<String>>? {
        val entry = NowCache.read(this) ?: return null
        // An offline door changes the budget without a new fetch.
        cached?.takeIf { it.first == entry.fetchedAt && it.second == entry.json }?.let { return it }
        return Triple(entry.fetchedAt, entry.json, BlockPolicy.distractionPackages(entry.json)).also { cached = it }
    }

    private fun currentDecision(): BlockDecision? = entry()?.let { BlockPolicy.current(this, it.second) }

    /** The cached budget when it is on; parsed once per answer. */
    private fun budget(): BudgetView? {
        val json = entry()?.second ?: return null
        cachedBudget?.takeIf { it.first === json }?.let { return it.second }
        return Budget.parse(json)?.takeIf { it.enabled }.also { cachedBudget = json to it }
    }

    override fun onInterrupt() = removeAll()

    override fun onDestroy() {
        if (connected === this) connected = null
        grayscaleObserver?.let { runCatching { contentResolver.unregisterContentObserver(it) } }
        removeAll()
        super.onDestroy()
    }

    private fun removeAll() {
        removeOverlay()
        removeCorner()
        removeNotice()
    }

    // The block screen, which is the door while the budget is on.

    private val dim = Color.rgb(170, 169, 164)
    private val bright = Color.rgb(238, 238, 235)

    private fun showOverlay(decision: BlockDecision) {
        val entry = entry()
        val key = listOf(decision.mode, decision.reason, entry?.first, entry?.second?.hashCode(), doorMessage, doorBusy, blockedPackage).joinToString("|")
        if (overlay != null && key == overlayKey) return
        overlayKey = key
        val existing = overlay
        val scroll = existing ?: ScrollView(this).apply {
            isFillViewport = true
            setBackgroundColor(Color.rgb(17, 18, 19))
        }
        scroll.removeAllViews()
        scroll.addView(screen(decision, budget(), entry?.first))
        if (existing != null) return
        val params = WindowManager.LayoutParams(
            WindowManager.LayoutParams.MATCH_PARENT,
            WindowManager.LayoutParams.MATCH_PARENT,
            WindowManager.LayoutParams.TYPE_ACCESSIBILITY_OVERLAY,
            WindowManager.LayoutParams.FLAG_LAYOUT_IN_SCREEN or WindowManager.LayoutParams.FLAG_LAYOUT_NO_LIMITS,
            PixelFormat.OPAQUE,
        ).apply {
            // Cover the status bar and cutout too, so nothing of the blocked app shows through.
            layoutInDisplayCutoutMode = WindowManager.LayoutParams.LAYOUT_IN_DISPLAY_CUTOUT_MODE_ALWAYS
        }
        runCatching { getSystemService(WindowManager::class.java).addView(scroll, params) }
            .onSuccess { overlay = scroll }
        scheduleTick()
    }

    private fun screen(decision: BlockDecision, budget: BudgetView?, fetchedAt: Instant?): LinearLayout {
        val density = resources.displayMetrics.density
        val pad = (28 * density).toInt()
        val now = Instant.now()
        waitText = null
        val container = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            gravity = Gravity.CENTER_VERTICAL
            setPadding(pad, pad, pad, pad)
        }
        fun text(value: String, size: Float, color: Int = dim, bold: Boolean = false, bottom: Int = pad / 3) = TextView(this).apply {
            text = value
            textSize = size
            setTextColor(color)
            if (bold) typeface = Typeface.DEFAULT_BOLD
            setPadding(0, 0, 0, bottom)
        }.also(container::addView)
        fun label(value: String) = text(value, 12f, bold = true, bottom = pad / 6).apply { letterSpacing = 0.12f; setPadding(0, pad / 3, 0, pad / 6) }
        fun button(value: String, onClick: () -> Unit) = Button(this).apply {
            text = value
            isEnabled = !doorBusy
            setOnClickListener { onClick() }
        }.also(container::addView)

        text(NowModel.modeLabel(decision.mode).uppercase(), 15f, bold = true).letterSpacing = 0.12f
        text(decision.reason, 26f, bright, bottom = pad / 2)
        text("This app is closed right now.", 16f, bottom = pad / 2)
        val door = budget?.door
        when {
            budget == null -> {
                button("Go back", ::goHome)
                // With the budget off nothing changes: the emergency unlock stays.
                button("Emergency: open everything for ${Emergency.LENGTH.toMinutes()} min") {
                    removeOverlay()
                    Thread { runCatching { Emergency.start(this@TelemetryAccessibilityService) } }.start()
                }
            }
            door != null && door.waiting -> {
                waitText = text(BudgetText.wait(door, now), 30f, bright, bold = true)
                text("Cancelling now costs nothing.", 15f, bottom = pad / 2)
                button("Cancel") { cancelDoor(door.id) }
                button("Go back", ::goHome)
            }
            door != null && door.open -> {
                text("The door is open; this screen goes once the phone has the new times.", 15f)
                button("Go back", ::goHome)
            }
            else -> {
                if (budget.doorAvailable) {
                    text(BudgetText.rate(budget.rate) + " " + BudgetText.cost(budget), 18f, bright)
                } else {
                    text(BudgetText.unavailable(budget, zone), 18f, bright)
                }
                BudgetText.bedtime(budget, now, BlockPolicy.bedtimeUntil(entry()?.second.orEmpty().ifBlank { "{}" }, now), zone)?.let { text(it, 16f, bright) }
                if (budget.habits.isNotEmpty()) {
                    label("TODAY'S HABITS")
                    budget.habits.forEach { text(BudgetText.habit(it), 16f, if (it.done) dim else bright, bottom = pad / 8) }
                }
                if (budget.reasons.isNotEmpty()) {
                    label("YOUR REASONS")
                    budget.reasons.forEach { text(it, 16f, bright, bottom = pad / 8) }
                }
                container.addView(View(this), LinearLayout.LayoutParams(1, pad / 2))
                if (budget.doorAvailable) {
                    button("Back", ::back)
                    button(if (doorBusy) "Asking the server…" else "Still want it", ::stillWantIt)
                } else {
                    button("Go back", ::goHome)
                }
            }
        }
        doorMessage?.let { text(it, 16f, bright).setPadding(0, pad / 2, 0, 0) }
        if (budget != null && fetchedAt != null && Duration.between(fetchedAt, now) > STALE) {
            text("Figures from ${BudgetText.clock(fetchedAt, zone)}.", 13f).setPadding(0, pad / 2, 0, 0)
        }
        return container
    }

    private fun goHome() {
        performGlobalAction(GLOBAL_ACTION_HOME)
        // The home screen's own event follows; until then nothing is in front.
        foreground = ""
        removeOverlay()
    }

    /** Back: home, recorded as a door declined. */
    private fun back() {
        val pkg = blockedPackage
        val decision = currentDecision()
        goHome()
        val app = applicationContext
        Thread { runCatching { Door.back(app, decision, pkg) } }.start()
    }

    private fun stillWantIt() = doorCall { Door.request(it) }

    private fun cancelDoor(id: Long) = doorCall { Door.cancel(it, id) }

    private fun doorCall(call: (android.content.Context) -> Door.Outcome) {
        if (doorBusy) return
        doorBusy = true
        doorMessage = null
        currentDecision()?.let(::showOverlay)
        val app = applicationContext
        Thread {
            val outcome = runCatching { call(app) }.getOrDefault(Door.Outcome.Unreachable)
            main.post {
                doorBusy = false
                doorMessage = when (outcome) {
                    is Door.Outcome.Done -> null
                    is Door.Outcome.Refused -> outcome.message
                    Door.Outcome.Unreachable -> Door.UNREACHABLE
                }
                cached = null
                val decision = currentDecision()
                if (overlay != null && decision != null && BlockPolicy.shouldBlock(decision, blockedPackage, systemSafe)) showOverlay(decision)
                scheduleTick()
            }
        }.start()
    }

    private fun removeOverlay() {
        overlay?.let { view -> runCatching { getSystemService(WindowManager::class.java).removeView(view) } }
        overlay = null
        overlayKey = ""
        waitText = null
    }

    // The counter and the bedtime chip, in one small corner that takes no input.

    private fun refreshCorner() {
        val now = Instant.now()
        val interactive = runCatching { getSystemService(PowerManager::class.java).isInteractive }.getOrDefault(true)
        val budget = budget()
        val entry = entry()
        val decision = entry?.let { BlockPolicy.current(this, it.second, now) }
        val pkg = foreground
        val distraction = interactive && overlay == null && budget != null && entry != null && pkg.isNotBlank() &&
            pkg in entry.third && pkg !in systemSafe && decision?.alwaysAllowed?.contains(pkg) != true &&
            !BlockPolicy.shouldBlock(decision, pkg, systemSafe)
        if (distraction) sitting.enter(now) else sitting.leave(now)
        val lines = when {
            distraction -> CounterText.lines(budget!!, sitting.used(now), sitting.usedSince(entry!!.first, now), now, zone)
            // Not over Telemetry itself: Today says when bedtime is (owner 2026-11-26).
            interactive && overlay == null && budget != null && budget.windDown && budget.rate.kind != "bedtime" && pkg != packageName ->
                listOfNotNull(BudgetText.bedtime(budget, now, null, zone))
            else -> emptyList()
        }
        if (lines.isEmpty()) removeCorner() else showCorner(lines.joinToString("\n"))
        if (lines.isNotEmpty()) scheduleTick()
    }

    private fun showCorner(value: String) {
        corner?.let { view ->
            if (view.text != value) view.text = value
            return
        }
        val density = resources.displayMetrics.density
        val view = TextView(this).apply {
            text = value
            textSize = 12f
            setTextColor(bright)
            setPadding((10 * density).toInt(), (6 * density).toInt(), (10 * density).toInt(), (6 * density).toInt())
            background = GradientDrawable().apply {
                cornerRadius = 10 * density
                setColor(Color.argb(200, 17, 18, 19))
            }
        }
        val params = WindowManager.LayoutParams(
            WindowManager.LayoutParams.WRAP_CONTENT,
            WindowManager.LayoutParams.WRAP_CONTENT,
            WindowManager.LayoutParams.TYPE_ACCESSIBILITY_OVERLAY,
            WindowManager.LayoutParams.FLAG_NOT_FOCUSABLE or WindowManager.LayoutParams.FLAG_NOT_TOUCHABLE or
                WindowManager.LayoutParams.FLAG_LAYOUT_IN_SCREEN,
            PixelFormat.TRANSLUCENT,
        ).apply {
            gravity = Gravity.TOP or Gravity.END
            x = (8 * density).toInt()
            y = (40 * density).toInt()
        }
        runCatching { getSystemService(WindowManager::class.java).addView(view, params) }.onSuccess { corner = view }
    }

    private fun removeCorner() {
        corner?.let { view -> runCatching { getSystemService(WindowManager::class.java).removeView(view) } }
        corner = null
    }

    // Every few seconds while something here is on screen or a door waits.

    private fun scheduleTick() {
        if (ticking) return
        ticking = true
        main.postDelayed(::tick, TICK_MS)
    }

    private fun tick() {
        ticking = false
        val now = Instant.now()
        val decision = currentDecision()
        val door = budget()?.door
        // The door's wait ran out: fetch the new state once and lift suspensions.
        if (door != null && door.waiting && door.opensAt?.isAfter(now) == false && reconciledDoor != door.id) {
            reconciledDoor = door.id
            val app = applicationContext
            Thread {
                runCatching { PolicyWatch.check(app) }
                main.post {
                    cached = null
                    tick()
                }
            }.start()
        }
        if (overlay != null) {
            if (!BlockPolicy.shouldBlock(decision, blockedPackage, systemSafe)) {
                // Opened meanwhile (the door, or the policy changed): the app is underneath.
                removeOverlay()
            } else {
                showOverlay(decision!!)
                if (door != null && door.waiting) waitText?.text = BudgetText.wait(door, now)
            }
        } else if (foreground.isNotBlank() && BlockPolicy.shouldBlock(decision, foreground, systemSafe)) {
            // The sliding end passed while the app was in front.
            block(foreground, decision!!)
        }
        refreshCorner()
        if (overlay != null || corner != null || notice != null || door?.waiting == true) scheduleTick()
    }

    // The grayscale notice: asked once each time it is switched off while wanted.

    /** Shows the notice unless it is up already. Safe from any thread. */
    fun askGrayscale(surcharge: Double?) {
        main.post { if (notice == null) showNotice(surcharge) }
    }

    fun dismissGrayscaleNotice() {
        if (!noticeShown) return
        main.post(::removeNotice)
    }

    private val noticeTimeout = Runnable {
        val app = applicationContext
        Thread { runCatching { Grayscale.switchBack(app) } }.start()
        removeNotice()
    }

    private fun showNotice(surcharge: Double?) {
        val density = resources.displayMetrics.density
        val pad = (14 * density).toInt()
        val app = applicationContext
        val box = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(pad, pad, pad, pad)
            background = GradientDrawable().apply {
                cornerRadius = 14 * density
                setColor(Color.argb(235, 17, 18, 19))
            }
        }
        val message = TextView(this).apply {
            text = "Grayscale is off. It switches back in a minute if neither is chosen."
            textSize = 14f
            setTextColor(bright)
        }
        box.addView(message)
        val keep = Button(this).apply {
            text = surcharge?.let { "Keep it off: +${BudgetText.number(it)}× until bedtime ends" } ?: "Keep it off: costs more until bedtime ends"
        }
        val switchBack = Button(this).apply { text = "Switch back" }
        keep.setOnClickListener {
            main.removeCallbacks(noticeTimeout)
            keep.isEnabled = false
            switchBack.isEnabled = false
            Thread {
                val outcome = runCatching { Grayscale.keepOff(app) }.getOrDefault(Door.Outcome.Unreachable)
                main.post {
                    when (outcome) {
                        is Door.Outcome.Done -> removeNotice()
                        else -> {
                            message.text = (if (outcome is Door.Outcome.Refused) outcome.message else "The server can't be reached.") + " Grayscale is back on."
                            main.postDelayed(::removeNotice, 4_000L)
                        }
                    }
                }
            }.start()
        }
        switchBack.setOnClickListener {
            Thread { runCatching { Grayscale.switchBack(app) } }.start()
            removeNotice()
        }
        box.addView(keep)
        box.addView(switchBack)
        val params = WindowManager.LayoutParams(
            WindowManager.LayoutParams.MATCH_PARENT,
            WindowManager.LayoutParams.WRAP_CONTENT,
            WindowManager.LayoutParams.TYPE_ACCESSIBILITY_OVERLAY,
            WindowManager.LayoutParams.FLAG_NOT_FOCUSABLE or WindowManager.LayoutParams.FLAG_NOT_TOUCH_MODAL,
            PixelFormat.TRANSLUCENT,
        ).apply {
            gravity = Gravity.BOTTOM
            y = (24 * density).toInt()
            horizontalMargin = 0.04f
        }
        runCatching { getSystemService(WindowManager::class.java).addView(box, params) }.onSuccess {
            notice = box
            noticeShown = true
            main.postDelayed(noticeTimeout, NOTICE_TIMEOUT_MS)
            scheduleTick()
        }
    }

    private fun removeNotice() {
        main.removeCallbacks(noticeTimeout)
        notice?.let { view -> runCatching { getSystemService(WindowManager::class.java).removeView(view) } }
        notice = null
        noticeShown = false
    }
}
