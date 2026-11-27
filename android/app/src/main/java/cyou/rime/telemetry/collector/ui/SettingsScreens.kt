package cyou.rime.telemetry.collector.ui

import android.graphics.BitmapFactory
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.graphics.FilterQuality
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.em
import androidx.compose.ui.unit.sp
import cyou.rime.telemetry.collector.ActivityText
import cyou.rime.telemetry.collector.Bedtime
import cyou.rime.telemetry.collector.BudgetFields
import cyou.rime.telemetry.collector.BudgetNumbers
import cyou.rime.telemetry.collector.BudgetText
import cyou.rime.telemetry.collector.LockView
import cyou.rime.telemetry.collector.SettingsChange
import cyou.rime.telemetry.collector.SettingsData
import cyou.rime.telemetry.collector.SettingsPage
import cyou.rime.telemetry.collector.SettingsRules
import cyou.rime.telemetry.collector.SettingsSection
import java.time.Instant
import java.time.ZoneId

/** Every setting in one grouped list, as the panel's Settings; each row shows its value and opens its page. */
@Composable
fun SettingsScreen(data: SettingsData, now: Instant, zone: ZoneId, onPhone: (SettingsSection) -> Unit) {
    val page = data.page
    if (page != null) {
        BackHandler { data.open(null) }
        AppPage {
            ActWord("‹ Settings") { data.open(null) }
            PageHead(page.title)
            when (page) {
                SettingsPage.BUDGET -> BudgetPage(data, now, zone)
                SettingsPage.TIMES -> TimesPage(data, now, zone)
                SettingsPage.DISTRACTIONS -> DistractionsPage(data, now, zone)
                SettingsPage.LOCK -> LockPage(data, now, zone)
                SettingsPage.DEVICES -> DevicesPage(data, now, zone)
                SettingsPage.SOURCES -> SourcesPage(data, now, zone)
            }
        }
        return
    }
    AppPage {
        PageHead("Settings")
        // Unreachable or refused: said once, over the list.
        listOf(data.policy, data.lock).firstNotNullOfOrNull { staleLine(it, now, zone)?.takeIf { line -> line != "Loading…" } }?.let {
            Small(it)
            Gap(8.dp)
        }
        val policy = data.policy.value
        val lock = data.lock.value
        Group("Blocking")
        Rows(
            listOf(
                Triple(SettingsPage.BUDGET.title, SettingsRules.budgetValue(policy, now)) { data.open(SettingsPage.BUDGET) },
                Triple(SettingsPage.TIMES.title, SettingsRules.timesValue(policy, lock)) { data.open(SettingsPage.TIMES) },
                Triple(SettingsPage.DISTRACTIONS.title, SettingsRules.distractionsValue(policy, data.labels.value)) { data.open(SettingsPage.DISTRACTIONS) },
                Triple(SettingsPage.LOCK.title, SettingsRules.lockValue(lock, zone)) { data.open(SettingsPage.LOCK) },
            ),
        )
        if (data.waiting.isNotEmpty()) Small("${data.waiting.joinToString(", ") { it.label }}: waiting to be sent.", Modifier.padding(top = 6.dp))
        Group("This phone")
        Rows(
            listOf(
                Triple("Permissions", "") { onPhone(SettingsSection.PERMISSIONS) },
                Triple("Notifications", "") { onPhone(SettingsSection.NOTIFICATIONS) },
            ),
        )
        Group("System")
        Rows(
            listOf(
                Triple(SettingsPage.DEVICES.title, SettingsRules.devicesValue(data.devices.value)) { data.open(SettingsPage.DEVICES) },
                Triple(SettingsPage.SOURCES.title, SettingsRules.sourcesValue(data.collection.value)) { data.open(SettingsPage.SOURCES) },
            ),
        )
    }
}

@Composable
private fun Group(title: String) {
    val colors = panelColors()
    Text(title.uppercase(), Modifier.padding(top = 18.dp, bottom = 6.dp),
        style = TextStyle(fontFamily = Mono, fontSize = 11.5.sp, fontWeight = FontWeight.Bold, letterSpacing = 0.08.em, color = colors.muted))
}

