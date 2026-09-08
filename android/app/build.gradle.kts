plugins {
    id("com.android.application")
}

android {
    namespace = "cyou.rime.telemetry.collector"
    compileSdk = 36

    defaultConfig {
        applicationId = "cyou.rime.telemetry.collector"
        minSdk = 28
        targetSdk = 36
        versionCode = 1
        versionName = "0.1.0"
    }
}

dependencies {
    implementation("androidx.activity:activity-ktx:1.10.1")
    implementation("androidx.health.connect:connect-client:1.1.0")
    implementation("androidx.lifecycle:lifecycle-runtime-ktx:2.8.7")
    implementation("androidx.work:work-runtime-ktx:2.10.1")
}
