plugins {
    id("com.android.application")
}

android {
    namespace = "cyou.rime.telemetry.collector"
    compileSdk = 36

    buildFeatures {
        buildConfig = true
    }

    defaultConfig {
        applicationId = "cyou.rime.telemetry.collector"
        minSdk = 28
        targetSdk = 36
        versionCode = 1
        versionName = "0.1.0"
        val telemetryServerUrl = project.findProperty("telemetryServerUrl")?.toString()
            ?.trim()
            ?.trimEnd('/')
            ?.takeIf { it.isNotBlank() }
            ?: "https://server.example.ts.net:8443"
        buildConfigField("String", "TELEMETRY_SERVER_URL", "\"$telemetryServerUrl\"")
    }
}

dependencies {
    testImplementation("junit:junit:4.13.2")
    // JVM tests need real JSON behavior; Android's mockable jar only has stubs.
    testImplementation("org.json:json:20260814")
    implementation("androidx.activity:activity-ktx:1.10.1")
    implementation("androidx.health.connect:connect-client:1.1.0")
    implementation("androidx.lifecycle:lifecycle-runtime-ktx:2.8.7")
    implementation("androidx.work:work-runtime-ktx:2.10.1")
}