@Composable
private fun Rows(rows: List<Triple<String, String, () -> Unit>>) {
    val colors = panelColors()
    Column(Modifier.fillMaxWidth().background(colors.panel).border(1.dp, colors.fg)) {
        rows.forEachIndexed { index, (label, value, open) ->
            Row(
                Modifier.fillMaxWidth().heightIn(min = 50.dp)
                    .drawBehind { if (index > 0) drawRect(colors.line, Offset.Zero, Size(size.width, 1.dp.toPx())) }
                    .semantics { contentDescription = if (value.isEmpty()) label else "$label, $value" }
                    .clickable(role = Role.Button, onClick = open)
                    .padding(horizontal = 14.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(10.dp),
            ) {
                PText(label, Modifier.weight(1f), size = 14.5.sp)
                if (value.isNotEmpty()) Text(value, Modifier.weight(0.8f, fill = false), maxLines = 1, overflow = TextOverflow.Ellipsis, textAlign = TextAlign.End,
                    style = TextStyle(fontFamily = Mono, fontSize = 12.5.sp, color = colors.muted))
                PText("›", color = colors.muted, size = 17.sp)
            }
        }
    }
}

/** A field for a number or a clock time, with the panel's underline. */
@Composable
private fun Field(value: String, label: String, modifier: Modifier, keyboard: KeyboardType = KeyboardType.Number, secret: Boolean = false, onChange: (String) -> Unit) {
    val colors = panelColors()
    BasicTextField(
        value = value,
        onValueChange = onChange,
        modifier = modifier.semantics { contentDescription = label }
            .drawBehind { drawRect(colors.fg, Offset(0f, size.height - 1.dp.toPx()), Size(size.width, 1.dp.toPx())) }
            .padding(top = 8.dp, bottom = 6.dp),
        singleLine = true,
        textStyle = TextStyle(fontFamily = Mono, fontSize = 15.sp, color = colors.fg),
        cursorBrush = SolidColor(colors.fg),
        visualTransformation = if (secret) PasswordVisualTransformation() else VisualTransformation.None,
        keyboardOptions = KeyboardOptions(keyboardType = keyboard),
    )
}

@Composable
private fun Check(label: String, checked: Boolean, enabled: Boolean = true, onChange: (Boolean) -> Unit) {
    Row(Modifier.tapRow(label, enabled) { onChange(!checked) }, verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(10.dp)) {
        SquareCheck(checked, enabled)
        PText(label, size = 14.sp)
    }
}

/** A labelled line of a form: the label on the left, the field on the right. */
@Composable
private fun Labeled(label: String, unit: String = "", content: @Composable () -> Unit) {
    Row(Modifier.fillMaxWidth().padding(top = 4.dp), verticalAlignment = Alignment.CenterVertically) {
        Small(label, Modifier.weight(1f), color = panelColors().fg)
        Box(Modifier.width(84.dp)) { content() }
        Small(unit, Modifier.width(32.dp).padding(start = 6.dp))
    }
}

/** A change waiting on the phone, or what the server said to the last one. */
@Composable
private fun ChangeState(data: SettingsData, kind: String, zone: ZoneId) {
    data.waiting.firstOrNull { it.kind == kind }?.let {
        Small("Waiting to be sent: made at ${BudgetText.clock(it.madeAt, zone)}. Until the server takes it, the rules in force stay as they are.", Modifier.padding(top = 6.dp))
    }
    data.notices[kind]?.let { Small(it, Modifier.padding(top = 6.dp), color = panelColors().fg) }
}

/** A question before a save: why it needs a second look, and Save anyway or Cancel. */
@Composable
private fun Confirm(message: String, onYes: () -> Unit, onNo: () -> Unit) {
    val colors = panelColors()
    Column(Modifier.fillMaxWidth().padding(top = 8.dp).border(1.dp, colors.fg).padding(10.dp)) {
        PText("$message Save anyway?", size = 13.5.sp)
        Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            ActWord("Save anyway", bold = true, onClick = onYes)
            ActWord("Cancel", onClick = onNo)
        }
    }
}

/**
 * While the settings lock holds, the rules are not shown: only the PIN,
 * entered by the person who holds it (owner 2026-10-31), as in the panel.
 */
