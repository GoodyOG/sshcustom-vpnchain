plugins {
    alias(libs.plugins.android.application)
    alias(libs.plugins.kotlin.android)
    alias(libs.plugins.kotlin.compose)
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "com.sshcustom.vpnchain"
    compileSdk = 35

    defaultConfig {
        applicationId = "com.sshcustom.vpnchain"
        minSdk = 28
        targetSdk = 35
        versionCode = 20305
        versionName = "2.3.5"
    }

    signingConfigs {
        // CI generates a keystore; locally fall back to debug
        val keystoreFile = System.getenv("SIGNING_KEY_PATH")
        if (keystoreFile != null) {
            create("release") {
                storeFile = file(keystoreFile)
                storePassword = System.getenv("SIGNING_STORE_PASSWORD") ?: ""
                keyAlias = System.getenv("SIGNING_KEY_ALIAS") ?: "release"
                keyPassword = System.getenv("SIGNING_KEY_PASSWORD") ?: ""
            }
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = true
            isShrinkResources = true
            proguardFiles(
                getDefaultProguardFile("proguard-android-optimize.txt"),
                "proguard-rules.pro"
            )
            // Use the release signing config if available, else debug
            val sc = signingConfigs.findByName("release")
            if (sc != null) signingConfig = sc
        }
        debug {
            isMinifyEnabled = false
            // Same package ID so root grants survive reinstalls
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

    // Disable lint vital check for release — avoids lint metadata crash
    // with newer Kotlin/AGP combinations
    lint {
        checkReleaseBuilds = false
        abortOnError = false
    }

    // arm64-only split — massive size reduction
    splits {
        abi {
            isEnable = true
            reset()
            include("arm64-v8a")
            isUniversalApk = false
        }
    }

    packaging {
        resources {
            excludes += setOf(
                "/META-INF/{AL2.0,LGPL2.1}",
                "/META-INF/DEPENDENCIES",
                "**/*.kotlin_builtins",
                "**/*.kotlin_module",
                "DebugProbesKt.bin",
            )
        }
    }
}

dependencies {
    // ── miuix 0.7.2 — monolithic, no SDK37, no navigationevent ──────────────
    implementation("top.yukonga.miuix.kmp:miuix:0.7.2")

    // ── libsu root access ─────────────────────────────────────────────────────
    implementation("com.github.topjohnwu.libsu:core:6.0.0")
    implementation("com.github.topjohnwu.libsu:service:6.0.0")
    implementation("com.github.topjohnwu.libsu:io:6.0.0")

    // ── Networking ────────────────────────────────────────────────────────────
    implementation("com.squareup.okhttp3:okhttp:4.12.0")

    // ── Kotlin ────────────────────────────────────────────────────────────────
    implementation("org.jetbrains.kotlinx:kotlinx-serialization-json:1.7.1")
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-android:1.8.1")

    // ── AndroidX — pinned stable SDK-35 versions ──────────────────────────────
    implementation("androidx.activity:activity-compose:1.9.0")
    implementation("androidx.lifecycle:lifecycle-viewmodel-compose:2.7.0")
    implementation("androidx.lifecycle:lifecycle-runtime-ktx:2.7.0")
    implementation("androidx.core:core-ktx:1.13.1")
    // NOTE: appcompat intentionally removed — miuix handles all theming

    // ── Compose foundation — miuix KMP does not transitively export these ─────
    implementation(platform("androidx.compose:compose-bom:2024.06.00"))
    implementation("androidx.compose.foundation:foundation")
    implementation("androidx.compose.foundation:foundation-layout")
    implementation("androidx.compose.runtime:runtime")
    implementation("androidx.compose.ui:ui")
    implementation("androidx.compose.ui:ui-tooling-preview")
    debugImplementation("androidx.compose.ui:ui-tooling")
}
