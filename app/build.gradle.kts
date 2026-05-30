plugins {
    alias(libs.plugins.android.application)
    alias(libs.plugins.kotlin.android)
    alias(libs.plugins.kotlin.compose)
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "com.sshcustom.vpnchain"
    compileSdk = 37

    defaultConfig {
        applicationId = "com.sshcustom.vpnchain"
        minSdk = 28
        targetSdk = 37
        versionCode = 20000
        versionName = "2.0.0"
    }

    buildTypes {
        release {
            isMinifyEnabled = true
            isShrinkResources = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
        }
        debug {
            isMinifyEnabled = false
            // No applicationIdSuffix — keep same package for easier root granting
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions { jvmTarget = "17" }
    buildFeatures {
        compose = true
        buildConfig = true
    }

    // Only arm64 — reduces APK size significantly
    splits {
        abi {
            isEnable = true
            reset()
            include("arm64-v8a")
            isUniversalApk = false
        }
    }

    packaging {
        resources { excludes += "/META-INF/{AL2.0,LGPL2.1}" }
    }

    // Prevent rotation from recreating activity (preserves all UI state)
    defaultConfig {
        // handled via Manifest android:configChanges
    }
}

dependencies {
    // ── miuix UI (replaces ALL Material 3) ───────────────────────────────────
    implementation("top.yukonga.miuix.kmp:miuix-ui:0.9.1")
    implementation("top.yukonga.miuix.kmp:miuix-preference:0.9.1")
    implementation("top.yukonga.miuix.kmp:miuix-icons:0.9.1")

    // ── libsu root access ─────────────────────────────────────────────────────
    implementation("com.github.topjohnwu.libsu:core:6.0.0")
    implementation("com.github.topjohnwu.libsu:service:6.0.0")
    implementation("com.github.topjohnwu.libsu:io:6.0.0")

    // ── Networking ────────────────────────────────────────────────────────────
    implementation("com.squareup.okhttp3:okhttp:4.12.0")

    // ── Kotlin ────────────────────────────────────────────────────────────────
    implementation("org.jetbrains.kotlinx:kotlinx-serialization-json:1.7.1")
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-android:1.8.1")

    // ── AndroidX ─────────────────────────────────────────────────────────────
    implementation("androidx.activity:activity-compose:1.10.1")
    implementation("androidx.lifecycle:lifecycle-viewmodel-compose:2.9.0")
    implementation("androidx.lifecycle:lifecycle-runtime-ktx:2.9.0")
    implementation("androidx.core:core-ktx:1.16.0")

    // NOTE: No material3, no material-icons-extended, no navigation-compose —
    // all navigation and UI is handled by miuix primitives directly.
}