@Composable
private fun PinGate(data: SettingsData, lock: LockView, zone: ZoneId) {
    var pin by remember { mutableStateOf("") }
    PanelCard("These settings are locked") {
        Small(
            (lock.until?.let { "Locked until ${BudgetText.clock(it, zone)}. " } ?: "Locked at all times. ") +
                "The person who holds the PIN enters it here; it opens the rules for ${lock.unlockMinutes} minutes, and changes made then apply at once.",
            Modifier.padding(top = 6.dp),
        )
        if (!lock.pinOpen) Small("The PIN is accepted only at ${SettingsRules.formatWindows(lock.pinWindows)}.", Modifier.padding(top = 4.dp))
        val open = lock.pinOpen && lock.triesLeft > 0 && !data.busy
        Row(verticalAlignment = Alignment.CenterVertically) {
            Field(pin, "PIN", Modifier.weight(1f), KeyboardType.NumberPassword, secret = true) { pin = it.take(32) }
            ActWord(if (data.busy) "Checking…" else "Unlock", enabled = open && pin.isNotEmpty(), bold = true) {
                data.unlock(pin)
                pin = ""
            }
        }
        Small("${lock.triesLeft} ${if (lock.triesLeft == 1) "try" else "tries"} left this hour")
        data.notices["pin"]?.let { Small(it, Modifier.padding(top = 4.dp), color = panelColors().fg) }
    }
}

private fun LockView.holds(): Boolean = enabled && locked

@Composable
private fun BudgetPage(data: SettingsData, now: Instant, zone: ZoneId) {
    staleLine(data.policy, now, zone)?.let { Small(it); Gap(8.dp) }
    val lock = data.lock.value
    if (lock != null && lock.holds()) {
        PinGate(data, lock, zone)
        return
    }
    val policy = data.policy.value ?: return
    val current = policy.budgetInForce(now)
    lock?.unlockedUntil?.let { Small("Unlocked with the PIN until ${BudgetText.clock(it, zone)}. Changes made now apply at once.", Modifier.padding(bottom = 8.dp)) }
    var enabled by remember { mutableStateOf(current?.enabled ?: false) }
    val fields = remember { mutableStateOf(BudgetFields.all.associate { it.key to number(current?.get(it.key) ?: 0.0) }) }
    var problem by remember { mutableStateOf<String?>(null) }
    var confirming by remember { mutableStateOf<BudgetNumbers?>(null) }
    PanelCard("Distraction budget") {
        Small(SettingsRules.budgetSummary(current), Modifier.padding(top = 6.dp))
        val waiting = policy.budgetNext?.takeIf { policy.budgetNextFrom != null && !SettingsRules.started(policy.budgetNextFrom, now, policy.zone) }
        if (waiting != null) Small("From ${SettingsRules.waitStart(policy.budgetNextFrom, zone)}: ${SettingsRules.budgetSummary(waiting)}", Modifier.padding(top = 4.dp))
        Small("1× hours are the distraction times" + (if (policy.distractionTimes.isNotEmpty()) ": ${SettingsRules.formatWindows(policy.distractionTimes)}" else ", none set") +
            ". Habits earn the rest of the day's budget up to the maximum.", Modifier.padding(top = 4.dp))
        Gap(6.dp)
        Check("On", enabled) { enabled = it }
        if (enabled && !policy.closedByDefault) Small("The budget needs distractions closed by default, which is off now.")
        for ((title, group) in BudgetFields.groups) {
            Small(title, Modifier.padding(top = 10.dp), color = panelColors().fg)
            for (field in group) {
                Labeled(field.label, field.unit) {
                    Field(fields.value[field.key].orEmpty(), field.label, Modifier.fillMaxWidth(), if (field.whole) KeyboardType.Number else KeyboardType.Decimal) { text ->
                        fields.value = fields.value + (field.key to text.filter { it.isDigit() || (!field.whole && it == '.') }.take(7))
                    }
                }
            }
        }
        Row(Modifier.padding(top = 6.dp), verticalAlignment = Alignment.CenterVertically) {
            ActWord("Save", bold = true) {
                problem = null
                val values = mutableMapOf<String, Double>()
                for (field in BudgetFields.all) {
                    val value = fields.value[field.key]?.toDoubleOrNull()
                    val min = if (enabled) field.min else 0.0
                    if (value == null || value < min || value > field.max) {
                        problem = "${field.label}: from ${number(min)} to ${number(field.max)}."
                        return@ActWord
                    }
                    values[field.key] = value
                }
                val next = BudgetNumbers(enabled, values)
                if (SettingsRules.budgetAsksLess(current, next)) confirming = next else data.saveBudget(next)
            }
            Small("Asking less starts after the next bedtime. Asking more applies at once.", Modifier.weight(1f))
        }
        problem?.let { Small(it, color = panelColors().fg) }
        confirming?.let { next ->
            Confirm("This asks less than now, so it only starts when the next bedtime ends; until then the budget stays as it is. It is recorded as a loosening.",
                onYes = { confirming = null; data.saveBudget(next) }, onNo = { confirming = null })
        }
        ChangeState(data, SettingsChange.BUDGET, zone)
    }
}

private fun number(value: Double): String = if (value == Math.floor(value)) value.toLong().toString() else value.toString()

@Composable
private fun TimesPage(data: SettingsData, now: Instant, zone: ZoneId) {
    staleLine(data.lock, now, zone)?.let { Small(it); Gap(8.dp) }
    val policy = data.policy.value
    val lock = data.lock.value
    if (lock != null && lock.holds()) PinGate(data, lock, zone)
    else if (lock != null) BedtimeCard(data, lock, zone)
    PanelCard("Distraction times") {
        val times = policy?.distractionTimes.orEmpty()
        Small(if (policy == null) "Unknown: never saved on this phone." else if (times.isEmpty()) "None set." else SettingsRules.formatWindows(times), Modifier.padding(top = 6.dp), color = panelColors().fg)
        Small("Distraction times themselves are set through the assistant.", Modifier.padding(top = 4.dp))
    }
}

@Composable
private fun BedtimeCard(data: SettingsData, lock: LockView, zone: ZoneId) {
    val current = lock.bedtime
    var enabled by remember { mutableStateOf(current.enabled) }
    var start by remember { mutableStateOf(current.start) }
    var end by remember { mutableStateOf(current.end) }
    var gray by remember { mutableStateOf(current.windDownMinutes.toString()) }
    var problem by remember { mutableStateOf<String?>(null) }
    var confirming by remember { mutableStateOf<Pair<Bedtime, String>?>(null) }
    lock.unlockedUntil?.let { Small("Unlocked with the PIN until ${BudgetText.clock(it, zone)}. Changes made now apply at once.", Modifier.padding(bottom = 8.dp)) }
    PanelCard("Bedtime") {
        Small((if (current.enabled) "On, ${current.start}–${current.end}" + (if (current.windDownMinutes > 0) ", screens gray from ${current.windDownMinutes} min before." else ".") else "Off.") +
            " Screens lock when used; nothing is closed, so agents keep running.", Modifier.padding(top = 6.dp))
        lock.bedtimeNext?.let { next ->
            Small("From ${SettingsRules.waitStart(lock.bedtimeNextFrom, zone)}: ${if (next.enabled) "${next.start}–${next.end}" else "off"}.", Modifier.padding(top = 4.dp))
        }
        Check("On", enabled) { enabled = it }
        Labeled("From") { Field(start, "Bedtime from, HH:MM", Modifier.fillMaxWidth(), KeyboardType.Ascii) { start = it.take(5) } }
        Labeled("to") { Field(end, "Bedtime to, HH:MM", Modifier.fillMaxWidth(), KeyboardType.Ascii) { end = it.take(5) } }
        Labeled("gray from", "min") { Field(gray, "Gray from minutes before", Modifier.fillMaxWidth()) { gray = it.filter(Char::isDigit).take(3) } }
        ActWord("Save", bold = true) {
            problem = null
            val clock = Regex("([01][0-9]|2[0-3]):[0-5][0-9]")
            val minutes = gray.toIntOrNull()
            if (!clock.matches(start) || !clock.matches(end) || start == end) { problem = "Write times as 23:00 and 08:00."; return@ActWord }
            if (minutes == null || minutes > 180) { problem = "Gray from: 0 to 180 minutes."; return@ActWord }
            val next = Bedtime(enabled, start, end, minutes)
            val parts = buildList {
                val hours = SettingsRules.bedtimeHours(start, end)
                if (enabled && hours > 12) add("Bedtime would last ${number(hours)} hours, from $start to $end.")
                if (SettingsRules.bedtimeAsksLess(current, next)) add("This asks less than now, so it only starts when the next bedtime ends; until then it stays as it is.")
            }
            if (parts.isEmpty()) data.saveBedtime(next) else confirming = next to parts.joinToString(" ")
        }
        problem?.let { Small(it, color = panelColors().fg) }
        confirming?.let { (next, message) -> Confirm(message, onYes = { confirming = null; data.saveBedtime(next) }, onNo = { confirming = null }) }
        ChangeState(data, SettingsChange.BEDTIME, zone)
    }
}

@Composable
private fun DistractionsPage(data: SettingsData, now: Instant, zone: ZoneId) {
    val colors = panelColors()
    staleLine(data.policy, now, zone)?.let { Small(it); Gap(8.dp) }
    val policy = data.policy.value
    PanelCard("Blocked apps and sites") {
        when {
            policy == null -> Small("Unknown: never saved on this phone.", Modifier.padding(top = 6.dp))
            policy.groups.isEmpty() -> Small("No blocked groups.", Modifier.padding(top = 6.dp))
            else -> policy.groups.forEach { group ->
                Column(Modifier.fillMaxWidth().padding(top = 8.dp)) {
                    PText(group.name, size = 14.sp, weight = FontWeight.Bold)
                    if (group.apps.isNotEmpty()) Small("Apps: ${group.apps.joinToString(", ")}")
                    if (group.sites.isNotEmpty()) Small("Sites: ${group.sites.joinToString(", ")}")
                }
            }
        }
    }
    PanelCard("Labels") {
        val labels = data.labels.value
        staleLine(data.labels, now, zone)?.takeIf { labels == null }?.let { Small(it, Modifier.padding(top = 6.dp)) }
        when {
            labels == null -> Unit
            labels.isEmpty() -> Small("No labels yet.", Modifier.padding(top = 6.dp))
            else -> labels.sortedWith(compareBy({ it.label }, { it.match })).forEach { label ->
                Row(Modifier.fillMaxWidth().drawBehind {
                    drawRect(colors.line, Offset(0f, size.height - 1.dp.toPx()), Size(size.width, 1.dp.toPx()))
                }.padding(vertical = 5.dp), verticalAlignment = Alignment.CenterVertically) {
                    PText(label.match, Modifier.weight(1f), size = 13.5.sp, maxLines = 1)
                    Small("${if (label.kind == "site") "site" else "app"} · ${label.label}")
                }
            }
        }
        Small("Labels change through the assistant: it shows the exact change, and you approve it.", Modifier.padding(top = 6.dp))
    }
    val lock = data.lock.value
    if (lock != null) PanelCard("Browser extension") {
        Small((if (lock.extensionEnabled) "On: a browser without a running extension is hidden after ${lock.extensionQuietSeconds} s while sites are blocked." else "Off.") +
            " Hidden, never quit; its Extensions page stays usable. Once on, it stays on.", Modifier.padding(top = 6.dp))
    }
}

@Composable
private fun LockPage(data: SettingsData, now: Instant, zone: ZoneId) {
    staleLine(data.lock, now, zone)?.let { Small(it); Gap(8.dp) }
    val lock = data.lock.value ?: return
    PanelCard("Settings lock", SettingsRules.lockValue(lock, zone)) {
        Small(
            if (!lock.enabled) "Off. Blocking settings can be changed at any time; loosening still waits for the next day."
            else "Locked at ${SettingsRules.formatWindows(lock.windows).ifEmpty { "all times" }}. The PIN is accepted at ${SettingsRules.formatWindows(lock.pinWindows).ifEmpty { "any time" }}; it opens the rules for ${lock.unlockMinutes} minutes.",
            Modifier.padding(top = 6.dp),
        )
        if (!lock.pinSet) Small("No PIN is set.", Modifier.padding(top = 4.dp))
        Small("The lock itself is changed in the panel.", Modifier.padding(top = 4.dp))
    }
    if (lock.holds()) PinGate(data, lock, zone)
    else lock.unlockedUntil?.let { Small("Unlocked with the PIN until ${BudgetText.clock(it, zone)}. Changes made now apply at once.") }
}

@Composable
private fun DevicesPage(data: SettingsData, now: Instant, zone: ZoneId) {
    val colors = panelColors()
    PanelCard("Add a device") {
        Small("A setup code connects a new device at once. It is used once.", Modifier.padding(top = 6.dp))
        ActWord(if (data.busy) "Making a code…" else "Make a setup code", enabled = !data.busy, bold = true) { data.addDevice() }
        data.notices["devices"]?.let { Small(it, color = colors.fg) }
        data.setupCode?.let { code ->
            code.qrPng?.let { png ->
                val bitmap = remember(png) { BitmapFactory.decodeByteArray(png, 0, png.size)?.asImageBitmap() }
                if (bitmap != null) Image(bitmap, "Setup code as a QR code", Modifier.padding(top = 6.dp).size(220.dp).background(androidx.compose.ui.graphics.Color.White), filterQuality = FilterQuality.None)
            }
            SelectionContainer {
                Column {
                    if (code.code.isNotBlank()) PText("Code: ${code.code}", Modifier.padding(top = 6.dp), size = 14.sp)
                    Small("Phone: ${code.setupLink}", Modifier.padding(top = 4.dp))
                    Small("Other devices: ${code.joinUrl}", Modifier.padding(top = 4.dp))
                }
            }
            code.expiresAt?.let { Small("Good until ${BudgetText.clock(it, zone)}.", Modifier.padding(top = 4.dp)) }
        }
    }
    PanelCard("Connected devices") {
        staleLine(data.devices, now, zone)?.let { Small(it, Modifier.padding(top = 6.dp)) }
        val devices = data.devices.value
        when {
            devices == null -> Unit
            devices.isEmpty() -> Small("No devices", Modifier.padding(top = 6.dp))
            else -> devices.forEach { device ->
                Column(Modifier.fillMaxWidth().drawBehind {
                    drawRect(colors.line, Offset(0f, size.height - 1.dp.toPx()), Size(size.width, 1.dp.toPx()))
                }.padding(vertical = 8.dp)) {
                    PText(device.name, size = 14.sp, weight = FontWeight.Bold, color = if (device.revoked) colors.muted else colors.fg)
                    Small(device.id)
                    if (device.revoked) Small("Removed · add it again with a setup code")
                }
            }
        }
        Small("Removing a device is done in the panel.", Modifier.padding(top = 6.dp))
    }
}

@Composable
private fun SourcesPage(data: SettingsData, now: Instant, zone: ZoneId) {
    val colors = panelColors()
    staleLine(data.collection, now, zone)?.let { Small(it); Gap(8.dp) }
    val collection = data.collection.value ?: return
    fun ago(at: Instant?): String = at?.let {
        val minutes = java.time.Duration.between(it, now).toMinutes().coerceAtLeast(0)
        "${BudgetText.clock(it, zone)}" + if (minutes < 24 * 60) " · ${ActivityText.minutes(minutes.toDouble())} ago" else " · ${SettingsRules.waitStart(it.atZone(zone).toLocalDate().toString(), zone)}"
    } ?: "never"
    PanelCard("Devices", collection.checkedAt?.let { "checked ${BudgetText.clock(it, zone)}" }.orEmpty()) {
        if (collection.devices.isEmpty()) Small("No devices have sent anything.", Modifier.padding(top = 6.dp))
        collection.devices.forEach { (device, last) ->
            Row(Modifier.fillMaxWidth().padding(vertical = 5.dp)) {
                PText(device, Modifier.weight(1f), size = 13.5.sp, maxLines = 1)
                Small("last sent ${ago(last)}")
            }
        }
    }
    PanelCard("Sources", "${collection.sources.size}") {
        collection.sources.sortedByDescending { it.lastCollected }.forEach { source ->
            Column(Modifier.fillMaxWidth().drawBehind {
                drawRect(colors.line, Offset(0f, size.height - 1.dp.toPx()), Size(size.width, 1.dp.toPx()))
            }.padding(vertical = 6.dp)) {
                PText(source.kind, size = 13.5.sp)
                Small("${source.device} · ${"%,d".format(java.util.Locale.UK, source.count)} records · last ${ago(source.lastCollected)}")
            }
        }
    }
}
